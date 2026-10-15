package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/agentclient"
	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// StorageAgent son las operaciones de tango-agent que usa el almacenamiento
// (agentclient.Client las implementa; los tests usan un falso).
type StorageAgent interface {
	DiskInspect(ctx context.Context, devIDs []string) ([]agentproto.DiskInspection, error)
	VolumeCreate(ctx context.Context, a agentproto.VolumeCreateArgs) error
	ScrubStart(ctx context.Context, opID, mount string) error
	JobStatus(ctx context.Context, opID string) (*agentclient.JobStatus, error)
	SubvolCreate(ctx context.Context, mount, path string) error
	SnapshotCreate(ctx context.Context, mount, share, name string) error
	SambaApply(ctx context.Context, name, path, comment string) (bool, error)
	// Fase M2.1.
	VolumeDelete(ctx context.Context, a agentproto.VolumeDeleteArgs) error
	ShareDelete(ctx context.Context, mount, share string) (*agentproto.ShareDeleteResult, error)
	SharePermissions(ctx context.Context, a agentproto.SharePermsArgs) (bool, error)
	UserEnsure(ctx context.Context, name, full string) error
	UserPassword(ctx context.Context, name, full, password string) error
}

// Share es exactamente el Share de TS.
type Share struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Volume      string `json:"volume"`
	Path        string `json:"path"`
	Size        uint64 `json:"size"`
	Description string `json:"description"`
	SMB         bool   `json:"smb"`
	NFS         bool   `json:"nfs"`
	Snapshots   bool   `json:"snapshots"`
	RecycleBin  bool   `json:"recycleBin"`
	Color       string `json:"color"`
	// Time Machine (protocol/README.md §13): destino de respaldos de Mac por SMB.
	TimeMachine         bool   `json:"timeMachine"`
	TimeMachineMaxBytes *int64 `json:"timeMachineMaxBytes"`
}

// Snapshot es exactamente el Snapshot de TS.
type Snapshot struct {
	ID     string `json:"id"`
	Share  string `json:"share"`
	At     string `json:"at"`
	Size   uint64 `json:"size"`
	Kind   string `json:"kind"`
	Locked bool   `json:"locked"`
}

// VolumePlan es exactamente el VolumePlan de TS.
type VolumePlan struct {
	PlanID       string      `json:"plan_id"`
	Expires      string      `json:"expires"`
	ConfirmToken string      `json:"confirm_token"`
	Summary      PlanSummary `json:"summary"`
}

type PlanSummary struct {
	Name    string        `json:"name"`
	Profile string        `json:"profile"`
	FS      string        `json:"fs"`
	Usable  uint64        `json:"usable"`
	Disks   []PlanDiskOut `json:"disks"`
	Notes   []string      `json:"notes,omitempty"`
}

// DeletePlan responde storage.volumes.deletePlan (protocol/README.md §8).
type DeletePlan struct {
	PlanID       string        `json:"plan_id"`
	Expires      string        `json:"expires"`
	ConfirmToken string        `json:"confirm_token"`
	Summary      DeleteSummary `json:"summary"`
}

type DeleteSummary struct {
	Name      string       `json:"name"`
	Shares    []string     `json:"shares"`
	Snapshots int          `json:"snapshots"`
	Used      uint64       `json:"used"`
	Disks     []DeleteDisk `json:"disks"`
}

type DeleteDisk struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Serial string `json:"serial"`
}

// PermEntry es un permiso de carpeta: usuario o grupo con rw | ro | none.
type PermEntry struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Access string `json:"access"`
}

type PlanDiskOut struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Serial  string `json:"serial"`
	Size    uint64 `json:"size"`
	Content string `json:"content"`
}

// meta persiste lo que el filesystem no sabe: descripción y color de cada carpeta,
// si se publica por SMB, y la fecha del último scrub de cada volumen.
type meta struct {
	Shares    map[string]shareMeta   `json:"shares"`     // clave: nombre en minúsculas
	LastScrub map[string]string      `json:"last_scrub"` // clave: punto de montaje
	Perms     map[string][]PermEntry `json:"perms"`      // clave: nombre en minúsculas
	// SambaRev es la versión del formato de las secciones de Samba ya aplicada; si es
	// menor que sambaRev, tangod las regenera al arrancar (users_v10.go).
	SambaRev int `json:"samba_rev,omitempty"`
}

type shareMeta struct {
	Description string `json:"description"`
	Color       string `json:"color"`
	SMB         bool   `json:"smb"`
	TimeMachine bool   `json:"timeMachine,omitempty"`
	TMMaxBytes  int64  `json:"tmMaxBytes,omitempty"`
}

var shareColors = []string{"#F97316", "#EC4899", "#8B5CF6", "#2F7CF6", "#22C55E", "#EAB308", "#0EA5E9", "#8E8E96"}

type storageAPI struct {
	a       *API
	agent   StorageAgent
	jobs    *jobs.Store
	inv     *storage.Inventory
	planner *planner
	poll    time.Duration

	mu   sync.Mutex
	path string
	m    meta
}

func (s *storageAPI) load() {
	s.m = meta{Shares: map[string]shareMeta{}, LastScrub: map[string]string{}, Perms: map[string][]PermEntry{}}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.m)
	}
	if s.m.Shares == nil {
		s.m.Shares = map[string]shareMeta{}
	}
	if s.m.LastScrub == nil {
		s.m.LastScrub = map[string]string{}
	}
	if s.m.Perms == nil {
		s.m.Perms = map[string][]PermEntry{}
	}
}

func (s *storageAPI) save() {
	b, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

// registerStorage agrega los métodos de la fase M2 (protocol/README.md §7).
func (a *API) registerStorage(r *rtcrpc.Router, d Deps) {
	s := &storageAPI{a: a, agent: d.Agent, jobs: d.Jobs, inv: d.Inventory, planner: newPlanner(), poll: 500 * time.Millisecond,
		path: filepath.Join(d.StateDir, "storage.json")}
	s.load()
	a.storage = s
	go s.migrateSamba() // secciones de Samba al formato actual (protocol/README.md §14)

	type planP struct {
		Name    string   `json:"name"`
		Profile string   `json:"profile"`
		Disks   []string `json:"disks"`
	}
	type createP struct {
		PlanID       string `json:"plan_id"`
		ConfirmToken string `json:"confirm_token"`
		TypedName    string `json:"typed_name"`
	}
	type volP struct {
		Volume string `json:"volume"`
	}
	type shareCreateP struct {
		Name                string `json:"name"`
		Volume              string `json:"volume"`
		Description         string `json:"description"`
		SMB                 bool   `json:"smb"`
		TimeMachine         bool   `json:"timeMachine"`
		TimeMachineMaxBytes *int64 `json:"timeMachineMaxBytes"`
	}
	type shareP struct {
		Share string `json:"share"`
	}
	type jobR struct {
		Job string `json:"job"`
	}

	r.Handle("storage.volumes.plan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p planP) (VolumePlan, error) {
		if err := a.requireAdmin(sess); err != nil {
			return VolumePlan{}, err
		}
		return s.plan(ctx, p.Name, p.Profile, p.Disks)
	}))
	r.Handle("storage.volumes.create", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p createP) (jobR, error) {
		if err := a.requireAdmin(sess); err != nil {
			return jobR{}, err
		}
		id, err := s.create(ctx, p.PlanID, p.ConfirmToken, p.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.scrub.start", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p volP) (jobR, error) {
		if err := a.requireAdmin(sess); err != nil {
			return jobR{}, err
		}
		id, err := s.scrub(ctx, p.Volume)
		return jobR{id}, err
	}))
	r.Handle("shares.list", func(ctx context.Context, _ *rtcrpc.Session, _ json.RawMessage) (any, error) {
		return s.shares(ctx), nil
	})
	r.Handle("shares.create", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p shareCreateP) (jobR, error) {
		if err := a.requireAdmin(sess); err != nil {
			return jobR{}, err
		}
		var tmMax int64
		if p.TimeMachineMaxBytes != nil {
			tmMax = *p.TimeMachineMaxBytes
		}
		if err := validTM(p.SMB, p.TimeMachine, tmMax); err != nil {
			return jobR{}, err
		}
		id, err := s.createShare(ctx, p.Name, p.Volume, p.Description, p.SMB, p.TimeMachine, tmMax)
		return jobR{id}, err
	}))
	s.registerShareUpdate(r)
	r.Handle("snapshots.list", func(ctx context.Context, _ *rtcrpc.Session, raw json.RawMessage) (any, error) {
		var p shareP
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "parámetros inválidos")
			}
		}
		return s.snapshots(ctx, p.Share), nil
	})
	r.Handle("snapshots.create", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p shareP) (jobR, error) {
		if err := a.requireAdmin(sess); err != nil {
			return jobR{}, err
		}
		id, err := s.createSnapshot(ctx, p.Share)
		return jobR{id}, err
	}))

	// ---- Fase M2.1 (protocol/README.md §8) ----
	type deleteP struct {
		PlanID       string `json:"plan_id"`
		ConfirmToken string `json:"confirm_token"`
		TypedName    string `json:"typed_name"`
	}
	type shareDeleteP struct {
		Share     string `json:"share"`
		TypedName string `json:"typed_name"`
	}
	type permsP struct {
		Share   string      `json:"share"`
		Entries []PermEntry `json:"entries"`
	}
	type permsR struct {
		Entries []PermEntry `json:"entries"`
	}
	r.Handle("storage.volumes.deletePlan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p volP) (DeletePlan, error) {
		if err := a.requireAdmin(sess); err != nil {
			return DeletePlan{}, err
		}
		return s.deletePlan(ctx, p.Volume)
	}))
	r.Handle("storage.volumes.delete", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p deleteP) (jobR, error) {
		if err := a.requireAdmin(sess); err != nil {
			return jobR{}, err
		}
		id, err := s.deleteVolume(ctx, p.PlanID, p.ConfirmToken, p.TypedName)
		return jobR{id}, err
	}))
	r.Handle("shares.delete", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p shareDeleteP) (jobR, error) {
		if err := a.requireAdmin(sess); err != nil {
			return jobR{}, err
		}
		id, err := s.deleteShare(ctx, p.Share, p.TypedName)
		return jobR{id}, err
	}))
	r.Handle("shares.permissions.get", rtcrpc.Typed(func(ctx context.Context, _ *rtcrpc.Session, p shareP) (permsR, error) {
		e, err := s.permissions(ctx, p.Share)
		return permsR{e}, err
	}))
	r.Handle("shares.permissions.set", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p permsP) (struct{}, error) {
		if err := a.requireAdmin(sess); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, s.setPermissions(ctx, p.Share, p.Entries)
	}))
}

// ---- Volúmenes -----------------------------------------------------------------------

func agentErr(err error) error {
	var pe *agentproto.Error
	switch {
	case errors.As(err, &pe):
		return rtcrpc.Errorf(pe.Code, "%s", pe.Message)
	case errors.Is(err, agentclient.ErrUnavailable):
		return rtcrpc.Errorf("AGENT_UNAVAILABLE", "el agente del sistema no responde")
	default:
		return rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
	}
}

func (s *storageAPI) plan(ctx context.Context, name, profile string, ids []string) (VolumePlan, error) {
	if err := diskops.ValidVolumeName(name); err != nil {
		return VolumePlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	if err := diskops.CheckProfileDisks(profile, len(ids)); err != nil {
		return VolumePlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	vols, _ := s.inv.Volumes(ctx)
	for _, v := range vols {
		if v.Name == name || v.Mount == "/"+name {
			return VolumePlan{}, rtcrpc.Errorf(codeVolumeExists, "ya existe un volumen %s", name)
		}
	}
	disks, err := s.inv.Disks(ctx)
	if err != nil {
		return VolumePlan{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
	}
	byID := map[string]storage.Disk{}
	for _, d := range disks {
		byID[d.ID] = d
	}
	var chosen []storage.Disk
	seen := map[string]bool{}
	for _, id := range ids {
		d, ok := byID[id]
		switch {
		case !ok:
			return VolumePlan{}, rtcrpc.Errorf("DEVICE_NOT_FOUND", "no existe el disco %s", id)
		case seen[id]:
			return VolumePlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "disco repetido")
		case d.Use == "system":
			return VolumePlan{}, rtcrpc.Errorf(codeNotEligible, "%s es el disco del sistema", label(d))
		case d.Use != "free":
			return VolumePlan{}, rtcrpc.Errorf(codeNotEligible, "%s ya está en uso", label(d))
		case d.ByID == "":
			return VolumePlan{}, rtcrpc.Errorf(codeNotEligible, "%s no tiene una identidad estable", label(d))
		}
		seen[id] = true
		chosen = append(chosen, d)
	}
	devIDs := make([]string, len(chosen))
	for i, d := range chosen {
		devIDs[i] = d.ByID
	}
	ins, err := s.agent.DiskInspect(ctx, devIDs)
	if err != nil {
		return VolumePlan{}, agentErr(err)
	}
	if len(ins) != len(chosen) {
		return VolumePlan{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "respuesta incompleta del agente")
	}
	pds := make([]planDisk, len(chosen))
	sizes := make([]uint64, len(chosen))
	for i, d := range chosen {
		in := ins[i]
		if !in.Eligible {
			return VolumePlan{}, rtcrpc.Errorf(codeNotEligible, "%s: %s", label(d), in.Reason)
		}
		pds[i] = planDisk{ID: d.ID, ByID: d.ByID, Model: d.Model, Serial: d.Serial, Size: in.Size, Content: in.Content, Fingerprint: in.Fingerprint}
		sizes[i] = in.Size
	}
	v := s.planner.add(name, profile, pds)
	out := VolumePlan{PlanID: v.ID, Expires: v.Expires.UTC().Format(time.RFC3339), ConfirmToken: v.Token,
		Summary: PlanSummary{Name: name, Profile: profile, FS: "btrfs", Usable: diskops.UsableBytes(profile, sizes)}}
	if profile != "basic" && diskops.MixedSizes(sizes) {
		out.Summary.Notes = append(out.Summary.Notes, fmt.Sprintf("Los discos tienen distinto tamaño: se usa %s de cada uno", humanBytes(diskops.MinSize(sizes))))
	}
	for _, d := range pds {
		out.Summary.Disks = append(out.Summary.Disks, PlanDiskOut{ID: d.ID, Model: d.Model, Serial: d.Serial, Size: d.Size, Content: d.Content})
	}
	return out, nil
}

func label(d storage.Disk) string {
	if d.Bay != nil {
		return fmt.Sprintf("El disco de la bahía %d (%s)", *d.Bay, d.Serial)
	}
	return fmt.Sprintf("El disco %s (%s)", d.Model, d.Serial)
}

var profileLabel = map[string]string{"basic": "Básico", "raid1": "RAID 1", "raid5": "RAID 5", "raid6": "RAID 6", "raid10": "RAID 10"}

// humanBytes formatea en unidades decimales, como la UI (fmtBytes).
func humanBytes(n uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	f, i := float64(n), 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if f < 10 && i > 0 {
		v := strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", f), "0"), ".")
		return strings.Replace(v, ".", ",", 1) + " " + units[i] // es-AR, como fmtBytes de la UI
	}
	return fmt.Sprintf("%.0f %s", f, units[i])
}

func (s *storageAPI) create(ctx context.Context, planID, token, typed string) (string, error) {
	v, code, msg := s.planner.check(planID, token, typed)
	if v == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	// Revalidar ahora: si un disco cambió desde el plan, se avisa antes de crear el job.
	ids := make([]string, len(v.Disks))
	for i, d := range v.Disks {
		ids[i] = d.ByID
	}
	ins, err := s.agent.DiskInspect(ctx, ids)
	if err != nil {
		return "", agentErr(err)
	}
	for i, d := range v.Disks {
		if i >= len(ins) || ins[i].Fingerprint != d.Fingerprint || ins[i].Size != d.Size {
			s.planner.consume(planID)
			return "", rtcrpc.Errorf(codeChanged, "el disco %s cambió desde el plan: revisalo de nuevo", d.Serial)
		}
		if !ins[i].Eligible {
			s.planner.consume(planID)
			return "", rtcrpc.Errorf(codeNotEligible, "el disco %s ya no es elegible: %s", d.Serial, ins[i].Reason)
		}
	}
	s.planner.consume(planID)

	args := agentproto.VolumeCreateArgs{Name: v.Name, Profile: v.Profile}
	for _, d := range v.Disks {
		args.Disks = append(args.Disks, agentproto.VolumeDisk{DevID: d.ByID, Fingerprint: d.Fingerprint})
	}
	j := s.jobs.Start("storage.volume.create", "Creando "+v.Name, fmt.Sprintf("%s · %d discos", profileLabel[v.Profile], len(v.Disks)),
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			if err := s.agent.VolumeCreate(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			s.a.d.Hub.Publish("storage.changed", map[string]string{"what": "volumes"})
			s.a.d.Hub.Publish("storage.changed", map[string]string{"what": "disks"})
			if j.State == jobs.Succeeded {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.storage", Title: v.Name + " está listo",
					Body: fmt.Sprintf("Volumen %s con Btrfs, montado en /%s.", profileLabel[v.Profile], v.Name)})
			} else {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.storage", Title: "No se pudo crear " + v.Name, Body: j.Error})
			}
		})
	return j.ID, nil
}

// follow consulta job.status del agente hasta que termina, copiando progreso y etapa.
func (s *storageAPI) follow(ctx context.Context, h *jobs.Handle) (json.RawMessage, error) {
	misses := 0
	for {
		st, err := s.agent.JobStatus(ctx, h.ID())
		if err != nil {
			misses++
			if misses > 20 { // ~10 s sin agente
				return nil, errors.New("se perdió el contacto con el agente del sistema")
			}
		} else {
			misses = 0
			switch st.State {
			case "succeeded":
				return st.Result, nil
			case "failed", "interrupted":
				if st.Error != nil {
					return nil, errors.New(st.Error.Message)
				}
				return nil, errors.New("la operación falló")
			default:
				h.Update(st.Progress, st.Stage)
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.poll):
		}
	}
}

// errMessage es el mensaje para el usuario de un error (sin el código).
func errMessage(err error) string {
	var re *rtcrpc.Error
	if errors.As(err, &re) {
		return re.Message
	}
	return agentMessage(err)
}

func agentMessage(err error) string {
	var pe *agentproto.Error
	if errors.As(err, &pe) {
		return pe.Message
	}
	if errors.Is(err, agentclient.ErrUnavailable) {
		return "el agente del sistema no responde"
	}
	return err.Error()
}

func (s *storageAPI) volumeByID(ctx context.Context, id string) (storage.Volume, bool) {
	vols, _ := s.inv.Volumes(ctx)
	for _, v := range vols {
		if v.ID == id {
			return v, true
		}
	}
	return storage.Volume{}, false
}

func (s *storageAPI) scrub(ctx context.Context, volID string) (string, error) {
	v, ok := s.volumeByID(ctx, volID)
	if !ok || v.Mount == "" {
		return "", rtcrpc.Errorf("VOLUME_NOT_FOUND", "no existe el volumen o no está montado")
	}
	if v.State != "healthy" {
		return "", rtcrpc.Errorf("VOLUME_BUSY", "el volumen está ocupado (%s)", v.State)
	}
	j := s.jobs.Start("storage.scrub", "Verificando "+v.Name, "Check de md + scrub de Btrfs",
		func(ctx context.Context, h *jobs.Handle) error {
			if err := s.agent.ScrubStart(ctx, h.ID(), v.Mount); err != nil {
				return errors.New(agentMessage(err))
			}
			res, err := s.follow(ctx, h)
			if err != nil {
				return err
			}
			var r agentproto.ScrubResult
			_ = json.Unmarshal(res, &r)
			s.mu.Lock()
			s.m.LastScrub[v.Mount] = time.Now().UTC().Format(time.RFC3339)
			s.save()
			s.mu.Unlock()
			if bad := r.CsumErrors + r.ReadErrors + r.Uncorrectable + r.MDMismatches; bad > 0 {
				return fmt.Errorf("se encontraron %d errores (%d corregidos, %d sin corregir)", bad, r.Corrected, r.Uncorrectable)
			}
			return nil
		},
		func(j jobs.Job) {
			s.a.d.Hub.Publish("storage.changed", map[string]string{"what": "volumes"})
			if j.State == jobs.Succeeded {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.storage", Title: "Verificación de " + v.Name + " terminada",
					Body: "Sin errores. Todos los datos coinciden con sus checksums."})
			} else {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.storage", Title: "La verificación de " + v.Name + " encontró problemas", Body: j.Error})
			}
		})
	return j.ID, nil
}

// decorateVolumes completa lo que el inventario no sabe (último scrub).
func (s *storageAPI) decorateVolumes(vols []storage.Volume) []storage.Volume {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range vols {
		if t := s.m.LastScrub[vols[i].Mount]; t != "" {
			vols[i].LastScrub = t
		}
	}
	return vols
}

// ---- Carpetas compartidas e instantáneas ---------------------------------------------

func (s *storageAPI) shares(ctx context.Context) []Share {
	vols, _ := s.inv.Volumes(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Share{}
	for _, v := range vols {
		if v.Mount == "" || v.FS != "btrfs" {
			continue
		}
		ents, _ := os.ReadDir(filepath.Join(v.Mount, "@shares"))
		for _, e := range ents {
			if !e.IsDir() || diskops.ValidShareName(e.Name()) != nil {
				continue
			}
			m := s.m.Shares[strings.ToLower(e.Name())]
			color := m.Color
			if color == "" {
				color = shareColors[len(out)%len(shareColors)]
			}
			sh := Share{ID: strings.ToLower(e.Name()), Name: e.Name(), Volume: v.ID, Path: filepath.Join(v.Mount, "@shares", e.Name()),
				Description: m.Description, SMB: m.SMB, Snapshots: true, Color: color, TimeMachine: m.TimeMachine && m.SMB}
			if sh.TimeMachine && m.TMMaxBytes > 0 {
				v := m.TMMaxBytes
				sh.TimeMachineMaxBytes = &v
			}
			out = append(out, sh)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (s *storageAPI) shareByID(ctx context.Context, id string) (Share, string, bool) {
	for _, sh := range s.shares(ctx) {
		if sh.ID == id {
			return sh, strings.TrimSuffix(sh.Path, "/@shares/"+sh.Name), true
		}
	}
	return Share{}, "", false
}

func (s *storageAPI) createShare(ctx context.Context, name, volID, description string, smb, timeMachine bool, tmMax int64) (string, error) {
	if err := diskops.ValidShareName(name); err != nil {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	if len(description) > 256 {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la descripción es demasiado larga")
	}
	v, ok := s.volumeByID(ctx, volID)
	if !ok || v.Mount == "" {
		return "", rtcrpc.Errorf("VOLUME_NOT_FOUND", "no existe el volumen o no está montado")
	}
	for _, sh := range s.shares(ctx) {
		if strings.EqualFold(sh.Name, name) {
			return "", rtcrpc.Errorf("SHARE_EXISTS", "ya existe una carpeta compartida %s", sh.Name)
		}
	}
	smbNote := ""
	j := s.jobs.Start("shares.create", "Creando la carpeta "+name, "Subvolumen Btrfs en "+v.Name,
		func(ctx context.Context, h *jobs.Handle) error {
			if err := s.agent.SubvolCreate(ctx, v.Mount, "@shares/"+name); err != nil {
				return errors.New(agentMessage(err))
			}
			s.mu.Lock()
			s.m.Shares[strings.ToLower(name)] = shareMeta{Description: description, SMB: smb, Color: shareColors[len(s.m.Shares)%len(shareColors)], TimeMachine: timeMachine && smb, TMMaxBytes: tmMax}
			s.save()
			s.mu.Unlock()
			// Permisos por defecto (administradores con lectura y escritura) y, si va por
			// SMB, la sección de Samba con acceso por grupos.
			h.Update(0.6, "Aplicando permisos")
			sh := Share{ID: strings.ToLower(name), Name: name, Description: description, SMB: smb, TimeMachine: timeMachine && smb}
			if tmMax > 0 {
				sh.TimeMachineMaxBytes = &tmMax
			}
			if err := s.applyPerms(ctx, sh, v.Mount, defaultPerms()); err != nil {
				var re *rtcrpc.Error
				if errors.As(err, &re) && re.Code == agentproto.CodeNotInstalled {
					smbNote = "Samba no está instalado: la carpeta existe pero todavía no se ve en la red."
				} else {
					smbNote = "La carpeta existe, pero no se pudieron aplicar los permisos: " + errMessage(err)
				}
				s.mu.Lock()
				m := s.m.Shares[strings.ToLower(name)]
				m.SMB = false
				s.m.Shares[strings.ToLower(name)] = m
				s.save()
				s.mu.Unlock()
			}
			return nil
		},
		func(j jobs.Job) {
			s.a.d.Hub.Publish("shares.changed", map[string]string{"id": strings.ToLower(name)})
			switch {
			case j.State != jobs.Succeeded:
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.settings", Title: "No se pudo crear " + name, Body: j.Error})
			case smbNote != "":
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "warn", App: "tango.settings", Title: "Carpeta " + name + " creada", Body: smbNote})
			default:
				body := "Lista en " + v.Name + "."
				if smb {
					body = "Lista en " + v.Name + " y publicada por SMB."
				}
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.settings", Title: "Carpeta " + name + " creada", Body: body})
			}
		})
	return j.ID, nil
}

func (s *storageAPI) snapshots(ctx context.Context, shareID string) []Snapshot {
	out := []Snapshot{}
	for _, sh := range s.shares(ctx) {
		if shareID != "" && sh.ID != shareID {
			continue
		}
		mount := strings.TrimSuffix(sh.Path, "/@shares/"+sh.Name)
		ents, _ := os.ReadDir(filepath.Join(mount, "@snapshots", sh.Name))
		for _, e := range ents {
			t, ok := diskops.ParseSnapshotName(e.Name())
			if !e.IsDir() || !ok {
				continue
			}
			out = append(out, Snapshot{ID: sh.ID + "@" + e.Name(), Share: sh.ID, At: t.Format(time.RFC3339), Kind: snapshotKind(e.Name())})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}

func (s *storageAPI) createSnapshot(ctx context.Context, shareID string) (string, error) {
	sh, mount, ok := s.shareByID(ctx, shareID)
	if !ok {
		return "", rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida")
	}
	j := s.jobs.Start("snapshots.create", "Instantánea de "+sh.Name, "Btrfs, solo lectura",
		func(ctx context.Context, h *jobs.Handle) error {
			if err := s.agent.SnapshotCreate(ctx, mount, sh.Name, diskops.SnapshotName(time.Now())); err != nil {
				return errors.New(agentMessage(err))
			}
			return nil
		},
		func(j jobs.Job) {
			s.a.d.Hub.Publish("snapshots.changed", map[string]string{"share": sh.ID})
			if j.State == jobs.Succeeded {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.storage", Title: "Instantánea creada", Body: "Se guardó el estado actual de " + sh.Name + "."})
			} else {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.storage", Title: "No se pudo crear la instantánea", Body: j.Error})
			}
		})
	return j.ID, nil
}
