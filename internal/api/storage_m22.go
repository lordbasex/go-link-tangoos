package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/monitor"
	"github.com/lordbasex/go-link-tangoos/internal/snapsched"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// ---- Fase M2.2: mantenimiento de volúmenes (protocol/README.md §11) ----------------

// M22Agent son las operaciones del agente de esta fase (agentclient.Client las tiene).
type M22Agent interface {
	MDReplace(ctx context.Context, a agentproto.MDReplaceArgs) error
	MDGrow(ctx context.Context, a agentproto.MDGrowArgs) error
	SnapshotDelete(ctx context.Context, mount, share, name string) error
	DiskLocate(ctx context.Context, devID string, on bool) error
}

// Tipos de plan de esta fase (el tipo entra en el HMAC: un token no sirve para otro).
const (
	planReplace = "replace"
	planExpand  = "expand"
)

// Códigos de error de esta fase.
const (
	codeNotSupported = "NOT_SUPPORTED"
	codeTooSmall     = "DEVICE_TOO_SMALL"
	codeVolumeBusy   = "VOLUME_BUSY"
)

// rebuildRate es la velocidad supuesta de una reconstrucción para estimar su duración.
const rebuildRate = 120e6 // bytes/s

// ReplacePlan responde storage.disks.replacePlan: un VolumePlan con summary.rebuild.
type ReplacePlan struct {
	PlanID       string         `json:"plan_id"`
	Expires      string         `json:"expires"`
	ConfirmToken string         `json:"confirm_token"`
	Summary      ReplaceSummary `json:"summary"`
}

type ReplaceSummary struct {
	PlanSummary
	Rebuild RebuildInfo `json:"rebuild"`
}

type RebuildInfo struct {
	From         *string `json:"from"`
	To           string  `json:"to"`
	EstimatedSec int64   `json:"estimatedSec"`
}

type m22 struct {
	s      *storageAPI
	agent  M22Agent
	sched  *snapsched.Store
	alerts *monitor.Store
	unit   time.Duration // "hora" del programador (en pruebas, un minuto)
	log    *slog.Logger

	mu       sync.Mutex
	schedErr map[string]bool // carpeta con error ya avisado (no repetir cada pasada)
}

// registerM22 agrega los métodos de la fase M2.2. Se llama después de registerStorage.
func (a *API) registerM22(r *rtcrpc.Router, d Deps) {
	ag, ok := d.Agent.(M22Agent)
	if !ok || a.storage == nil {
		return
	}
	unit := time.Hour
	if os.Getenv("TANGOD_SNAPSCHED_UNIT") == "minute" { // solo para pruebas (protocol/README.md §11)
		unit = time.Minute
	}
	m := &m22{s: a.storage, agent: ag, unit: unit, log: d.Log, schedErr: map[string]bool{},
		sched:  snapsched.Open(filepath.Join(d.StateDir, "snapshot-schedules.json")),
		alerts: monitor.OpenStore(filepath.Join(d.StateDir, "alerts.json"))}
	if m.log == nil {
		m.log = slog.Default()
	}
	a.m22 = m

	type replacePlanP struct {
		Volume      string  `json:"volume"`
		Failed      *string `json:"failed"`
		Replacement string  `json:"replacement"`
	}
	type expandPlanP struct {
		Volume string   `json:"volume"`
		Add    []string `json:"add"`
	}
	type confirmP struct {
		PlanID       string `json:"plan_id"`
		ConfirmToken string `json:"confirm_token"`
		TypedName    string `json:"typed_name"`
	}
	type diskP struct {
		Disk string `json:"disk"`
	}
	type shareP struct {
		Share string `json:"share"`
	}
	type scheduleP struct {
		Share    string             `json:"share"`
		Schedule snapsched.Schedule `json:"schedule"`
	}
	type idP struct {
		ID string `json:"id"`
	}
	type jobR struct {
		Job string `json:"job"`
	}

	admin := func(sess *rtcrpc.Session) error { return a.requireAdmin(sess) }

	r.Handle("storage.disks.replacePlan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p replacePlanP) (ReplacePlan, error) {
		if err := admin(sess); err != nil {
			return ReplacePlan{}, err
		}
		failed := ""
		if p.Failed != nil {
			failed = *p.Failed
		}
		return m.replacePlan(ctx, p.Volume, failed, p.Replacement)
	}))
	r.Handle("storage.disks.replace", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := m.replace(ctx, p.PlanID, p.ConfirmToken, p.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.volumes.expandPlan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p expandPlanP) (VolumePlan, error) {
		if err := admin(sess); err != nil {
			return VolumePlan{}, err
		}
		return m.expandPlan(ctx, p.Volume, p.Add)
	}))
	r.Handle("storage.volumes.expand", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := m.expand(ctx, p.PlanID, p.ConfirmToken, p.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.disks.locate", rtcrpc.Typed(func(ctx context.Context, _ *rtcrpc.Session, p diskP) (struct{}, error) {
		return struct{}{}, m.locate(ctx, p.Disk)
	}))
	r.Handle("snapshots.schedule.get", rtcrpc.Typed(func(ctx context.Context, _ *rtcrpc.Session, p shareP) (snapsched.Schedule, error) {
		if _, _, ok := m.s.shareByID(ctx, p.Share); !ok {
			return snapsched.Schedule{}, rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida")
		}
		return m.sched.Get(p.Share), nil
	}))
	r.Handle("snapshots.schedule.set", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p scheduleP) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		if _, _, ok := m.s.shareByID(ctx, p.Share); !ok {
			return struct{}{}, rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida")
		}
		if err := m.sched.Set(p.Share, p.Schedule); err != nil {
			return struct{}{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
		a.d.Hub.Publish("snapshots.changed", map[string]string{"share": p.Share})
		return struct{}{}, nil
	}))
	r.Handle("snapshots.delete", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p idP) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, m.deleteSnapshot(ctx, p.ID)
	}))
	r.Handle("alerts.list", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		return nonNil(m.alerts.List()), nil
	})
}

// ---- Reemplazar un disco -------------------------------------------------------------

// members devuelve los discos de un volumen (miembros y fallados) del inventario.
func (m *m22) members(ctx context.Context, volID string) ([]storage.Disk, map[string]storage.Disk, error) {
	disks, err := m.s.inv.Disks(ctx)
	if err != nil {
		return nil, nil, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
	}
	byID := map[string]storage.Disk{}
	var mem []storage.Disk
	for _, d := range disks {
		byID[d.ID] = d
		if d.Volume == volID && (d.Use == "member" || d.Use == "failed") {
			mem = append(mem, d)
		}
	}
	return mem, byID, nil
}

// candidate valida un disco nuevo para un array (reemplazo o ampliación) y lo inspecciona.
func (m *m22) candidate(ctx context.Context, d storage.Disk, ok bool, id string, minSize uint64) (planDisk, error) {
	switch {
	case !ok:
		return planDisk{}, rtcrpc.Errorf("DEVICE_NOT_FOUND", "no existe el disco %s", id)
	case d.Use == "system":
		return planDisk{}, rtcrpc.Errorf(codeNotEligible, "%s es el disco del sistema", label(d))
	case d.Use != "free":
		return planDisk{}, rtcrpc.Errorf(codeNotEligible, "%s ya está en uso", label(d))
	case d.ByID == "":
		return planDisk{}, rtcrpc.Errorf(codeNotEligible, "%s no tiene una identidad estable", label(d))
	case d.Size < minSize:
		return planDisk{}, rtcrpc.Errorf(codeTooSmall, "%s tiene %s y los discos del RAID usan %s: tiene que ser igual o más grande",
			label(d), humanBytes(d.Size), humanBytes(minSize))
	}
	ins, err := m.s.agent.DiskInspect(ctx, []string{d.ByID})
	if err != nil {
		return planDisk{}, agentErr(err)
	}
	if len(ins) != 1 || !ins[0].Eligible {
		reason := "no es elegible"
		if len(ins) == 1 {
			reason = ins[0].Reason
		}
		return planDisk{}, rtcrpc.Errorf(codeNotEligible, "%s: %s", label(d), reason)
	}
	return planDisk{ID: d.ID, ByID: d.ByID, Model: d.Model, Serial: d.Serial, Size: ins[0].Size, Content: ins[0].Content, Fingerprint: ins[0].Fingerprint}, nil
}

func minMemberSize(mem []storage.Disk) uint64 {
	sizes := make([]uint64, 0, len(mem))
	for _, d := range mem {
		sizes = append(sizes, d.Size)
	}
	return diskops.MinSize(sizes)
}

func (m *m22) replacePlan(ctx context.Context, volID, failedID, replID string) (ReplacePlan, error) {
	v, ok := m.s.volumeByID(ctx, volID)
	if !ok {
		return ReplacePlan{}, rtcrpc.Errorf("VOLUME_NOT_FOUND", "no existe el volumen")
	}
	if v.Profile == "basic" {
		return ReplacePlan{}, rtcrpc.Errorf(codeNotSupported, "%s es un volumen Básico: no tiene otra copia de los datos para reconstruir sobre un disco nuevo. Copiá los datos a un volumen con RAID.", v.Name)
	}
	if v.Task == "reshape" || (v.State == "building" && v.Task != "") || v.State == "scrubbing" {
		return ReplacePlan{}, rtcrpc.Errorf(codeVolumeBusy, "%s tiene una tarea en curso (%s): esperá a que termine", v.Name, firstNonEmpty(v.Task, v.State))
	}
	mem, byID, err := m.members(ctx, v.ID)
	if err != nil {
		return ReplacePlan{}, err
	}
	var failed *storage.Disk
	if failedID != "" {
		for i := range mem {
			if mem[i].ID == failedID {
				failed = &mem[i]
			}
		}
		if failed == nil {
			return ReplacePlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "ese disco no es parte de %s", v.Name)
		}
		if failed.ByID == "" {
			return ReplacePlan{}, rtcrpc.Errorf(codeNotEligible, "%s no tiene una identidad estable", label(*failed))
		}
	} else {
		for i := range mem {
			if mem[i].Use == "failed" {
				failed = &mem[i] // el fallado que md todavía lista: se saca en el mismo paso
			}
		}
		if failed == nil && v.State != "degraded" {
			return ReplacePlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%s no está degradado: indicá qué disco reemplazar", v.Name)
		}
	}
	// md usa en cada miembro el tamaño del más chico: el nuevo no puede ser menor.
	minSize := minMemberSize(mem)
	rd, ok := byID[replID]
	pd, err := m.candidate(ctx, rd, ok, replID, minSize)
	if err != nil {
		return ReplacePlan{}, err
	}
	aux := ""
	var from *string
	if failed != nil {
		aux = failed.ByID
		id := failed.ID
		from = &id
	}
	p := m.s.planner.store(&volumePlan{Kind: planReplace, Name: v.Name, Profile: v.Profile, Disks: []planDisk{pd},
		VolumeID: v.ID, Mount: v.Mount, Aux: []string{aux}})
	out := ReplacePlan{PlanID: p.ID, Expires: p.Expires.UTC().Format(time.RFC3339), ConfirmToken: p.Token,
		Summary: ReplaceSummary{
			PlanSummary: PlanSummary{Name: v.Name, Profile: v.Profile, FS: "btrfs", Usable: v.Size,
				Disks: []PlanDiskOut{{ID: pd.ID, Model: pd.Model, Serial: pd.Serial, Size: pd.Size, Content: pd.Content}}},
			Rebuild: RebuildInfo{From: from, To: pd.ID, EstimatedSec: int64(float64(max(minSize, 1)) / rebuildRate)},
		}}
	if pd.Size > minSize && minSize > 0 && diskops.MixedSizes([]uint64{pd.Size, minSize}) {
		out.Summary.Notes = append(out.Summary.Notes, fmt.Sprintf("El disco nuevo es más grande: el RAID usa %s de él.", humanBytes(minSize)))
	}
	if failed != nil {
		out.Summary.Notes = append(out.Summary.Notes, label(*failed)+" sale del RAID y se le borran las firmas.")
	}
	out.Summary.Notes = append(out.Summary.Notes, "Mientras se reconstruye, el volumen sigue disponible pero sin redundancia.")
	return out, nil
}

func (m *m22) replace(ctx context.Context, planID, token, typed string) (string, error) {
	p, code, msg := m.s.planner.checkKind(planReplace, planID, token, typed)
	if p == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	if err := m.recheck(ctx, planID, p.Disks); err != nil {
		return "", err
	}
	m.s.planner.consume(planID)
	failed := ""
	if len(p.Aux) > 0 {
		failed = p.Aux[0]
	}
	repl := p.Disks[0]
	j := m.s.jobs.Start("storage.disk.replace", "Reemplazando un disco de "+p.Name, "Reconstrucción del RAID sobre "+repl.Serial,
		func(ctx context.Context, h *jobs.Handle) error {
			args := agentproto.MDReplaceArgs{OpID: h.ID(), Name: p.Name, Failed: failed,
				Replacement: agentproto.VolumeDisk{DevID: repl.ByID, Fingerprint: repl.Fingerprint}}
			if err := m.agent.MDReplace(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := m.s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			m.s.a.d.Hub.Publish("storage.changed", map[string]string{"what": "volumes"})
			m.s.a.d.Hub.Publish("storage.changed", map[string]string{"what": "disks"})
			if j.State == jobs.Succeeded {
				m.s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.storage", Title: "Disco reemplazado en " + p.Name,
					Body: "La reconstrucción terminó: el RAID está sano."})
			} else {
				m.s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.storage", Title: "No se pudo reemplazar el disco de " + p.Name, Body: j.Error})
			}
		})
	return j.ID, nil
}

// recheck revalida la huella de los discos del plan justo antes de crear el job.
func (m *m22) recheck(ctx context.Context, planID string, disks []planDisk) error {
	ids := make([]string, len(disks))
	for i, d := range disks {
		ids[i] = d.ByID
	}
	ins, err := m.s.agent.DiskInspect(ctx, ids)
	if err != nil {
		return agentErr(err)
	}
	for i, d := range disks {
		if i >= len(ins) || ins[i].Fingerprint != d.Fingerprint || ins[i].Size != d.Size {
			m.s.planner.consume(planID)
			return rtcrpc.Errorf(codeChanged, "el disco %s cambió desde el plan: revisalo de nuevo", d.Serial)
		}
		if !ins[i].Eligible {
			m.s.planner.consume(planID)
			return rtcrpc.Errorf(codeNotEligible, "el disco %s ya no es elegible: %s", d.Serial, ins[i].Reason)
		}
	}
	return nil
}

// ---- Ampliar un volumen ------------------------------------------------------------------

func (m *m22) expandPlan(ctx context.Context, volID string, add []string) (VolumePlan, error) {
	v, ok := m.s.volumeByID(ctx, volID)
	if !ok {
		return VolumePlan{}, rtcrpc.Errorf("VOLUME_NOT_FOUND", "no existe el volumen")
	}
	switch v.Profile {
	case "raid5", "raid6", "raid10":
	case "raid1":
		return VolumePlan{}, rtcrpc.Errorf(codeNotSupported, "Un RAID 1 no gana espacio agregando discos: reemplazá sus dos discos por otros más grandes, de a uno, y después se agranda.")
	default:
		return VolumePlan{}, rtcrpc.Errorf(codeNotSupported, "Un volumen Básico no se amplía agregando discos (sin RAID no hay dónde repartir los datos): creá un volumen nuevo con RAID y copiá los datos.")
	}
	if v.State != "healthy" || v.Mount == "" {
		return VolumePlan{}, rtcrpc.Errorf(codeVolumeBusy, "%s tiene que estar sano, montado y sin tareas en curso para ampliarlo", v.Name)
	}
	if len(add) == 0 || len(add) > 8 {
		return VolumePlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "elegí entre 1 y 8 discos")
	}
	mem, byID, err := m.members(ctx, v.ID)
	if err != nil {
		return VolumePlan{}, err
	}
	n := len(mem) + len(add)
	if err := diskops.CheckProfileDisks(v.Profile, n); err != nil {
		return VolumePlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	minSize := minMemberSize(mem)
	seen := map[string]bool{}
	var pds []planDisk
	for _, id := range add {
		if seen[id] {
			return VolumePlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "disco repetido")
		}
		seen[id] = true
		d, ok := byID[id]
		pd, err := m.candidate(ctx, d, ok, id, minSize)
		if err != nil {
			return VolumePlan{}, err
		}
		pds = append(pds, pd)
	}
	sizes := make([]uint64, n)
	for i := range sizes {
		sizes[i] = minSize
	}
	usable := diskops.UsableBytes(v.Profile, sizes)
	before := make([]uint64, len(mem))
	for i := range before {
		before[i] = minSize
	}
	p := m.s.planner.store(&volumePlan{Kind: planExpand, Name: v.Name, Profile: v.Profile, Disks: pds,
		VolumeID: v.ID, Mount: v.Mount, Aux: []string{strconv.Itoa(n)}})
	out := VolumePlan{PlanID: p.ID, Expires: p.Expires.UTC().Format(time.RFC3339), ConfirmToken: p.Token,
		Summary: PlanSummary{Name: v.Name, Profile: v.Profile, FS: "btrfs", Usable: usable, Notes: []string{
			fmt.Sprintf("%s pasa de %d a %d discos: de %s a %s útiles.", v.Name, len(mem), n, humanBytes(diskops.UsableBytes(v.Profile, before)), humanBytes(usable)),
			"La reorganización (reshape) mueve todos los datos: puede tardar horas. El volumen sigue disponible mientras tanto.",
		}}}
	for _, d := range pds {
		out.Summary.Disks = append(out.Summary.Disks, PlanDiskOut{ID: d.ID, Model: d.Model, Serial: d.Serial, Size: d.Size, Content: d.Content})
		if d.Size > minSize && diskops.MixedSizes([]uint64{d.Size, minSize}) {
			out.Summary.Notes = append(out.Summary.Notes, fmt.Sprintf("%s es más grande: el RAID usa %s de él.", d.Serial, humanBytes(minSize)))
		}
	}
	return out, nil
}

func (m *m22) expand(ctx context.Context, planID, token, typed string) (string, error) {
	p, code, msg := m.s.planner.checkKind(planExpand, planID, token, typed)
	if p == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	if err := m.recheck(ctx, planID, p.Disks); err != nil {
		return "", err
	}
	m.s.planner.consume(planID)
	n, _ := strconv.Atoi(p.Aux[0])
	args := agentproto.MDGrowArgs{Name: p.Name, Mount: p.Mount, RaidDevices: n}
	for _, d := range p.Disks {
		args.Add = append(args.Add, agentproto.VolumeDisk{DevID: d.ByID, Fingerprint: d.Fingerprint})
	}
	j := m.s.jobs.Start("storage.volume.expand", "Ampliando "+p.Name, fmt.Sprintf("%s · %d discos", profileLabel[p.Profile], n),
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			if err := m.agent.MDGrow(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			res, err := m.s.follow(ctx, h)
			if err != nil {
				return err
			}
			var r agentproto.MDGrowResult
			_ = json.Unmarshal(res, &r)
			h.Update(1, "Ahora tiene "+humanBytes(r.FSBytes))
			return nil
		},
		func(j jobs.Job) {
			m.s.a.d.Hub.Publish("storage.changed", map[string]string{"what": "volumes"})
			m.s.a.d.Hub.Publish("storage.changed", map[string]string{"what": "disks"})
			if j.State == jobs.Succeeded {
				body := "El RAID ya usa los discos nuevos."
				if v, ok := m.s.volumeByID(context.Background(), p.VolumeID); ok {
					body = "Ahora tiene " + humanBytes(v.Size) + " en total."
				}
				m.s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.storage", Title: p.Name + " ampliado", Body: body})
			} else {
				m.s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.storage", Title: "No se pudo ampliar " + p.Name, Body: j.Error})
			}
		})
	return j.ID, nil
}

// ---- LED, instantáneas ---------------------------------------------------------------------

func (m *m22) locate(ctx context.Context, diskID string) error {
	disks, err := m.s.inv.Disks(ctx)
	if err != nil {
		return rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
	}
	for _, d := range disks {
		if d.ID != diskID {
			continue
		}
		if d.ByID == "" {
			return rtcrpc.Errorf(codeNotSupported, "este disco no tiene una identidad estable")
		}
		if err := m.agent.DiskLocate(ctx, d.ByID, true); err != nil {
			return agentErr(err)
		}
		return nil
	}
	return rtcrpc.Errorf("DEVICE_NOT_FOUND", "no existe el disco")
}

func (m *m22) deleteSnapshot(ctx context.Context, id string) error {
	shareID, name, ok := strings.Cut(id, "@")
	if !ok || diskops.ValidSnapshotName(name) != nil {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "instantánea inválida")
	}
	sh, mount, ok := m.s.shareByID(ctx, shareID)
	if !ok {
		return rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida")
	}
	if err := m.agent.SnapshotDelete(ctx, mount, sh.Name, name); err != nil {
		return agentErr(err)
	}
	m.s.a.d.Hub.Publish("snapshots.changed", map[string]string{"share": sh.ID})
	return nil
}

// snapshotKind deriva Snapshot.kind del nombre: las programadas llevan el nivel.
func snapshotKind(name string) string {
	if _, level := diskops.SplitSnapshotLevel(name); level != "" {
		return "scheduled"
	}
	return "manual"
}

// ---- Tareas de fondo: monitor y programador -------------------------------------------

// RunMaintenance corre el monitor de volúmenes (cada 10 s) y el programador de
// instantáneas hasta que ctx termina. No hace nada si la fase M2.2 no está registrada.
func (a *API) RunMaintenance(ctx context.Context) {
	m := a.m22
	if m == nil {
		return
	}
	monTick := time.NewTicker(10 * time.Second)
	defer monTick.Stop()
	schedEvery := 30 * time.Second
	if m.unit < time.Hour {
		schedEvery = 10 * time.Second
	}
	schedTick := time.NewTicker(schedEvery)
	defer schedTick.Stop()
	m.monitorPass(ctx)
	m.schedulePass(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-monTick.C:
			m.monitorPass(ctx)
		case <-schedTick.C:
			m.schedulePass(ctx)
		}
	}
}

func (m *m22) monitorPass(ctx context.Context) {
	vols, err := m.s.inv.Volumes(ctx)
	if err != nil {
		return
	}
	disks, _ := m.s.inv.Disks(ctx)
	obs := monitor.Observation{}
	names := map[string]string{}
	for _, v := range vols {
		names[v.ID] = v.Name
		obs.Volumes = append(obs.Volumes, monitor.VolumeObs{ID: v.ID, Name: v.Name, Profile: v.Profile, State: v.State,
			Progress: v.Progress, Size: v.Size, Used: v.Used, BtrfsErrors: monitor.BtrfsErrors(m.s.inv.Sys, v.ID)})
	}
	for _, d := range disks {
		if d.Use == "system" && d.Smart == "unknown" {
			continue
		}
		obs.Disks = append(obs.Disks, monitor.DiskObs{ID: d.ID, Label: label(d), Smart: d.Smart})
	}
	next, events := monitor.Evaluate(m.alerts.Active(), obs, time.Now())
	m.alerts.Replace(next)
	if len(events) == 0 {
		return
	}
	for _, e := range events {
		al := e.Alert
		switch e.Kind {
		case "raised":
			m.s.a.Notify(Notice{ID: "alert_" + al.ID + "_" + strconv.FormatInt(time.Now().Unix(), 10), Level: al.Level, App: "tango.storage",
				Title: al.Title, Body: al.Body, Action: &Action{Label: "Ver en Almacenamiento", App: "tango.storage"}})
		case "resolved":
			if _, still := names[al.Target]; !still && al.Source != "smart" {
				continue // el volumen ya no existe: se resuelve en silencio
			}
			title, body := monitor.ResolvedNotice(al, func(id string) string { return firstNonEmpty(names[id], id) })
			m.s.a.Notify(Notice{ID: "alert_" + al.ID + "_ok_" + strconv.FormatInt(time.Now().Unix(), 10), Level: "ok", App: "tango.storage", Title: title, Body: body})
		}
	}
	m.s.a.d.Hub.Publish("alerts.changed", map[string]int{"count": len(events)})
	m.s.a.d.Hub.Publish("storage.changed", map[string]string{"what": "volumes"})
}

func (m *m22) schedulePass(ctx context.Context) {
	now := time.Now()
	for _, sh := range m.s.shares(ctx) {
		sc := m.sched.Get(sh.ID)
		if !sc.Enabled {
			continue
		}
		mount := strings.TrimSuffix(sh.Path, "/@shares/"+sh.Name)
		byLevel := map[string][]string{}
		ents, _ := os.ReadDir(filepath.Join(mount, "@snapshots", sh.Name))
		for _, e := range ents {
			if _, level := diskops.SplitSnapshotLevel(e.Name()); level != "" && diskops.ValidSnapshotName(e.Name()) == nil {
				byLevel[level] = append(byLevel[level], e.Name())
			}
		}
		changed := false
		for _, level := range snapsched.Levels {
			last := m.sched.Last(sh.ID, level)
			for _, n := range byLevel[level] { // la más nueva en disco también cuenta
				if t, ok := diskops.ParseSnapshotName(n); ok && t.After(last) {
					last = t
				}
			}
			if snapsched.Due(sc, level, last, now, m.unit) {
				name := diskops.ScheduledSnapshotName(now, level)
				if err := m.s.agent.SnapshotCreate(ctx, mount, sh.Name, name); err != nil {
					m.schedFailed(sh, err)
					continue
				}
				m.sched.MarkRun(sh.ID, level, now)
				byLevel[level] = append(byLevel[level], name)
				changed = true
				m.clearSchedErr(sh.ID)
			}
			if sc.Keep(level) <= 0 {
				continue // nivel apagado: no crea ni borra (las que ya existen quedan)
			}
			for _, old := range snapsched.Prune(byLevel[level], sc.Keep(level)) {
				if err := m.agent.SnapshotDelete(ctx, mount, sh.Name, old); err != nil {
					m.log.Warn("retención de instantáneas", "share", sh.Name, "snapshot", old, "err", agentMessage(err))
					continue
				}
				changed = true
			}
		}
		if changed {
			m.s.a.d.Hub.Publish("snapshots.changed", map[string]string{"share": sh.ID})
		}
	}
}

func (m *m22) schedFailed(sh Share, err error) {
	m.log.Warn("instantánea programada", "share", sh.Name, "err", agentMessage(err))
	m.mu.Lock()
	already := m.schedErr[sh.ID]
	m.schedErr[sh.ID] = true
	m.mu.Unlock()
	if !already {
		m.s.a.Notify(Notice{ID: "sched_" + sh.ID + "_" + strconv.FormatInt(time.Now().Unix(), 10), Level: "warn", App: "tango.storage",
			Title: "No se pudo crear la instantánea programada de " + sh.Name, Body: agentMessage(err)})
	}
}

func (m *m22) clearSchedErr(share string) {
	m.mu.Lock()
	delete(m.schedErr, share)
	m.mu.Unlock()
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
