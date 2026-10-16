package api

// Replicación de volúmenes ZFS (protocol/README.md §18.1, ADR 0016). Una tarea manda un
// volumen ZFS de este NAS a otro pool de este NAS o a otro TangoOS vinculado (código y
// huella, como al unir un nodo al clúster). El agente corre zfs send/receive y entrega
// un pipe; tangod lleva los bytes. Cada dataset del volumen viaja por separado y raw:
// un envío cortado se retoma con el token del destino y un volumen cifrado nunca se
// descifra fuera de este NAS.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/internal/zfs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// ZfsReplAgent son las ops del agente para replicar.
type ZfsReplAgent interface {
	ZfsReplState(ctx context.Context, a agentproto.ZfsReplStateArgs) (*agentproto.ZfsReplState, error)
	ZfsReplSnapshot(ctx context.Context, a agentproto.ZfsReplSnapshotArgs) error
	ZfsReplSend(ctx context.Context, a agentproto.ZfsReplSendArgs) (*os.File, agentproto.ZfsStreamResult, error)
	ZfsReplRecv(ctx context.Context, a agentproto.ZfsReplRecvArgs) (*os.File, agentproto.ZfsStreamResult, error)
	ZfsReplWait(ctx context.Context, id string) error
	ZfsReplPrune(ctx context.Context, a agentproto.ZfsReplPruneArgs) error
	ZfsReplMirror(ctx context.Context, a agentproto.ZfsReplMirrorArgs) error
	ZfsReplAbort(ctx context.Context, a agentproto.ZfsReplTargetArgs) error
	ZfsReplDestroy(ctx context.Context, a agentproto.ZfsReplTargetArgs) error
	ZfsReplPromote(ctx context.Context, a agentproto.ZfsReplPromoteArgs) (bool, error)
}

// ReplTask es una tarea de replicación (en el NAS de origen).
type ReplTask struct {
	ID      string   `json:"id"`
	Pool    string   `json:"pool"`
	Vol     string   `json:"vol"`
	Target  string   `json:"target"` // "local:<pool>" o el id de un destino vinculado
	Every   int      `json:"every"`  // horas entre envíos; 0 = solo a mano
	Enabled bool     `json:"enabled"`
	Pending string   `json:"pending,omitempty"` // instantánea de un envío que no terminó
	Last    *ReplRun `json:"last,omitempty"`
	LastOK  string   `json:"lastOk,omitempty"` // RFC 3339 del último envío completo
}

// ReplRun es el resultado del último envío.
type ReplRun struct {
	At      string `json:"at"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Bytes   uint64 `json:"bytes"`
	Seconds int    `json:"seconds"`
	Snap    string `json:"snap,omitempty"`
}

// ReplTarget es otro TangoOS al que este manda réplicas.
type ReplTarget struct {
	ID          string `json:"id"` // 12 hex de la huella de su clave
	Name        string `json:"name"`
	Addr        string `json:"addr"` // host:puerto
	Fingerprint string `json:"fingerprint"`
	SPKI        string `json:"spki"` // SHA-256 hex de su clave pública (el pin)
	AddedAt     string `json:"addedAt"`
}

// ReplSource es otro TangoOS autorizado a mandarle réplicas a este.
type ReplSource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	SPKI        string `json:"spki"`
	AddedAt     string `json:"addedAt"`
}

// ReplReceive: este NAS acepta réplicas de los orígenes vinculados, en Pool.
type ReplReceive struct {
	Enabled bool   `json:"enabled"`
	Pool    string `json:"pool"`
}

type replFile struct {
	Tasks   []ReplTask   `json:"tasks"`
	Targets []ReplTarget `json:"targets"`
	Sources []ReplSource `json:"sources"`
	Receive ReplReceive  `json:"receive"`
}

type zfsRepl struct {
	z     *zfsAPI
	agent ZfsReplAgent
	dir   string

	mu      sync.Mutex
	st      replFile
	running map[string]string // tarea → job
	invite  *replInvite
	probes  map[string]replProbe
	recvMu  sync.Mutex // un receive a la vez por réplica: se toma por volumen en recvLocks
	recvVol map[string]bool
	net     *replNet

	log     *slog.Logger
	changed func()                                      // avisa a la UI (storage.changed "repl")
	poolOK  func(ctx context.Context, pool string) bool // el pool de destino está importado
}

func newZfsRepl(z *zfsAPI, ag ZfsReplAgent) *zfsRepl {
	r := &zfsRepl{z: z, agent: ag, running: map[string]string{}, probes: map[string]replProbe{}, recvVol: map[string]bool{},
		log: z.s.a.d.Log, changed: func() { z.changed("repl") },
		poolOK: func(ctx context.Context, pool string) bool { _, ok := z.pool(ctx, storage.ZfsPoolID(pool)); return ok }}
	if z.s.a.d.StateDir != "" {
		r.dir = filepath.Join(z.s.a.d.StateDir, "repl")
	}
	r.load()
	r.net = newReplNet(r)
	return r
}

func (r *zfsRepl) path() string { return filepath.Join(r.dir, "repl.json") }

func (r *zfsRepl) load() {
	if r.dir == "" {
		return
	}
	b, err := os.ReadFile(r.path())
	if err == nil {
		_ = json.Unmarshal(b, &r.st)
	}
}

// save: con r.mu tomado.
func (r *zfsRepl) save() {
	if r.dir == "" {
		return
	}
	_ = os.MkdirAll(r.dir, 0o700)
	b, _ := json.MarshalIndent(r.st, "", "  ")
	tmp := r.path() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, r.path())
	}
}

func (r *zfsRepl) task(id string) (ReplTask, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.st.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return ReplTask{}, false
}

func (r *zfsRepl) updateTask(id string, f func(t *ReplTask)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.st.Tasks {
		if r.st.Tasks[i].ID == id {
			f(&r.st.Tasks[i])
			r.save()
			return
		}
	}
}

func newReplID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- Destinos (sinks) ------------------------------------------------------------------

// replSink es adonde va una réplica: otro pool de este NAS o otro TangoOS.
type replSink interface {
	State(ctx context.Context, vol string) (*agentproto.ZfsReplState, error)
	Recv(ctx context.Context, vol, rel, baseGUID string, body io.Reader) error
	Mirror(ctx context.Context, vol string, keep map[string][]string) error
	Label() string
}

type localSink struct {
	r    *zfsRepl
	pool string
}

func (s localSink) Label() string { return "el pool " + s.pool + " de este NAS" }

func (s localSink) State(ctx context.Context, vol string) (*agentproto.ZfsReplState, error) {
	return s.r.agent.ZfsReplState(ctx, agentproto.ZfsReplStateArgs{Pool: s.pool, Origin: zfs.ReplLocal, Vol: vol})
}

func (s localSink) Recv(ctx context.Context, vol, rel, base string, body io.Reader) error {
	return s.r.receive(ctx, agentproto.ZfsReplRecvArgs{Pool: s.pool, Origin: zfs.ReplLocal, Vol: vol, Rel: rel, BaseGUID: base}, body)
}

func (s localSink) Mirror(ctx context.Context, vol string, keep map[string][]string) error {
	return s.r.agent.ZfsReplMirror(ctx, agentproto.ZfsReplMirrorArgs{Pool: s.pool, Origin: zfs.ReplLocal, Vol: vol, Keep: keep})
}

// receive copia body al zfs receive del agente y espera a que termine. Lo usan el
// destino local y el servidor de réplicas (otro NAS manda).
func (r *zfsRepl) receive(ctx context.Context, a agentproto.ZfsReplRecvArgs, body io.Reader) error {
	key := a.Pool + "/" + a.Origin + "/" + a.Vol
	r.mu.Lock()
	if r.recvVol[key] {
		r.mu.Unlock()
		return rtcrpc.Errorf(agentproto.CodeBusy, "ya se está recibiendo esa réplica")
	}
	r.recvVol[key] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.recvVol, key)
		r.mu.Unlock()
	}()
	w, res, err := r.agent.ZfsReplRecv(ctx, a)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(w, body)
	w.Close()
	werr := r.agent.ZfsReplWait(context.WithoutCancel(ctx), res.ID)
	if werr != nil {
		return werr
	}
	return cerr
}

func (r *zfsRepl) sink(t ReplTask) (replSink, error) {
	if pool, ok := strings.CutPrefix(t.Target, "local:"); ok {
		return localSink{r: r, pool: pool}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, tg := range r.st.Targets {
		if tg.ID == t.Target {
			return r.net.client(tg), nil
		}
	}
	return nil, errors.New("el destino de la tarea ya no está vinculado")
}

// ---- Un envío ----------------------------------------------------------------------------

// countReader cuenta los bytes que pasan (progreso del job).
type countReader struct {
	r io.Reader
	n *atomic.Uint64
}

func (c countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(uint64(n))
	return n, err
}

// start arranca el envío de una tarea como job (uno a la vez por tarea).
func (r *zfsRepl) start(id string, manual bool) (string, error) {
	t, ok := r.task(id)
	if !ok {
		return "", rtcrpc.Errorf("NOT_FOUND", "no existe la tarea")
	}
	r.mu.Lock()
	if j := r.running[id]; j != "" {
		r.mu.Unlock()
		return j, nil
	}
	r.mu.Unlock()
	sink, err := r.sink(t)
	if err != nil {
		return "", rtcrpc.Errorf("NOT_FOUND", "%v", err)
	}
	var sent atomic.Uint64
	began := time.Now()
	j := r.z.s.jobs.Start("storage.zfs.repl", "Replicando "+t.Vol, "A "+sink.Label(), func(ctx context.Context, h *jobs.Handle) error {
		return r.run(ctx, h.Update, t, sink, &sent)
	}, func(j jobs.Job) {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
		run := &ReplRun{At: time.Now().UTC().Format(time.RFC3339), OK: j.State == jobs.Succeeded, Bytes: sent.Load(), Seconds: int(time.Since(began).Seconds())}
		if !run.OK {
			run.Error = j.Error
		}
		r.updateTask(id, func(t *ReplTask) {
			run.Snap = t.Pending
			if run.OK {
				t.Pending, t.LastOK = "", run.At
			}
			t.Last = run
		})
		r.changed()
		if !run.OK && (manual || t.LastOK != "") {
			r.z.s.a.Notify(Notice{ID: "n_" + j.ID, Level: "warn", App: "tango.storage", Title: "No se pudo replicar " + t.Vol,
				Body: run.Error + " El próximo envío sigue desde donde quedó.", Action: &Action{Label: "Abrir Almacenamiento", App: "tango.storage"}})
		} else if run.OK && manual {
			r.z.s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.storage", Title: "Se replicó " + t.Vol, Body: "Llegó a " + sink.Label() + " (" + humanBytes(run.Bytes) + ")."})
		}
	})
	r.mu.Lock()
	r.running[id] = j.ID
	r.mu.Unlock()
	r.changed()
	return j.ID, nil
}

// run hace un envío: instantánea recursiva (o la de un envío que no terminó), cada
// dataset con lo que le falte al destino, el destino se pone igual que el origen en
// instantáneas y el origen guarda solo la última de la tarea (la base del próximo).
func (r *zfsRepl) run(ctx context.Context, progress func(float64, string), t ReplTask, sink replSink, sent *atomic.Uint64) error {
	snap := t.Pending
	if snap == "" {
		snap = zfs.ReplSnapName(t.ID, time.Now())
		progress(0.02, "Tomando la instantánea "+snap)
		if err := r.agent.ZfsReplSnapshot(ctx, agentproto.ZfsReplSnapshotArgs{Pool: t.Pool, Vol: t.Vol, Name: snap}); err != nil {
			return errors.New(agentMessage(err))
		}
		r.updateTask(t.ID, func(x *ReplTask) { x.Pending = snap })
	}
	src, err := r.agent.ZfsReplState(ctx, agentproto.ZfsReplStateArgs{Pool: t.Pool, Vol: t.Vol})
	if err != nil {
		return errors.New(agentMessage(err))
	}
	if !src.Exists {
		return fmt.Errorf("no existe el volumen %s", t.Vol)
	}
	progress(0.04, "Comparando con "+sink.Label())
	dst, err := sink.State(ctx, t.Vol)
	if err != nil {
		return fmt.Errorf("no se pudo leer el destino: %s", agentMessage(err))
	}
	// Si la instantánea pendiente ya no existe en el origen (alguien la borró), otra.
	if !hasSnap(src.Datasets[0], snap) {
		r.updateTask(t.ID, func(x *ReplTask) { x.Pending = "" })
		return fmt.Errorf("la instantánea %s ya no está: el próximo envío toma otra", snap)
	}
	for i, d := range src.Datasets {
		if !hasSnap(d, snap) {
			continue // carpeta creada después de la instantánea: va en el próximo envío
		}
		frac := 0.05 + 0.9*float64(i)/float64(len(src.Datasets))
		for attempt := 0; attempt < 4; attempt++ {
			cur := findRel(dst, d.Rel)
			step, err := zfs.PlanDataset(d, cur, snap)
			if err != nil {
				return err
			}
			if step.Mode == "skip" {
				break
			}
			label := relName(d.Rel)
			progress(frac, fmt.Sprintf("%s: %s (%s enviados)", label, modeLabel(step.Mode), humanBytes(sent.Load())))
			switch step.Mode {
			case "resume":
				err = r.transfer(ctx, sink, t, d.Rel, agentproto.ZfsReplSendArgs{Token: step.Token}, "", sent)
			case "full":
				first := step.Snap
				if step.First != "" {
					first = step.First
				}
				err = r.transfer(ctx, sink, t, d.Rel, agentproto.ZfsReplSendArgs{Pool: t.Pool, Vol: t.Vol, Rel: d.Rel, Snap: first}, "", sent)
			case "incr":
				err = r.transfer(ctx, sink, t, d.Rel, agentproto.ZfsReplSendArgs{Pool: t.Pool, Vol: t.Vol, Rel: d.Rel, Snap: snap, From: step.From}, snapGUID(d, step.From), sent)
			}
			if err != nil {
				return fmt.Errorf("%s: %s", label, agentMessage(err))
			}
			// Volver a leer: un resume o un full con historia necesitan otro paso.
			if dst, err = sink.State(ctx, t.Vol); err != nil {
				return fmt.Errorf("no se pudo leer el destino: %s", agentMessage(err))
			}
		}
	}
	// El origen guarda solo la última instantánea de la tarea (la base del próximo envío)
	// y después el destino se iguala a lo que quedó en el origen.
	if err := r.agent.ZfsReplPrune(ctx, agentproto.ZfsReplPruneArgs{Pool: t.Pool, Vol: t.Vol, Task: t.ID, Keep: snap}); err != nil {
		return fmt.Errorf("no se pudieron borrar las instantáneas viejas: %s", agentMessage(err))
	}
	progress(0.96, "Igualando las instantáneas del destino")
	if src, err = r.agent.ZfsReplState(ctx, agentproto.ZfsReplStateArgs{Pool: t.Pool, Vol: t.Vol}); err != nil {
		return errors.New(agentMessage(err))
	}
	keep := map[string][]string{}
	for _, d := range src.Datasets {
		for _, s := range d.Snaps {
			keep[d.Rel] = append(keep[d.Rel], s.GUID)
		}
	}
	if err := sink.Mirror(ctx, t.Vol, keep); err != nil {
		return fmt.Errorf("no se pudieron igualar las instantáneas: %s", agentMessage(err))
	}
	progress(1, fmt.Sprintf("Listo: %s enviados", humanBytes(sent.Load())))
	return nil
}

// transfer manda un stream del agente al destino.
func (r *zfsRepl) transfer(ctx context.Context, sink replSink, t ReplTask, rel string, a agentproto.ZfsReplSendArgs, baseGUID string, sent *atomic.Uint64) error {
	f, res, err := r.agent.ZfsReplSend(ctx, a)
	if err != nil {
		return err
	}
	rerr := sink.Recv(ctx, t.Vol, rel, baseGUID, countReader{r: f, n: sent})
	f.Close() // si el destino falló, zfs send termina con EPIPE
	serr := r.agent.ZfsReplWait(context.WithoutCancel(ctx), res.ID)
	if rerr != nil {
		return rerr
	}
	return serr
}

func hasSnap(d agentproto.ReplDataset, name string) bool { return snapGUID(d, name) != "" }

func snapGUID(d agentproto.ReplDataset, name string) string {
	for _, s := range d.Snaps {
		if s.Name == name {
			return s.GUID
		}
	}
	return ""
}

func findRel(st *agentproto.ZfsReplState, rel string) *agentproto.ReplDataset {
	if st == nil {
		return nil
	}
	for i := range st.Datasets {
		if st.Datasets[i].Rel == rel {
			return &st.Datasets[i]
		}
	}
	return nil
}

func relName(rel string) string {
	switch {
	case rel == "":
		return "Volumen"
	case rel == "shares":
		return "Carpetas"
	case rel == "apps":
		return "Apps"
	case strings.HasPrefix(rel, "shares/"):
		return "Carpeta " + strings.TrimPrefix(rel, "shares/")
	default:
		return "App " + strings.TrimPrefix(rel, "apps/")
	}
}

func modeLabel(m string) string {
	return map[string]string{"resume": "retomando el envío cortado", "full": "copia completa", "incr": "solo los cambios"}[m]
}

// ---- Programa ----------------------------------------------------------------------------

// tick arranca las tareas que tocan. Un envío fallido se reintenta a los 15 minutos
// (o antes, si la tarea es más frecuente).
func (r *zfsRepl) tick(now time.Time) {
	r.mu.Lock()
	var due []string
	for _, t := range r.st.Tasks {
		if !t.Enabled || t.Every <= 0 || r.running[t.ID] != "" {
			continue
		}
		every := time.Duration(t.Every) * time.Hour
		last := parseTime(t.LastOK)
		if t.Last != nil && !t.Last.OK {
			retry := min(every, 15*time.Minute)
			if now.Sub(parseTime(t.Last.At)) < retry {
				continue
			}
			due = append(due, t.ID)
			continue
		}
		if last.IsZero() || now.Sub(last) >= every {
			due = append(due, t.ID)
		}
	}
	r.mu.Unlock()
	for _, id := range due {
		_, _ = r.start(id, false)
	}
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// ---- Vista -----------------------------------------------------------------------------

// ReplStatus es storage.zfs.repl.status.
type ReplStatus struct {
	Identity ReplIdentity  `json:"identity"`
	Receive  ReplRecvView  `json:"receive"`
	Targets  []ReplTarget  `json:"targets"`
	Sources  []ReplSource  `json:"sources"`
	Tasks    []ReplTaskOut `json:"tasks"`
	Replicas []ReplReplica `json:"replicas"`
}

type ReplIdentity struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
}

type ReplRecvView struct {
	Enabled bool        `json:"enabled"`
	Pool    string      `json:"pool"`
	Port    int         `json:"port"`
	Addrs   []string    `json:"addrs"`
	Invite  *ReplInvite `json:"invite,omitempty"`
}

type ReplInvite struct {
	Code    string `json:"code"`
	Expires string `json:"expires"`
}

type ReplTaskOut struct {
	ReplTask
	VolumeID    string `json:"volumeId,omitempty"`
	TargetLabel string `json:"targetLabel"`
	Job         string `json:"job,omitempty"`
	Next        string `json:"next,omitempty"`
}

// ReplReplica es una réplica recibida en este NAS.
type ReplReplica struct {
	Pool      string `json:"pool"`
	Origin    string `json:"origin"`
	From      string `json:"from"` // nombre del NAS de origen ("Este NAS" si es local)
	Vol       string `json:"vol"`
	Used      uint64 `json:"used"`
	Encrypted bool   `json:"encrypted"`
	Partial   bool   `json:"partial"` // hay un envío a medias
	LastSnap  string `json:"lastSnap,omitempty"`
	LastAt    string `json:"lastAt,omitempty"`
	Snapshots int    `json:"snapshots"`
}

func (r *zfsRepl) status(ctx context.Context) ReplStatus {
	id := r.net.identity()
	vols, _ := r.z.s.inv.Volumes(ctx)
	r.mu.Lock()
	out := ReplStatus{Identity: id, Targets: append([]ReplTarget{}, r.st.Targets...), Sources: append([]ReplSource{}, r.st.Sources...), Tasks: []ReplTaskOut{}, Replicas: []ReplReplica{}}
	out.Receive = ReplRecvView{Enabled: r.st.Receive.Enabled, Pool: r.st.Receive.Pool, Port: replPort, Addrs: localAddrs()}
	if inv := r.invite; inv != nil && time.Now().Before(inv.expires) {
		out.Receive.Invite = &ReplInvite{Code: inv.code, Expires: inv.expires.UTC().Format(time.RFC3339)}
	}
	sources := map[string]string{}
	for _, s := range r.st.Sources {
		sources[s.ID] = s.Name
	}
	targets := map[string]string{}
	for _, tg := range r.st.Targets {
		targets[tg.ID] = tg.Name
	}
	for _, t := range r.st.Tasks {
		o := ReplTaskOut{ReplTask: t, Job: r.running[t.ID]}
		if pool, ok := strings.CutPrefix(t.Target, "local:"); ok {
			o.TargetLabel = "Pool " + pool + " (este NAS)"
		} else if n, ok := targets[t.Target]; ok {
			o.TargetLabel = n
		} else {
			o.TargetLabel = "Destino desvinculado"
		}
		for _, v := range vols {
			if v.Kind == "zfs" && v.Name == t.Vol && v.Pool == storage.ZfsPoolID(t.Pool) {
				o.VolumeID = v.ID
			}
		}
		if t.Enabled && t.Every > 0 {
			if last := parseTime(t.LastOK); !last.IsZero() {
				o.Next = last.Add(time.Duration(t.Every) * time.Hour).UTC().Format(time.RFC3339)
			}
		}
		out.Tasks = append(out.Tasks, o)
	}
	r.mu.Unlock()
	for _, p := range r.z.s.inv.ZfsReport(ctx).Pools {
		prefix := p.Name + "/" + zfs.ReplRoot + "/"
		for _, d := range p.Datasets {
			rest, ok := strings.CutPrefix(d.Name, prefix)
			origin, vol, ok2 := strings.Cut(rest, "/")
			if !ok || !ok2 || strings.Contains(vol, "/") || zfs.ValidOrigin(origin) != nil {
				continue
			}
			rp := ReplReplica{Pool: p.Name, Origin: origin, Vol: vol, Used: d.Used, Encrypted: d.Encrypted, From: sources[origin]}
			if origin == zfs.ReplLocal {
				rp.From = "Este NAS"
			} else if rp.From == "" {
				rp.From = "Origen desvinculado (" + origin + ")"
			}
			if st, err := r.agent.ZfsReplState(ctx, agentproto.ZfsReplStateArgs{Pool: p.Name, Origin: origin, Vol: vol}); err == nil && st.Exists {
				for _, ds := range st.Datasets {
					rp.Partial = rp.Partial || ds.Token != ""
				}
				root := st.Datasets[0]
				rp.Snapshots = len(root.Snaps)
				for i := len(root.Snaps) - 1; i >= 0; i-- {
					if zfs.ReplSnapTask(root.Snaps[i].Name) != "" {
						rp.LastSnap = root.Snaps[i].Name
						if ts, err := time.Parse("20060102T150405Z", rp.LastSnap[len(rp.LastSnap)-16:]); err == nil {
							rp.LastAt = ts.UTC().Format(time.RFC3339)
						}
						break
					}
				}
			}
			out.Replicas = append(out.Replicas, rp)
		}
	}
	sort.Slice(out.Replicas, func(i, j int) bool { return out.Replicas[i].Vol < out.Replicas[j].Vol })
	return out
}

// ---- Tareas -----------------------------------------------------------------------------

func validEvery(h int) error {
	switch h {
	case 0, 1, 6, 12, 24, 168:
		return nil
	}
	return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "frecuencia inválida")
}

func (r *zfsRepl) createTask(ctx context.Context, volID, target string, every int) (ReplTask, error) {
	v, pool, err := r.z.volume(ctx, volID)
	if err != nil {
		return ReplTask{}, err
	}
	if err := validEvery(every); err != nil {
		return ReplTask{}, err
	}
	if p, ok := strings.CutPrefix(target, "local:"); ok {
		if p == pool {
			return ReplTask{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "elegí otro pool: una réplica en el mismo pool no protege de que falle")
		}
		if _, ok := r.z.pool(ctx, storage.ZfsPoolID(p)); !ok {
			return ReplTask{}, rtcrpc.Errorf(codePoolNotFound, "no existe el pool %s", p)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !strings.HasPrefix(target, "local:") {
		found := false
		for _, tg := range r.st.Targets {
			found = found || tg.ID == target
		}
		if !found {
			return ReplTask{}, rtcrpc.Errorf("NOT_FOUND", "no existe ese destino")
		}
	}
	for _, t := range r.st.Tasks {
		if t.Pool == pool && t.Vol == v.Name && t.Target == target {
			return ReplTask{}, rtcrpc.Errorf("ALREADY_EXISTS", "ya hay una tarea que replica %s a ese destino", v.Name)
		}
	}
	t := ReplTask{ID: newReplID(), Pool: pool, Vol: v.Name, Target: target, Every: every, Enabled: true}
	r.st.Tasks = append(r.st.Tasks, t)
	r.save()
	return t, nil
}

func (r *zfsRepl) deleteTask(ctx context.Context, id string) error {
	t, ok := r.task(id)
	if !ok {
		return rtcrpc.Errorf("NOT_FOUND", "no existe la tarea")
	}
	r.mu.Lock()
	if r.running[id] != "" {
		r.mu.Unlock()
		return rtcrpc.Errorf(agentproto.CodeBusy, "la tarea está enviando: esperá a que termine")
	}
	keep := r.st.Tasks[:0]
	for _, x := range r.st.Tasks {
		if x.ID != id {
			keep = append(keep, x)
		}
	}
	r.st.Tasks = keep
	r.save()
	r.mu.Unlock()
	// Las instantáneas de la tarea en el origen ya no sirven (la réplica queda en el destino).
	_ = r.agent.ZfsReplPrune(ctx, agentproto.ZfsReplPruneArgs{Pool: t.Pool, Vol: t.Vol, Task: t.ID})
	return nil
}

// ---- Réplicas recibidas ------------------------------------------------------------------

func (r *zfsRepl) promote(ctx context.Context, pool, origin, vol, name, pass string) (bool, error) {
	if err := zfs.ValidVolumeName(name); err != nil {
		return false, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	vols, _ := r.z.s.inv.Volumes(ctx)
	for _, v := range vols {
		if v.Name == name {
			return false, rtcrpc.Errorf("ALREADY_EXISTS", "ya hay un volumen %s", name)
		}
	}
	key := pool + "/" + origin + "/" + vol
	r.mu.Lock()
	busy := r.recvVol[key]
	r.mu.Unlock()
	if busy {
		return false, rtcrpc.Errorf(agentproto.CodeBusy, "la réplica está recibiendo un envío: esperá a que termine")
	}
	locked, err := r.agent.ZfsReplPromote(ctx, agentproto.ZfsReplPromoteArgs{Pool: pool, Origin: origin, Vol: vol, Name: name, Passphrase: pass})
	if err != nil {
		return false, agentErr(err)
	}
	r.z.changed("volumes", "shares", "repl")
	return locked, nil
}

func (r *zfsRepl) destroyReplica(ctx context.Context, pool, origin, vol string) error {
	key := pool + "/" + origin + "/" + vol
	r.mu.Lock()
	busy := r.recvVol[key]
	r.mu.Unlock()
	if busy {
		return rtcrpc.Errorf(agentproto.CodeBusy, "la réplica está recibiendo un envío: esperá a que termine")
	}
	if err := r.agent.ZfsReplDestroy(ctx, agentproto.ZfsReplTargetArgs{Pool: pool, Origin: origin, Vol: vol}); err != nil {
		return agentErr(err)
	}
	r.changed()
	return nil
}
