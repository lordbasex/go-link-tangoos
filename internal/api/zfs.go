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

	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/internal/zfs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// ---- ZFS (protocol/README.md §18, ADR 0013) -------------------------------------------

// ZfsAgent son las operaciones ZFS del agente (agentclient.Client las tiene).
type ZfsAgent interface {
	ZfsReport(ctx context.Context, importable bool) (*agentproto.ZfsReport, error)
	ZfsEnable(ctx context.Context, opID string) error
	ZfsPoolCreate(ctx context.Context, a agentproto.ZfsPoolCreateArgs) error
	ZfsPoolAdd(ctx context.Context, a agentproto.ZfsPoolAddArgs) error
	ZfsAux(ctx context.Context, a agentproto.ZfsAuxArgs) error
	ZfsReplace(ctx context.Context, a agentproto.ZfsReplaceArgs) error
	ZfsScrub(ctx context.Context, a agentproto.ZfsScrubArgs) error
	ZfsImport(ctx context.Context, a agentproto.ZfsImportArgs) error
	ZfsExport(ctx context.Context, a agentproto.ZfsPoolArgs) error
	ZfsDestroy(ctx context.Context, a agentproto.ZfsPoolArgs) error
	ZfsVolCreate(ctx context.Context, a agentproto.ZfsVolCreateArgs) error
	ZfsVolSet(ctx context.Context, a agentproto.ZfsVolSetArgs) error
	ZfsVolDelete(ctx context.Context, a agentproto.ZfsVolArgs) error
	ZfsUnlock(ctx context.Context, a agentproto.ZfsUnlockArgs) error
	ZfsLock(ctx context.Context, a agentproto.ZfsVolArgs) error
	ZfsPassphrase(ctx context.Context, a agentproto.ZfsPassphraseArgs) error
	ZfsZvolCreate(ctx context.Context, a agentproto.ZfsZvolArgs) error
	ZfsZvolDelete(ctx context.Context, a agentproto.ZfsZvolArgs) error
}

// Tipos de plan ZFS (el tipo entra en el HMAC).
const (
	planZfsCreate  = "zfs.create"
	planZfsAdd     = "zfs.add"
	planZfsAux     = "zfs.aux"
	planZfsReplace = "zfs.replace"
	planZfsDestroy = "zfs.destroy"
	planZfsVolDel  = "zfs.volume.delete"
)

const codeZfsUnavailable = agentproto.CodeZfsUnavailable

// ZfsStatus es storage.zfs.status.
type ZfsStatus struct {
	Enabled   bool              `json:"enabled"`
	Installed bool              `json:"installed"`
	Version   string            `json:"version,omitempty"`
	Enabling  string            `json:"enabling,omitempty"` // job de "Habilitar ZFS" en curso
	Pools     []storage.ZfsPool `json:"pools"`
}

// ZfsPlanSummary es el resumen que muestra el asistente antes de confirmar.
type ZfsPlanSummary struct {
	Name      string        `json:"name"`
	Kind      string        `json:"kind"` // create | add | cache | log | replace
	Layout    string        `json:"layout"`
	Usable    uint64        `json:"usable"`
	Raw       uint64        `json:"raw"`
	Wasted    uint64        `json:"wasted"`
	Tolerance int           `json:"tolerance"`
	Disks     []PlanDiskOut `json:"disks"`
	Notes     []string      `json:"notes"`
}

type ZfsPlan struct {
	PlanID       string         `json:"plan_id"`
	Expires      string         `json:"expires"`
	ConfirmToken string         `json:"confirm_token"`
	Summary      ZfsPlanSummary `json:"summary"`
}

// ZfsScrubSchedule: scrub automático por pool (por defecto, el día 1 de cada mes:
// cada 30 días desde el último).
type ZfsScrubSchedule struct {
	Enabled bool   `json:"enabled"`
	Days    int    `json:"days"`
	Last    string `json:"last,omitempty"`
}

type zfsAPI struct {
	s     *storageAPI
	pools *poolsAPI
	agent ZfsAgent

	mu       sync.Mutex
	enabling string
	sched    map[string]ZfsScrubSchedule // pool → programa
	health   map[string]string           // pool → último estado avisado
	failures map[string][]time.Time
}

func (a *API) registerZFS(r *rtcrpc.Router, d Deps) {
	ag, ok := d.Agent.(ZfsAgent)
	if !ok || a.storage == nil || a.pools == nil {
		return
	}
	z := &zfsAPI{s: a.storage, pools: a.pools, agent: ag, sched: map[string]ZfsScrubSchedule{}, health: map[string]string{},
		failures: map[string][]time.Time{}}
	z.load()
	a.zfs = z
	admin := func(sess *rtcrpc.Session) error { return a.requireAdmin(sess) }

	type confirmP struct {
		PlanID       string `json:"plan_id"`
		ConfirmToken string `json:"confirm_token"`
		TypedName    string `json:"typed_name"`
	}
	type poolP struct {
		Pool string `json:"pool"`
	}
	type jobR struct {
		Job string `json:"job"`
	}

	r.Handle("storage.zfs.status", func(ctx context.Context, _ *rtcrpc.Session, _ json.RawMessage) (any, error) {
		return z.status(ctx), nil
	})
	r.Handle("storage.zfs.importable", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, _ struct{}) ([]storage.ZfsImportable, error) {
		if err := admin(sess); err != nil {
			return nil, err
		}
		return z.s.inv.ZfsImportables(ctx), nil
	}))
	r.Handle("storage.zfs.enable", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, _ struct{}) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := z.enable(ctx)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.plan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Name   string   `json:"name"`
		Layout string   `json:"layout"`
		Disks  []string `json:"disks"`
	}) (ZfsPlan, error) {
		if err := admin(sess); err != nil {
			return ZfsPlan{}, err
		}
		return z.createPlan(ctx, x.Name, x.Layout, x.Disks)
	}))
	r.Handle("storage.zfs.create", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := z.confirmPool(ctx, planZfsCreate, x.PlanID, x.ConfirmToken, x.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.addPlan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Pool  string   `json:"pool"`
		Kind  string   `json:"kind"` // data | cache | log
		Disks []string `json:"disks"`
	}) (ZfsPlan, error) {
		if err := admin(sess); err != nil {
			return ZfsPlan{}, err
		}
		return z.addPlan(ctx, x.Pool, x.Kind, x.Disks)
	}))
	r.Handle("storage.zfs.add", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := z.confirmAdd(ctx, x.PlanID, x.ConfirmToken, x.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.auxRemove", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Pool string `json:"pool"`
		Kind string `json:"kind"`
		Vdev string `json:"vdev"`
	}) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := z.auxRemove(ctx, x.Pool, x.Kind, x.Vdev)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.replacePlan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Pool        string `json:"pool"`
		Old         string `json:"old"`
		Replacement string `json:"replacement"`
	}) (ZfsPlan, error) {
		if err := admin(sess); err != nil {
			return ZfsPlan{}, err
		}
		return z.replacePlan(ctx, x.Pool, x.Old, x.Replacement)
	}))
	r.Handle("storage.zfs.replace", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := z.confirmReplace(ctx, x.PlanID, x.ConfirmToken, x.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.scrub", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Pool string `json:"pool"`
		Stop bool   `json:"stop"`
	}) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := z.scrub(ctx, x.Pool, x.Stop)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.scrubSchedule.get", rtcrpc.Typed(func(ctx context.Context, _ *rtcrpc.Session, x poolP) (ZfsScrubSchedule, error) {
		return z.schedule(x.Pool), nil
	}))
	r.Handle("storage.zfs.scrubSchedule.set", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Pool    string `json:"pool"`
		Enabled bool   `json:"enabled"`
		Days    int    `json:"days"`
	}) (ZfsScrubSchedule, error) {
		if err := admin(sess); err != nil {
			return ZfsScrubSchedule{}, err
		}
		return z.setSchedule(ctx, x.Pool, x.Enabled, x.Days)
	}))
	r.Handle("storage.zfs.import", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Pool     string `json:"pool"`
		ReadOnly bool   `json:"readonly"`
	}) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := z.importPool(ctx, x.Pool, x.ReadOnly)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.export", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x poolP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := z.exportPool(ctx, x.Pool)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.destroyPlan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x poolP) (DeletePlan, error) {
		if err := admin(sess); err != nil {
			return DeletePlan{}, err
		}
		return z.destroyPlan(ctx, x.Pool)
	}))
	r.Handle("storage.zfs.destroy", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := z.destroy(ctx, x.PlanID, x.ConfirmToken, x.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.volumes.create", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Pool        string `json:"pool"`
		Name        string `json:"name"`
		Quota       uint64 `json:"quota"`
		Reservation uint64 `json:"reservation"`
		Encryption  *struct {
			Passphrase string `json:"passphrase"`
		} `json:"encryption"`
	}) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		pass := ""
		if x.Encryption != nil {
			if pass = x.Encryption.Passphrase; pass == "" {
				return jobR{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "falta la frase de cifrado")
			}
		}
		id, err := z.createVolume(ctx, x.Pool, x.Name, x.Quota, x.Reservation, pass)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.volumes.set", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Volume      string `json:"volume"`
		Quota       uint64 `json:"quota"`
		Reservation uint64 `json:"reservation"`
	}) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, z.setVolume(ctx, x.Volume, x.Quota, x.Reservation)
	}))
	r.Handle("storage.zfs.volumes.deletePlan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Volume string `json:"volume"`
	}) (DeletePlan, error) {
		if err := admin(sess); err != nil {
			return DeletePlan{}, err
		}
		return z.volumeDeletePlan(ctx, x.Volume)
	}))
	r.Handle("storage.zfs.volumes.delete", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := z.volumeDelete(ctx, x.PlanID, x.ConfirmToken, x.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.zfs.zvols.create", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Pool   string `json:"pool"`
		Name   string `json:"name"`
		Size   uint64 `json:"size"`
		Sparse bool   `json:"sparse"`
	}) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, z.zvolCreate(ctx, x.Pool, x.Name, x.Size, x.Sparse)
	}))
	r.Handle("storage.zfs.zvols.delete", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Pool string `json:"pool"`
		Name string `json:"name"`
	}) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, z.zvolDelete(ctx, x.Pool, x.Name)
	}))
	go z.loop(context.Background())
}

// ---- Ayudas -------------------------------------------------------------------------------

func (z *zfsAPI) changed(what ...string) {
	z.s.inv.InvalidateZfs()
	z.s.inv.InvalidatePools()
	for _, w := range append([]string{"zfs"}, what...) {
		z.s.a.d.Hub.Publish("storage.changed", map[string]string{"what": w})
	}
}

func (z *zfsAPI) notify(j jobs.Job, ok, okBody, failTitle string) {
	if j.State == jobs.Succeeded {
		z.s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.storage", Title: ok, Body: okBody})
	} else {
		z.s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.storage", Title: failTitle, Body: j.Error})
	}
}

func (z *zfsAPI) status(ctx context.Context) ZfsStatus {
	rep := z.s.inv.ZfsReport(ctx)
	z.mu.Lock()
	enabling := z.enabling
	z.mu.Unlock()
	return ZfsStatus{Enabled: rep.Available, Installed: rep.Installed, Version: rep.Version, Enabling: enabling, Pools: z.s.inv.ZfsPools(ctx)}
}

func (z *zfsAPI) need(ctx context.Context) error {
	if !z.s.inv.ZfsReport(ctx).Available {
		return rtcrpc.Errorf(codeZfsUnavailable, "ZFS no está habilitado: habilitalo en Almacenamiento → Pools ZFS")
	}
	return nil
}

func (z *zfsAPI) pool(ctx context.Context, id string) (storage.ZfsPool, bool) {
	for _, p := range z.s.inv.ZfsPools(ctx) {
		if p.ID == id || p.Name == id {
			return p, true
		}
	}
	return storage.ZfsPool{}, false
}

// volume busca un volumen ZFS y su pool.
func (z *zfsAPI) volume(ctx context.Context, id string) (storage.Volume, string, error) {
	v, ok := z.s.volumeByID(ctx, id)
	if !ok || v.Kind != "zfs" {
		return storage.Volume{}, "", rtcrpc.Errorf("VOLUME_NOT_FOUND", "no existe el volumen ZFS")
	}
	return v, strings.TrimPrefix(v.Pool, "zfs:"), nil
}

// run arranca un job que sigue una op asíncrona del agente.
func (z *zfsAPI) run(kind, title, detail string, call func(ctx context.Context, opID string) error, done func(j jobs.Job)) string {
	j := z.s.jobs.Start(kind, title, detail, func(ctx context.Context, h *jobs.Handle) error {
		if err := call(ctx, h.ID()); err != nil {
			return errors.New(agentMessage(err))
		}
		_, err := z.s.follow(ctx, h)
		return err
	}, func(j jobs.Job) {
		z.changed("pools", "disks", "volumes")
		done(j)
	})
	return j.ID
}

func vdevDisks(pds []planDisk) []agentproto.VolumeDisk {
	out := make([]agentproto.VolumeDisk, len(pds))
	for i, d := range pds {
		out[i] = agentproto.VolumeDisk{DevID: d.ByID, Fingerprint: d.Fingerprint}
	}
	return out
}

func (z *zfsAPI) toPlan(v *volumePlan, sum ZfsPlanSummary) ZfsPlan {
	if sum.Notes == nil {
		sum.Notes = []string{}
	}
	if sum.Disks == nil {
		sum.Disks = []PlanDiskOut{}
	}
	return ZfsPlan{PlanID: v.ID, Expires: v.Expires.UTC().Format(time.RFC3339), ConfirmToken: v.Token, Summary: sum}
}

// ---- Habilitar -----------------------------------------------------------------------------

func (z *zfsAPI) enable(ctx context.Context) (string, error) {
	if z.s.inv.ZfsReport(ctx).Available {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "ZFS ya está habilitado")
	}
	z.mu.Lock()
	if z.enabling != "" {
		id := z.enabling
		z.mu.Unlock()
		return id, nil
	}
	z.mu.Unlock()
	id := z.run("storage.zfs.enable", "Habilitando ZFS", "Instala OpenZFS y compila el módulo del kernel (DKMS): tarda unos minutos",
		func(ctx context.Context, opID string) error { return z.agent.ZfsEnable(ctx, opID) },
		func(j jobs.Job) {
			z.mu.Lock()
			z.enabling = ""
			z.mu.Unlock()
			z.notify(j, "ZFS está habilitado", "Ya podés crear o importar pools ZFS en Almacenamiento.", "No se pudo habilitar ZFS")
		})
	z.mu.Lock()
	z.enabling = id
	z.mu.Unlock()
	return id, nil
}

// ---- Crear un pool --------------------------------------------------------------------------

func (z *zfsAPI) nameTaken(ctx context.Context, name string) error {
	if _, ok := z.pool(ctx, name); ok {
		return rtcrpc.Errorf(codePoolExists, "ya existe un pool %s", name)
	}
	return z.pools.nameTaken(ctx, name)
}

func (z *zfsAPI) createPlan(ctx context.Context, name, layout string, ids []string) (ZfsPlan, error) {
	if err := z.need(ctx); err != nil {
		return ZfsPlan{}, err
	}
	if err := zfs.ValidPoolName(name); err != nil {
		return ZfsPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	if err := zfs.CheckLayout(layout, len(ids)); err != nil {
		return ZfsPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	if err := z.nameTaken(ctx, name); err != nil {
		return ZfsPlan{}, err
	}
	pds, err := z.pools.pick(ctx, ids, nil)
	if err != nil {
		return ZfsPlan{}, err
	}
	sum := z.vdevSummary(name, "create", layout, pds)
	sum.Notes = append(sum.Notes, "Después creás uno o más volúmenes dentro del pool (con cifrado opcional).")
	v := z.s.planner.store(&volumePlan{Kind: planZfsCreate, Name: name, Profile: layout, Disks: pds})
	return z.toPlan(v, sum), nil
}

func (z *zfsAPI) vdevSummary(name, kind, layout string, pds []planDisk) ZfsPlanSummary {
	sizes := make([]uint64, len(pds))
	var raw uint64
	for i, d := range pds {
		sizes[i] = d.Size
		raw += d.Size
	}
	sum := ZfsPlanSummary{Name: name, Kind: kind, Layout: layout, Usable: zfs.Usable(layout, sizes), Raw: raw,
		Wasted: zfs.Wasted(layout, sizes), Tolerance: zfs.Tolerance(layout, len(pds)), Disks: diskOuts(pds), Notes: []string{}}
	switch sum.Tolerance {
	case 0:
		sum.Notes = append(sum.Notes, "Sin redundancia: si falla un disco se pierde el pool entero.")
	case 1:
		sum.Notes = append(sum.Notes, "Sigue funcionando si falla un disco.")
	default:
		sum.Notes = append(sum.Notes, fmt.Sprintf("Sigue funcionando si fallan hasta %d discos a la vez.", sum.Tolerance))
	}
	if sum.Wasted > 0 {
		sum.Notes = append(sum.Notes, fmt.Sprintf("Los discos tienen distinto tamaño: ZFS usa %s de cada uno y quedan %s sin usar.", humanBytes(sizes[minIdx(sizes)]), humanBytes(sum.Wasted)))
	}
	sum.Notes = append(sum.Notes, "La capacidad es aproximada: ZFS reserva alrededor de un 3 % para sí mismo.")
	return sum
}

func minIdx(s []uint64) int {
	m := 0
	for i := range s {
		if s[i] < s[m] {
			m = i
		}
	}
	return m
}

func (z *zfsAPI) confirmPool(ctx context.Context, kind, planID, token, typed string) (string, error) {
	v, code, msg := z.s.planner.checkKind(kind, planID, token, typed)
	if v == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	if err := z.pools.recheck(ctx, planID, v.Disks); err != nil {
		return "", err
	}
	z.s.planner.consume(planID)
	args := agentproto.ZfsPoolCreateArgs{Name: v.Name, Layout: v.Profile, Disks: vdevDisks(v.Disks)}
	return z.run("storage.zfs.create", "Creando el pool ZFS "+v.Name, fmt.Sprintf("%s · %d discos", zfs.LayoutLabel(v.Profile), len(v.Disks)),
		func(ctx context.Context, opID string) error {
			args.OpID = opID
			return z.agent.ZfsPoolCreate(ctx, args)
		},
		func(j jobs.Job) {
			z.notify(j, "El pool "+v.Name+" está listo", "Ya podés crear volúmenes en él.", "No se pudo crear el pool "+v.Name)
		}), nil
}

// ---- Sumar vdev, caché o registro -------------------------------------------------------------

func (z *zfsAPI) addPlan(ctx context.Context, poolID, kind string, ids []string) (ZfsPlan, error) {
	p, ok := z.pool(ctx, poolID)
	if !ok {
		return ZfsPlan{}, rtcrpc.Errorf(codePoolNotFound, "no existe el pool")
	}
	if p.ReadOnly {
		return ZfsPlan{}, rtcrpc.Errorf(codeNotSupported, "el pool está importado en solo lectura")
	}
	var check func(storage.Disk) error
	layout := p.Layout
	switch kind {
	case "data":
		if layout == "mixed" {
			return ZfsPlan{}, rtcrpc.Errorf(codeNotSupported, "el pool mezcla formas de vdev: sumá discos desde la línea de comandos")
		}
		if err := zfs.CheckLayout(layout, len(ids)); err != nil {
			return ZfsPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "el vdev nuevo tiene que ser %s: %v", zfs.LayoutLabel(layout), err)
		}
	case "cache", "log":
		if len(ids) < 1 || len(ids) > 2 {
			return ZfsPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "elegí uno o dos SSD")
		}
		check = func(d storage.Disk) error {
			if d.Kind == "hdd" {
				return rtcrpc.Errorf("NOT_SSD", "%s no es un SSD", label(d))
			}
			return nil
		}
		layout = map[string]string{"cache": "stripe", "log": map[bool]string{true: "mirror", false: "stripe"}[len(ids) == 2]}[kind]
	default:
		return ZfsPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "tipo inválido")
	}
	pds, err := z.pools.pick(ctx, ids, check)
	if err != nil {
		return ZfsPlan{}, err
	}
	sum := z.vdevSummary(p.Name, kind, layout, pds)
	switch kind {
	case "data":
		sum.Notes = append([]string{fmt.Sprintf("Se suma un vdev %s al pool: la capacidad crece al instante. Un vdev no se puede quitar después.", zfs.LayoutLabel(layout))}, sum.Notes...)
	case "cache":
		sum.Notes = []string{"Caché de lectura (L2ARC): acelera las lecturas repetidas. Si el SSD falla, no se pierde nada."}
	case "log":
		sum.Notes = []string{"Registro de escritura (SLOG): acelera las escrituras sincrónicas (NFS, iSCSI, bases de datos)."}
		if len(ids) == 1 {
			sum.Notes = append(sum.Notes, "Con un solo SSD, si falla justo en un corte de luz se pueden perder los últimos segundos de escrituras sincrónicas: mejor dos en espejo.")
		}
	}
	v := z.s.planner.store(&volumePlan{Kind: planZfsAdd, Name: p.Name, Profile: kind + ":" + layout, Disks: pds})
	return z.toPlan(v, sum), nil
}

func (z *zfsAPI) confirmAdd(ctx context.Context, planID, token, typed string) (string, error) {
	v, code, msg := z.s.planner.checkKind(planZfsAdd, planID, token, typed)
	if v == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	if err := z.pools.recheck(ctx, planID, v.Disks); err != nil {
		return "", err
	}
	z.s.planner.consume(planID)
	kind, layout, _ := strings.Cut(v.Profile, ":")
	if kind == "data" {
		args := agentproto.ZfsPoolAddArgs{Name: v.Name, Layout: layout, Disks: vdevDisks(v.Disks)}
		return z.run("storage.zfs.add", "Ampliando el pool "+v.Name, fmt.Sprintf("Vdev %s · %d discos", zfs.LayoutLabel(layout), len(v.Disks)),
			func(ctx context.Context, opID string) error { args.OpID = opID; return z.agent.ZfsPoolAdd(ctx, args) },
			func(j jobs.Job) {
				z.notify(j, "El pool "+v.Name+" creció", "El vdev nuevo ya suma espacio.", "No se pudo ampliar el pool "+v.Name)
			}), nil
	}
	args := agentproto.ZfsAuxArgs{Name: v.Name, Kind: kind, Disks: vdevDisks(v.Disks)}
	what := map[string]string{"cache": "la caché de lectura", "log": "el registro de escritura"}[kind]
	return z.run("storage.zfs.aux", "Agregando "+what+" a "+v.Name, fmt.Sprintf("%d SSD", len(v.Disks)),
		func(ctx context.Context, opID string) error { args.OpID = opID; return z.agent.ZfsAux(ctx, args) },
		func(j jobs.Job) { z.notify(j, "Listo: "+what+" de "+v.Name, "", "No se pudo agregar "+what) }), nil
}

func (z *zfsAPI) auxRemove(ctx context.Context, poolID, kind, vdev string) (string, error) {
	p, ok := z.pool(ctx, poolID)
	if !ok {
		return "", rtcrpc.Errorf(codePoolNotFound, "no existe el pool")
	}
	found := false
	switch kind {
	case "cache":
		for _, l := range p.Cache {
			found = found || l.Name == vdev
		}
	case "log":
		for _, v := range p.Logs {
			found = found || v.Name == vdev
		}
	}
	if !found {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%s no es un dispositivo de %s de %s", vdev, kind, p.Name)
	}
	args := agentproto.ZfsAuxArgs{Name: p.Name, Kind: kind, Remove: []string{vdev}}
	return z.run("storage.zfs.aux", "Quitando "+vdev+" de "+p.Name, "",
		func(ctx context.Context, opID string) error { args.OpID = opID; return z.agent.ZfsAux(ctx, args) },
		func(j jobs.Job) {
			z.notify(j, "Se quitó el dispositivo de "+p.Name, "", "No se pudo quitar el dispositivo")
		}), nil
}

// ---- Reemplazar un disco --------------------------------------------------------------------

func (z *zfsAPI) replacePlan(ctx context.Context, poolID, old, replacement string) (ZfsPlan, error) {
	p, ok := z.pool(ctx, poolID)
	if !ok {
		return ZfsPlan{}, rtcrpc.Errorf(codePoolNotFound, "no existe el pool")
	}
	var leaf *storage.ZfsLeaf
	var vd storage.ZfsVdev
	for _, v := range p.Vdevs {
		for i := range v.Disks {
			if v.Disks[i].Name == old || v.Disks[i].GUID == old || (v.Disks[i].ID != "" && v.Disks[i].ID == old) {
				leaf, vd = &v.Disks[i], v
			}
		}
	}
	if leaf == nil {
		return ZfsPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "ese disco no es parte del pool")
	}
	if vd.Type == "disk" && leaf.State == "ONLINE" && p.Layout == "stripe" {
		// Reemplazar en caliente un disco sano de un stripe funciona (copia), pero se avisa.
	}
	ref := leaf.Name
	if leaf.ID == "" { // el disco ya no está: se nombra por GUID
		ref = leaf.GUID
	}
	pds, err := z.pools.pick(ctx, []string{replacement}, nil)
	if err != nil {
		return ZfsPlan{}, err
	}
	sum := ZfsPlanSummary{Name: p.Name, Kind: "replace", Layout: vd.Type, Disks: diskOuts(pds),
		Notes: []string{fmt.Sprintf("Se reemplaza %s (%s) y ZFS reconstruye solo los datos usados (resilver).", leaf.Name, strings.ToLower(leaf.State))}}
	if pds[0].Size < vd.Size/uint64(max(1, len(vd.Disks)-zfs.Parity(vd.Type))) && vd.Type != "disk" {
		sum.Notes = append(sum.Notes, "Ojo: el disco nuevo parece más chico que el anterior; ZFS lo va a rechazar si no alcanza.")
	}
	v := z.s.planner.store(&volumePlan{Kind: planZfsReplace, Name: p.Name, Disks: pds, Aux: []string{ref}})
	return z.toPlan(v, sum), nil
}

func (z *zfsAPI) confirmReplace(ctx context.Context, planID, token, typed string) (string, error) {
	v, code, msg := z.s.planner.checkKind(planZfsReplace, planID, token, typed)
	if v == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	if err := z.pools.recheck(ctx, planID, v.Disks); err != nil {
		return "", err
	}
	z.s.planner.consume(planID)
	args := agentproto.ZfsReplaceArgs{Name: v.Name, Old: v.Aux[0], New: vdevDisks(v.Disks)[0]}
	return z.run("storage.zfs.replace", "Reemplazando un disco de "+v.Name, "Resilver: ZFS copia solo los datos usados",
		func(ctx context.Context, opID string) error { args.OpID = opID; return z.agent.ZfsReplace(ctx, args) },
		func(j jobs.Job) {
			z.notify(j, v.Name+" recuperó la redundancia", "El resilver terminó.", "No se pudo reemplazar el disco de "+v.Name)
		}), nil
}

// ---- Scrub --------------------------------------------------------------------------------

func (z *zfsAPI) scrub(ctx context.Context, poolID string, stop bool) (string, error) {
	p, ok := z.pool(ctx, poolID)
	if !ok {
		return "", rtcrpc.Errorf(codePoolNotFound, "no existe el pool")
	}
	if stop {
		if err := z.agent.ZfsScrub(ctx, agentproto.ZfsScrubArgs{Name: p.Name, Stop: true}); err != nil {
			return "", agentErr(err)
		}
		z.changed()
		return "", nil
	}
	if p.Scan != nil && p.Scan.State == "scanning" {
		return "", rtcrpc.Errorf("VOLUME_BUSY", "el pool ya está verificándose o reconstruyéndose")
	}
	if err := z.agent.ZfsScrub(ctx, agentproto.ZfsScrubArgs{Name: p.Name}); err != nil {
		return "", agentErr(err)
	}
	z.changed()
	j := z.s.jobs.Start("storage.zfs.scrub", "Verificando el pool "+p.Name, "Scrub de ZFS: lee todo y compara checksums",
		func(ctx context.Context, h *jobs.Handle) error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(3 * time.Second):
				}
				z.s.inv.InvalidateZfs()
				cur, ok := z.pool(ctx, p.Name)
				if !ok || cur.Scan == nil {
					return errors.New("el pool ya no está")
				}
				if cur.Scan.Function == "scrub" && cur.Scan.State == "scanning" {
					h.Update(cur.Scan.Progress, fmt.Sprintf("%.0f %%", cur.Scan.Progress*100))
					continue
				}
				if cur.Scan.State == "canceled" {
					return errors.New("el scrub se canceló")
				}
				if cur.Scan.Errors > 0 {
					return fmt.Errorf("el scrub encontró %d errores", cur.Scan.Errors)
				}
				z.mu.Lock()
				sc := z.sched[p.Name]
				sc.Last = time.Now().UTC().Format(time.RFC3339)
				z.sched[p.Name] = sc
				z.save()
				z.mu.Unlock()
				return nil
			}
		},
		func(j jobs.Job) {
			z.changed("volumes")
			z.notify(j, "Verificación de "+p.Name+" terminada", "Sin errores: todos los bloques coinciden con sus checksums.", "La verificación de "+p.Name+" encontró problemas")
		})
	return j.ID, nil
}

func (z *zfsAPI) schedule(pool string) ZfsScrubSchedule {
	z.mu.Lock()
	defer z.mu.Unlock()
	sc, ok := z.sched[strings.TrimPrefix(pool, "zfs:")]
	if !ok {
		sc = ZfsScrubSchedule{Enabled: true, Days: 30}
	}
	if sc.Days == 0 {
		sc.Days = 30
	}
	return sc
}

func (z *zfsAPI) setSchedule(ctx context.Context, poolID string, enabled bool, days int) (ZfsScrubSchedule, error) {
	p, ok := z.pool(ctx, poolID)
	if !ok {
		return ZfsScrubSchedule{}, rtcrpc.Errorf(codePoolNotFound, "no existe el pool")
	}
	if days < 1 || days > 365 {
		return ZfsScrubSchedule{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "cada 1 a 365 días")
	}
	z.mu.Lock()
	sc := z.sched[p.Name]
	sc.Enabled, sc.Days = enabled, days
	z.sched[p.Name] = sc
	z.save()
	z.mu.Unlock()
	return sc, nil
}

func (z *zfsAPI) statePath() string { return filepath.Join(z.s.a.d.StateDir, "zfs.json") }

func (z *zfsAPI) load() {
	b, err := os.ReadFile(z.statePath())
	if err != nil {
		return
	}
	var st struct {
		Scrub map[string]ZfsScrubSchedule `json:"scrub"`
	}
	if json.Unmarshal(b, &st) == nil && st.Scrub != nil {
		z.sched = st.Scrub
	}
}

// save: con z.mu tomado.
func (z *zfsAPI) save() {
	b, _ := json.MarshalIndent(map[string]any{"scrub": z.sched}, "", "  ")
	tmp := z.statePath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, z.statePath())
	}
}

// loop: alertas de salud (DEGRADED/FAULTED, errores de checksum) y scrubs programados.
func (z *zfsAPI) loop(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		z.tick(ctx, time.Now())
	}
}

func (z *zfsAPI) tick(ctx context.Context, now time.Time) {
	pools := z.s.inv.ZfsPools(ctx)
	for _, p := range pools {
		var cksum uint64
		for _, v := range p.Vdevs {
			cksum += v.Cksum
			for _, l := range v.Disks {
				cksum += l.Cksum
			}
		}
		state := p.Health
		if cksum > 0 && state == "ONLINE" {
			state = "CKSUM"
		}
		z.mu.Lock()
		prev, seen := z.health[p.Name]
		z.health[p.Name] = state
		z.mu.Unlock()
		if seen && prev != state || !seen && state != "ONLINE" {
			z.healthNotice(p, state, cksum)
		}
		sc := z.schedule(p.Name)
		if !sc.Enabled || p.ReadOnly || p.Health != "ONLINE" || (p.Scan != nil && p.Scan.State == "scanning") {
			continue
		}
		last, _ := time.Parse(time.RFC3339, sc.Last)
		if p.Scan != nil && p.Scan.End != "" {
			if t, err := time.Parse(time.RFC3339, p.Scan.End); err == nil && t.After(last) {
				last = t
			}
		}
		if last.IsZero() {
			// Nunca verificado: se cuenta desde ahora (no se dispara apenas se crea el pool).
			z.mu.Lock()
			sc.Last = now.UTC().Format(time.RFC3339)
			z.sched[p.Name] = sc
			z.save()
			z.mu.Unlock()
			continue
		}
		if now.Sub(last) >= time.Duration(sc.Days)*24*time.Hour {
			_, _ = z.scrub(ctx, p.Name, false)
		}
	}
}

func (z *zfsAPI) healthNotice(p storage.ZfsPool, state string, cksum uint64) {
	id := fmt.Sprintf("zfs-health-%s-%d", p.Name, time.Now().Unix())
	act := &Action{Label: "Abrir Almacenamiento", App: "tango.storage"}
	switch state {
	case "ONLINE":
		z.s.a.Notify(Notice{ID: id, Level: "ok", App: "tango.storage", Title: "El pool " + p.Name + " está sano otra vez", Body: "Todos sus discos responden."})
	case "DEGRADED":
		z.s.a.Notify(Notice{ID: id, Level: "warn", App: "tango.storage", Title: "El pool " + p.Name + " perdió redundancia",
			Body: "Un disco falló o no está. Los datos siguen disponibles: reemplazalo desde Almacenamiento.", Action: act})
	case "CKSUM":
		z.s.a.Notify(Notice{ID: id, Level: "warn", App: "tango.storage", Title: "Errores de checksum en " + p.Name,
			Body: fmt.Sprintf("ZFS detectó %d bloques con checksum incorrecto (los corrigió si había redundancia). Revisá los cables y el estado SMART de los discos.", cksum), Action: act})
	default:
		z.s.a.Notify(Notice{ID: id, Level: "crit", App: "tango.storage", Title: "El pool " + p.Name + " no está disponible (" + state + ")",
			Body: "Faltan más discos de los que soporta. No escribas en él: reconectá los discos y revisá Almacenamiento.", Action: act})
	}
}

// ---- Importar, exportar, destruir --------------------------------------------------------------

func (z *zfsAPI) importPool(ctx context.Context, pool string, readonly bool) (string, error) {
	if err := z.need(ctx); err != nil {
		return "", err
	}
	if zfs.ValidPoolName(pool) != nil && zfs.ValidGUID(pool) != nil {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "pool inválido")
	}
	if !readonly {
		if err := z.pools.nameTaken(ctx, pool); err != nil && zfs.ValidPoolName(pool) == nil {
			return "", err
		}
	}
	mode := map[bool]string{true: "solo lectura, en /import/" + pool, false: "lectura y escritura"}[readonly]
	return z.run("storage.zfs.import", "Importando el pool "+pool, mode,
		func(ctx context.Context, opID string) error {
			return z.agent.ZfsImport(ctx, agentproto.ZfsImportArgs{OpID: opID, Pool: pool, ReadOnly: readonly})
		},
		func(j jobs.Job) {
			body := "Sus volúmenes ya están montados."
			if readonly {
				body = "Está en solo lectura en /import/" + pool + ": revisá los datos y, si querés usarlo, exportalo e importalo en lectura y escritura."
			}
			z.notify(j, "Se importó el pool "+pool, body, "No se pudo importar el pool "+pool)
		}), nil
}

func (z *zfsAPI) exportPool(ctx context.Context, poolID string) (string, error) {
	p, ok := z.pool(ctx, poolID)
	if !ok {
		return "", rtcrpc.Errorf(codePoolNotFound, "no existe el pool")
	}
	if err := z.inUse(ctx, p); err != nil {
		return "", err
	}
	return z.run("storage.zfs.export", "Exportando el pool "+p.Name, "Queda listo para llevarlo a otro equipo",
		func(ctx context.Context, opID string) error {
			return z.agent.ZfsExport(ctx, agentproto.ZfsPoolArgs{OpID: opID, Name: p.Name})
		},
		func(j jobs.Job) {
			z.notify(j, "Se exportó el pool "+p.Name, "Podés desconectar sus discos o importarlo de nuevo.", "No se pudo exportar el pool "+p.Name)
		}), nil
}

// inUse: un pool con apps instaladas en sus volúmenes no se exporta ni destruye.
func (z *zfsAPI) inUse(ctx context.Context, p storage.ZfsPool) error {
	vols, _ := z.s.inv.Volumes(ctx)
	mounts := map[string]bool{}
	for _, v := range vols {
		if v.Pool == p.ID && v.Mount != "" {
			mounts[v.Mount] = true
		}
	}
	for m := range mounts {
		if ents, err := os.ReadDir(filepath.Join(m, "@apps")); err == nil && len(ents) > 0 {
			return rtcrpc.Errorf("VOLUME_BUSY", "%s tiene apps instaladas: desinstalalas primero", m)
		}
	}
	return nil
}

func (z *zfsAPI) destroyPlan(ctx context.Context, poolID string) (DeletePlan, error) {
	p, ok := z.pool(ctx, poolID)
	if !ok {
		return DeletePlan{}, rtcrpc.Errorf(codePoolNotFound, "no existe el pool")
	}
	if err := z.inUse(ctx, p); err != nil {
		return DeletePlan{}, err
	}
	disks, err := z.s.inv.Disks(ctx)
	if err != nil {
		return DeletePlan{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
	}
	byID := map[string]storage.Disk{}
	for _, d := range disks {
		byID[d.ID] = d
	}
	out := DeletePlan{Summary: DeleteSummary{Name: p.Name, Shares: []string{}, Disks: []DeleteDisk{}, Used: p.Allocated}}
	vols := map[string]bool{}
	for _, id := range p.Volumes {
		vols[id] = true
	}
	for _, sh := range z.s.shares(ctx) {
		if vols[sh.Volume] {
			out.Summary.Shares = append(out.Summary.Shares, sh.Name)
		}
	}
	var pds []planDisk
	ids := append([]string{}, p.Disks...)
	for _, l := range p.Cache {
		ids = append(ids, l.ID)
	}
	for _, v := range p.Logs {
		for _, l := range v.Disks {
			ids = append(ids, l.ID)
		}
	}
	for _, id := range ids {
		d, ok := byID[id]
		if !ok {
			continue
		}
		pds = append(pds, planDisk{ID: d.ID, ByID: d.ByID, Model: d.Model, Serial: d.Serial, Size: d.Size})
		out.Summary.Disks = append(out.Summary.Disks, DeleteDisk{ID: d.ID, Model: d.Model, Serial: d.Serial})
	}
	v := z.s.planner.store(&volumePlan{Kind: planZfsDestroy, Name: p.Name, Disks: pds, VolumeID: p.ID})
	out.PlanID, out.ConfirmToken, out.Expires = v.ID, v.Token, v.Expires.UTC().Format(time.RFC3339)
	return out, nil
}

func (z *zfsAPI) destroy(ctx context.Context, planID, token, typed string) (string, error) {
	v, code, msg := z.s.planner.checkKind(planZfsDestroy, planID, token, typed)
	if v == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	z.s.planner.consume(planID)
	ids := map[string]bool{}
	if p, ok := z.pool(ctx, v.VolumeID); ok {
		for _, id := range p.Volumes {
			ids[id] = true
		}
	}
	shares, mounts := z.volumesOf(ctx, ids)
	return z.run("storage.zfs.destroy", "Destruyendo el pool "+v.Name, fmt.Sprintf("%d discos quedan libres", len(v.Disks)),
		func(ctx context.Context, opID string) error {
			z.forgetShares(nil, mounts) // suelta archivos abiertos antes de desmontar
			return z.agent.ZfsDestroy(ctx, agentproto.ZfsPoolArgs{OpID: opID, Name: v.Name})
		},
		func(j jobs.Job) {
			if j.State == jobs.Succeeded {
				z.forgetShares(shares, mounts)
			}
			z.changed("shares")
			z.notify(j, "Se destruyó el pool "+v.Name, "Sus discos quedaron libres.", "No se pudo destruir el pool "+v.Name)
		}), nil
}

// ---- Volúmenes ------------------------------------------------------------------------------

func (z *zfsAPI) createVolume(ctx context.Context, poolID, name string, quota, reservation uint64, pass string) (string, error) {
	p, ok := z.pool(ctx, poolID)
	if !ok {
		return "", rtcrpc.Errorf(codePoolNotFound, "no existe el pool")
	}
	if p.ReadOnly {
		return "", rtcrpc.Errorf(codeNotSupported, "el pool está importado en solo lectura")
	}
	if err := zfs.ValidVolumeName(name); err != nil {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	if err := z.pools.nameTaken(ctx, name); err != nil {
		return "", err
	}
	if pass != "" {
		if err := validPassphrase(pass); err != nil {
			return "", err
		}
	}
	if reservation > 0 && quota > 0 && reservation > quota {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la reserva no puede ser mayor que la cuota")
	}
	detail := "Dataset ZFS en " + p.Name
	if pass != "" {
		detail += " · cifrado (AES-256-GCM)"
	}
	return z.run("storage.zfs.volume.create", "Creando el volumen "+name, detail,
		func(ctx context.Context, opID string) error {
			return z.agent.ZfsVolCreate(ctx, agentproto.ZfsVolCreateArgs{OpID: opID, Pool: p.Name, Name: name, Quota: quota, Reservation: reservation, Passphrase: pass})
		},
		func(j jobs.Job) {
			z.notify(j, "El volumen "+name+" está listo", "Ya podés crear carpetas compartidas en él.", "No se pudo crear el volumen "+name)
		}), nil
}

func validPassphrase(p string) error {
	if len(p) < 8 || len(p) > 512 {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la frase tiene que tener entre 8 y 512 caracteres")
	}
	return nil
}

func (z *zfsAPI) setVolume(ctx context.Context, id string, quota, reservation uint64) error {
	v, pool, err := z.volume(ctx, id)
	if err != nil {
		return err
	}
	if reservation > 0 && quota > 0 && reservation > quota {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la reserva no puede ser mayor que la cuota")
	}
	if quota > 0 && quota < v.Used {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la cuota no puede ser menor que lo que ya ocupa (%s)", humanBytes(v.Used))
	}
	if err := z.agent.ZfsVolSet(ctx, agentproto.ZfsVolSetArgs{Pool: pool, Name: v.Name, Quota: quota, Reservation: reservation}); err != nil {
		return agentErr(err)
	}
	z.changed("volumes")
	return nil
}

func (z *zfsAPI) volumeDeletePlan(ctx context.Context, id string) (DeletePlan, error) {
	v, _, err := z.volume(ctx, id)
	if err != nil {
		return DeletePlan{}, err
	}
	if v.Mount != "" {
		if ents, err := os.ReadDir(filepath.Join(v.Mount, "@apps")); err == nil && len(ents) > 0 {
			return DeletePlan{}, rtcrpc.Errorf("VOLUME_BUSY", "%s tiene apps instaladas: desinstalalas primero", v.Name)
		}
	}
	out := DeletePlan{Summary: DeleteSummary{Name: v.Name, Shares: []string{}, Disks: []DeleteDisk{}, Used: v.Used}}
	for _, sh := range z.s.shares(ctx) {
		if sh.Volume == v.ID {
			out.Summary.Shares = append(out.Summary.Shares, sh.Name)
		}
	}
	shareIDs := map[string]bool{}
	for _, sh := range z.s.shares(ctx) {
		if sh.Volume == v.ID {
			shareIDs[sh.ID] = true
		}
	}
	for _, sn := range z.s.snapshots(ctx, "") {
		if shareIDs[sn.Share] {
			out.Summary.Snapshots++
		}
	}
	pv := z.s.planner.store(&volumePlan{Kind: planZfsVolDel, Name: v.Name, VolumeID: v.ID, Mount: v.Mount})
	out.PlanID, out.ConfirmToken, out.Expires = pv.ID, pv.Token, pv.Expires.UTC().Format(time.RFC3339)
	return out, nil
}

func (z *zfsAPI) volumeDelete(ctx context.Context, planID, token, typed string) (string, error) {
	pv, code, msg := z.s.planner.checkKind(planZfsVolDel, planID, token, typed)
	if pv == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	z.s.planner.consume(planID)
	v, pool, err := z.volume(ctx, pv.VolumeID)
	if err != nil {
		return "", err
	}
	shares, mounts := z.volumesOf(ctx, map[string]bool{v.ID: true})
	return z.run("storage.zfs.volume.delete", "Eliminando el volumen "+v.Name, "Borra el dataset, sus carpetas e instantáneas",
		func(ctx context.Context, opID string) error {
			z.forgetShares(nil, mounts)
			return z.agent.ZfsVolDelete(ctx, agentproto.ZfsVolArgs{OpID: opID, Pool: pool, Name: v.Name})
		},
		func(j jobs.Job) {
			if j.State == jobs.Succeeded {
				z.forgetShares(shares, mounts)
			}
			z.changed("shares")
			z.notify(j, "Se eliminó el volumen "+v.Name, "", "No se pudo eliminar el volumen "+v.Name)
		}), nil
}

// unlock / lock / passphrase: storage.volumes.* los delega acá para volúmenes ZFS.
func (z *zfsAPI) unlock(ctx context.Context, id, pass string) error {
	v, pool, err := z.volume(ctx, id)
	if err != nil {
		return err
	}
	if !v.Locked {
		return nil
	}
	z.mu.Lock()
	recent := z.failures[id][:0]
	for _, t := range z.failures[id] {
		if time.Since(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	z.failures[id] = recent
	blocked := len(recent) >= 5
	z.mu.Unlock()
	if blocked {
		return rtcrpc.Errorf("RATE_LIMITED", "demasiados intentos: esperá unos minutos")
	}
	if err := z.agent.ZfsUnlock(ctx, agentproto.ZfsUnlockArgs{Pool: pool, Name: v.Name, Passphrase: pass}); err != nil {
		var ae *agentproto.Error
		if errors.As(err, &ae) && ae.Code == agentproto.CodeWrongPassphrase {
			z.mu.Lock()
			z.failures[id] = append(z.failures[id], time.Now())
			z.mu.Unlock()
		}
		return agentErr(err)
	}
	z.mu.Lock()
	delete(z.failures, id)
	z.mu.Unlock()
	z.changed("volumes", "shares")
	z.s.a.Notify(Notice{ID: "n_unlock_" + v.Name + "_" + time.Now().Format("150405"), Level: "ok", App: "tango.storage", Title: "Se desbloqueó " + v.Name})
	return nil
}

func (z *zfsAPI) lock(ctx context.Context, id string) error {
	v, pool, err := z.volume(ctx, id)
	if err != nil {
		return err
	}
	if !v.Encrypted {
		return rtcrpc.Errorf(codeNotSupported, "el volumen no está cifrado")
	}
	if v.Mount != "" {
		if ents, err := os.ReadDir(filepath.Join(v.Mount, "@apps")); err == nil && len(ents) > 0 {
			return rtcrpc.Errorf("VOLUME_BUSY", "%s tiene apps instaladas: detenelas primero", v.Name)
		}
	}
	if err := z.agent.ZfsLock(ctx, agentproto.ZfsVolArgs{Pool: pool, Name: v.Name}); err != nil {
		return agentErr(err)
	}
	z.changed("volumes", "shares")
	return nil
}

func (z *zfsAPI) passphrase(ctx context.Context, id, old, nw string) error {
	v, pool, err := z.volume(ctx, id)
	if err != nil {
		return err
	}
	if err := validPassphrase(nw); err != nil {
		return err
	}
	if err := z.agent.ZfsPassphrase(ctx, agentproto.ZfsPassphraseArgs{Pool: pool, Name: v.Name, Old: old, New: nw}); err != nil {
		return agentErr(err)
	}
	return nil
}

// ---- Volúmenes de bloque (zvol) para iSCSI -------------------------------------------------

func (z *zfsAPI) zvolCreate(ctx context.Context, poolID, name string, size uint64, sparse bool) error {
	p, ok := z.pool(ctx, poolID)
	if !ok {
		return rtcrpc.Errorf(codePoolNotFound, "no existe el pool")
	}
	if _, err := zfs.ArgsZvolCreate(p.Name, name, size, sparse); err != nil {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	for _, zv := range p.Zvols {
		if zv.Name == name {
			return rtcrpc.Errorf("ALREADY_EXISTS", "ya existe %s", name)
		}
	}
	if !sparse && size > p.Free {
		return rtcrpc.Errorf(codeNoSpace, "no hay %s libres en %s", humanBytes(size), p.Name)
	}
	if err := z.agent.ZfsZvolCreate(ctx, agentproto.ZfsZvolArgs{Pool: p.Name, Name: name, Size: size, Sparse: sparse}); err != nil {
		return agentErr(err)
	}
	z.changed()
	return nil
}

func (z *zfsAPI) zvolDelete(ctx context.Context, poolID, name string) error {
	p, ok := z.pool(ctx, poolID)
	if !ok {
		return rtcrpc.Errorf(codePoolNotFound, "no existe el pool")
	}
	if z.s.a.m4 != nil && z.s.a.m4.zvolInUse(zfs.ZvolDevice(p.Name, name)) {
		return rtcrpc.Errorf("VOLUME_BUSY", "un destino iSCSI usa este volumen de bloque: borralo primero")
	}
	if err := z.agent.ZfsZvolDelete(ctx, agentproto.ZfsZvolArgs{Pool: p.Name, Name: name}); err != nil {
		return agentErr(err)
	}
	z.changed()
	return nil
}

// ZvolDevices lista los volúmenes de bloque (para elegir el respaldo de un LUN).
func (z *zfsAPI) zvolDevices(ctx context.Context) []string {
	var out []string
	for _, p := range z.s.inv.ZfsPools(ctx) {
		for _, zv := range p.Zvols {
			out = append(out, zv.Device)
		}
	}
	sort.Strings(out)
	return out
}

type zvolInfo struct {
	pool string // "zfs:<pool>"
	size uint64
}

// zvolByDevice busca un volumen de bloque por su /dev/zvol/….
func (a *API) zvolByDevice(ctx context.Context, dev string) (zvolInfo, bool) {
	if a.zfs == nil {
		return zvolInfo{}, false
	}
	for _, p := range a.zfs.s.inv.ZfsPools(ctx) {
		for _, zv := range p.Zvols {
			if zv.Device == dev {
				return zvolInfo{pool: p.ID, size: zv.Size}, true
			}
		}
	}
	return zvolInfo{}, false
}

// zvolInUse: algún destino iSCSI usa ese volumen de bloque.
func (m *m4API) zvolInUse(dev string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.cfg.ISCSI.Targets {
		if t.Zvol == dev {
			return true
		}
	}
	return false
}

// forgetShares olvida la configuración de tangod de carpetas que se borran con su
// volumen o pool (los datos los borra el agente) y suelta los archivos abiertos.
func (z *zfsAPI) forgetShares(names []string, mounts []string) {
	s := z.s
	if s.a.files != nil {
		for _, m := range mounts {
			s.a.files.ForgetUnder(m + "/")
		}
		s.a.files.InvalidateWorkers()
	}
	s.mu.Lock()
	for _, sh := range names {
		delete(s.m.Shares, strings.ToLower(sh))
		delete(s.m.Perms, strings.ToLower(sh))
	}
	for _, m := range mounts {
		delete(s.m.LastScrub, m)
	}
	s.save()
	s.mu.Unlock()
}

// volumesOf devuelve nombres de carpetas y puntos de montaje de los volúmenes dados.
func (z *zfsAPI) volumesOf(ctx context.Context, ids map[string]bool) (shares, mounts []string) {
	vols, _ := z.s.inv.Volumes(ctx)
	for _, v := range vols {
		if ids[v.ID] && v.Mount != "" {
			mounts = append(mounts, v.Mount)
		}
	}
	for _, sh := range z.s.shares(ctx) {
		if ids[sh.Volume] {
			shares = append(shares, sh.Name)
		}
	}
	return shares, mounts
}
