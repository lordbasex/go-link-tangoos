package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/zfs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// fakeZfs simula lo que importa de ZFS para la replicación: datasets con instantáneas
// (GUID y txg), streams full/incremental (-I con intermedias) y receive -s con token.
type fakeZfs struct {
	mu      sync.Mutex
	ds      map[string]*fakeDS
	txg     uint64
	guid    uint64
	results map[string]chan error
	cutNext int // corta el próximo receive después de aplicar ese número de instantáneas (+1)
	sends   []string
}

type fakeDS struct {
	snaps []agentproto.ReplSnap
	token string // stream pendiente (JSON)
}

type fakeStream struct {
	DS    string                `json:"ds"`
	Kind  string                `json:"kind"` // full | incr
	Snaps []agentproto.ReplSnap `json:"snaps"`
}

func newFakeZfs() *fakeZfs {
	return &fakeZfs{ds: map[string]*fakeDS{}, results: map[string]chan error{}}
}

func (f *fakeZfs) mkdir(names ...string) {
	for _, n := range names {
		f.ds[n] = &fakeDS{}
	}
}

func (f *fakeZfs) snap(ds, name string) {
	f.txg++
	f.guid++
	f.ds[ds].snaps = append(f.ds[ds].snaps, agentproto.ReplSnap{Name: name, GUID: fmt.Sprint(1000 + f.guid), Txg: f.txg})
}

func (f *fakeZfs) under(root string) []string {
	var out []string
	for n := range f.ds {
		if n == root || strings.HasPrefix(n, root+"/") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (f *fakeZfs) ZfsReplState(_ context.Context, a agentproto.ZfsReplStateArgs) (*agentproto.ZfsReplState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	root := zfs.VolumeDataset(a.Pool, a.Vol)
	if a.Origin != "" {
		root = zfs.ReplDataset(a.Pool, a.Origin, a.Vol)
	}
	st := &agentproto.ZfsReplState{Datasets: []agentproto.ReplDataset{}}
	for _, n := range f.under(root) {
		rel, _ := zfs.RelOf(root, n)
		d := f.ds[n]
		tok := ""
		if d.token != "" {
			tok = "1-a-b-" + fmt.Sprintf("%016x", len(d.token))
		}
		st.Datasets = append(st.Datasets, agentproto.ReplDataset{Rel: rel, Snaps: append([]agentproto.ReplSnap{}, d.snaps...), Token: tok})
	}
	st.Exists = len(st.Datasets) > 0
	return st, nil
}

func (f *fakeZfs) ZfsReplSnapshot(_ context.Context, a agentproto.ZfsReplSnapshotArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.under(zfs.VolumeDataset(a.Pool, a.Vol)) {
		f.snap(n, a.Name)
	}
	return nil
}

func (f *fakeZfs) newID() string {
	f.guid++
	id := fmt.Sprintf("%016x", f.guid)
	f.results[id] = make(chan error, 1)
	return id
}

func (f *fakeZfs) ZfsReplSend(_ context.Context, a agentproto.ZfsReplSendArgs) (*os.File, agentproto.ZfsStreamResult, error) {
	f.mu.Lock()
	var st fakeStream
	if a.Token != "" {
		// El token apunta al dataset con el envío pendiente (en el fake hay uno solo).
		for _, d := range f.ds {
			if d.token != "" {
				_ = json.Unmarshal([]byte(d.token), &st)
			}
		}
		f.sends = append(f.sends, "resume")
	} else {
		ds := zfs.Join(zfs.VolumeDataset(a.Pool, a.Vol), a.Rel)
		d := f.ds[ds]
		st.DS = a.Rel
		if a.From == "" {
			st.Kind = "full"
			for _, s := range d.snaps {
				if s.Name == a.Snap {
					st.Snaps = []agentproto.ReplSnap{s}
				}
			}
		} else {
			st.Kind = "incr"
			in := false
			for _, s := range d.snaps {
				if in {
					st.Snaps = append(st.Snaps, s)
				}
				if s.Name == a.From {
					in = true
				}
				if s.Name == a.Snap {
					break
				}
			}
		}
		f.sends = append(f.sends, st.Kind+":"+a.Rel)
	}
	id := f.newID()
	ch := f.results[id]
	f.mu.Unlock()
	r, w, _ := os.Pipe()
	go func() {
		_ = json.NewEncoder(w).Encode(st)
		w.Close()
		ch <- nil
	}()
	return r, agentproto.ZfsStreamResult{ID: id}, nil
}

func (f *fakeZfs) ZfsReplRecv(_ context.Context, a agentproto.ZfsReplRecvArgs) (*os.File, agentproto.ZfsStreamResult, error) {
	f.mu.Lock()
	target := zfs.Join(zfs.ReplDataset(a.Pool, a.Origin, a.Vol), a.Rel)
	if a.BaseGUID != "" { // como zfs.repl.recv: se borran las posteriores a la base
		if d := f.ds[target]; d != nil {
			keep := d.snaps[:0]
			found := false
			for _, s := range d.snaps {
				if !found {
					keep = append(keep, s)
				}
				found = found || s.GUID == a.BaseGUID
			}
			d.snaps = keep
		}
	}
	id := f.newID()
	ch := f.results[id]
	f.mu.Unlock()
	r, w, _ := os.Pipe()
	go func() {
		defer r.Close()
		b, _ := io.ReadAll(r)
		var st fakeStream
		if err := json.Unmarshal(b, &st); err != nil {
			ch <- errors.New("stream roto")
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		d := f.ds[target]
		if d != nil && d.token != "" {
			d.token = ""
		} else if st.Kind == "full" {
			if d != nil {
				ch <- errors.New("destination exists")
				return
			}
			d = &fakeDS{}
			f.ds[target] = d
		} else if d == nil || len(d.snaps) == 0 {
			ch <- errors.New("destination does not exist")
			return
		}
		if f.cutNext > 0 && len(st.Snaps) > 1 {
			n := f.cutNext - 1
			f.cutNext = 0
			d.snaps = append(d.snaps, st.Snaps[:n]...)
			rest := st
			rest.Snaps = st.Snaps[n:]
			tb, _ := json.Marshal(rest)
			d.token = string(tb)
			ch <- errors.New("connection reset")
			return
		}
		d.snaps = append(d.snaps, st.Snaps...)
		ch <- nil
	}()
	return w, agentproto.ZfsStreamResult{ID: id}, nil
}

func (f *fakeZfs) ZfsReplWait(_ context.Context, id string) error {
	f.mu.Lock()
	ch := f.results[id]
	f.mu.Unlock()
	if ch == nil {
		return errors.New("no existe el stream")
	}
	return <-ch
}

func (f *fakeZfs) ZfsReplPrune(_ context.Context, a agentproto.ZfsReplPruneArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.under(zfs.VolumeDataset(a.Pool, a.Vol)) {
		d := f.ds[n]
		keep := d.snaps[:0]
		for _, s := range d.snaps {
			if zfs.ReplSnapTask(s.Name) != a.Task || s.Name == a.Keep {
				keep = append(keep, s)
			}
		}
		d.snaps = keep
	}
	return nil
}

func (f *fakeZfs) ZfsReplMirror(_ context.Context, a agentproto.ZfsReplMirrorArgs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	root := zfs.ReplDataset(a.Pool, a.Origin, a.Vol)
	for _, n := range f.under(root) {
		rel, _ := zfs.RelOf(root, n)
		keep, ok := a.Keep[rel]
		if !ok {
			continue
		}
		set := map[string]bool{}
		for _, g := range keep {
			set[g] = true
		}
		d := f.ds[n]
		out := d.snaps[:0]
		for _, s := range d.snaps {
			if set[s.GUID] {
				out = append(out, s)
			}
		}
		d.snaps = out
	}
	return nil
}

func (f *fakeZfs) ZfsReplAbort(context.Context, agentproto.ZfsReplTargetArgs) error   { return nil }
func (f *fakeZfs) ZfsReplDestroy(context.Context, agentproto.ZfsReplTargetArgs) error { return nil }
func (f *fakeZfs) ZfsReplPromote(context.Context, agentproto.ZfsReplPromoteArgs) (bool, error) {
	return false, nil
}

func (f *fakeZfs) names(ds string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	if d := f.ds[ds]; d != nil {
		for _, s := range d.snaps {
			out = append(out, s.Name)
		}
	}
	return out
}

func testRepl(ag ZfsReplAgent) *zfsRepl {
	return &zfsRepl{agent: ag, running: map[string]string{}, probes: map[string]replProbe{}, recvVol: map[string]bool{},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)), changed: func() {}, poolOK: func(context.Context, string) bool { return true }}
}

func TestReplRunLocal(t *testing.T) {
	f := newFakeZfs()
	f.mkdir("tank/volume1", "tank/volume1/shares", "tank/volume1/shares/Fotos", "tank/volume1/apps")
	f.snap("tank/volume1/shares/Fotos", "2026-10-15T02-00-04Z.hourly")
	f.snap("tank/volume1/shares/Fotos", "2026-10-15T03-00-04Z.hourly")
	r := testRepl(f)
	task := ReplTask{ID: "abc123", Pool: "tank", Vol: "volume1", Target: "local:backup", Every: 1, Enabled: true}
	r.st.Tasks = []ReplTask{task}
	sink := localSink{r: r, pool: "backup"}
	var sent atomic.Uint64
	ctx := context.Background()
	if err := r.run(ctx, func(float64, string) {}, task, sink, &sent); err != nil {
		t.Fatal(err)
	}
	dst := "backup/tgrepl/local/volume1/shares/Fotos"
	got := f.names(dst)
	if len(got) != 3 || got[0] != "2026-10-15T02-00-04Z.hourly" || zfs.ReplSnapTask(got[2]) != "abc123" {
		t.Fatalf("primer envío: la réplica tiene %v (falta la historia)", got)
	}
	if sent.Load() == 0 {
		t.Fatal("no contó bytes")
	}
	// Segundo envío: solo los cambios; la instantánea vieja de la tarea se borra en los dos lados.
	time.Sleep(1100 * time.Millisecond) // nombres con segundos
	f.mu.Lock()
	f.snap("tank/volume1/shares/Fotos", "2026-10-15T04-00-04Z.hourly")
	f.sends = nil
	f.mu.Unlock()
	task.Pending = ""
	if err := r.run(ctx, func(float64, string) {}, task, sink, &sent); err != nil {
		t.Fatal(err)
	}
	for _, s := range f.sends {
		if strings.HasPrefix(s, "full") {
			t.Fatalf("el segundo envío mandó copias completas: %v", f.sends)
		}
	}
	got = f.names(dst)
	src := f.names("tank/volume1/shares/Fotos")
	if strings.Join(got, ",") != strings.Join(src, ",") || len(src) != 4 {
		t.Fatalf("origen %v, réplica %v", src, got)
	}
	// Una carpeta nueva llega entera.
	f.mu.Lock()
	f.mkdir("tank/volume1/shares/Nueva")
	f.mu.Unlock()
	time.Sleep(1100 * time.Millisecond)
	if err := r.run(ctx, func(float64, string) {}, task, sink, &sent); err != nil {
		t.Fatal(err)
	}
	if len(f.names("backup/tgrepl/local/volume1/shares/Nueva")) != 1 {
		t.Fatal("no llegó la carpeta nueva")
	}
}

func TestReplRunResume(t *testing.T) {
	f := newFakeZfs()
	f.mkdir("tank/volume1", "tank/volume1/shares", "tank/volume1/shares/A")
	for i := 0; i < 5; i++ {
		f.snap("tank/volume1/shares/A", fmt.Sprintf("2026-10-15T0%d-00-04Z.hourly", i))
	}
	r := testRepl(f)
	task := ReplTask{ID: "abc123", Pool: "tank", Vol: "volume1", Target: "local:backup"}
	r.st.Tasks = []ReplTask{task}
	sink := localSink{r: r, pool: "backup"}
	var sent atomic.Uint64
	ctx := context.Background()
	// El incremental con la historia se corta después de 2 instantáneas.
	f.cutNext = 3
	err := r.run(ctx, func(float64, string) {}, task, sink, &sent)
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("esperaba el corte: %v", err)
	}
	dst := "backup/tgrepl/local/volume1/shares/A"
	if f.ds[dst].token == "" {
		t.Fatal("no quedó el token")
	}
	// El próximo envío retoma (no vuelve a mandar copias completas de A).
	task.Pending = r.st.Tasks[0].Pending
	f.mu.Lock()
	f.sends = nil
	f.mu.Unlock()
	if err := r.run(ctx, func(float64, string) {}, task, sink, &sent); err != nil {
		t.Fatal(err)
	}
	if f.sends[0] != "resume" {
		t.Fatalf("no retomó: %v", f.sends)
	}
	if got, src := f.names(dst), f.names("tank/volume1/shares/A"); strings.Join(got, ",") != strings.Join(src, ",") {
		t.Fatalf("origen %v, réplica %v", src, got)
	}
}

func TestReplPairAndRemote(t *testing.T) {
	old := replPort
	replPort = 24990 + int(time.Now().UnixNano()%9)
	defer func() { replPort = old }()
	fsrc, fdst := newFakeZfs(), newFakeZfs()
	fsrc.mkdir("tank/volume1", "tank/volume1/shares", "tank/volume1/shares/Docs")
	src, dst := testRepl(fsrc), testRepl(fdst)
	src.dir, dst.dir = t.TempDir(), t.TempDir()
	src.net, dst.net = newReplNet(src), newReplNet(dst)
	dst.st.Receive = ReplReceive{Enabled: true, Pool: "backup"}
	dst.net.sync()
	defer func() {
		dst.st.Receive.Enabled = false
		dst.net.sync()
	}()
	ctx := context.Background()
	addr := fmt.Sprintf("127.0.0.1:%d", replPort)
	var probe string
	var fp string
	for i := 0; i < 50; i++ {
		id, p, err := src.net.probe(ctx, addr)
		if err == nil {
			probe, fp = id, p.fp
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if probe == "" || fp != dst.net.identity().Fingerprint {
		t.Fatalf("sondeo: %q huella %q (quiero %q)", probe, fp, dst.net.identity().Fingerprint)
	}
	// Sin invitación abierta, no; con el código equivocado, no.
	if _, err := src.net.addTarget(ctx, probe, "AAAAA-BBBBB"); err == nil {
		t.Fatal("vinculó sin invitación")
	}
	dst.invite = &replInvite{code: "QWERT-YUPAS", expires: time.Now().Add(time.Minute)}
	probe, _, _ = src.net.probe(ctx, addr)
	if _, err := src.net.addTarget(ctx, probe, "QWERT-YUPAX"); err == nil {
		t.Fatal("vinculó con el código equivocado")
	}
	probe, _, _ = src.net.probe(ctx, addr)
	tg, err := src.net.addTarget(ctx, probe, "qwert yupas")
	if err != nil {
		t.Fatal(err)
	}
	if len(dst.st.Sources) != 1 || dst.st.Sources[0].SPKI != src.selfSPKI() || tg.SPKI != dst.selfSPKI() || dst.invite != nil {
		t.Fatalf("vinculación: %+v %+v", dst.st.Sources, tg)
	}
	// Un envío completo por la red.
	task := ReplTask{ID: "abc123", Pool: "tank", Vol: "volume1", Target: tg.ID}
	src.st.Targets, src.st.Tasks = []ReplTarget{tg}, []ReplTask{task}
	sink, err := src.sink(task)
	if err != nil {
		t.Fatal(err)
	}
	var sent atomic.Uint64
	if err := src.run(ctx, func(float64, string) {}, task, sink, &sent); err != nil {
		t.Fatal(err)
	}
	origin := dst.st.Sources[0].ID
	if got := fdst.names("backup/tgrepl/" + origin + "/volume1/shares/Docs"); len(got) != 1 || zfs.ReplSnapTask(got[0]) != "abc123" {
		t.Fatalf("la réplica remota tiene %v", got)
	}
	// Un NAS no vinculado no entra.
	other := testRepl(newFakeZfs())
	other.dir = t.TempDir()
	other.net = newReplNet(other)
	if _, err := other.net.client(tg).State(ctx, "volume1"); err == nil || !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "vinculado") {
		t.Fatalf("un extraño leyó el estado: %v", err)
	}
	// Si el destino cambia de clave, el origen no le manda nada.
	tg2 := tg
	tg2.SPKI = strings.Repeat("0", 64)
	if _, err := src.net.client(tg2).State(ctx, "volume1"); err == nil || !strings.Contains(err.Error(), "clave del destino cambió") {
		t.Fatalf("aceptó otra clave: %v", err)
	}
}
