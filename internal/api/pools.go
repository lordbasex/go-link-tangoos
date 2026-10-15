package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// ---- Grupos de almacenamiento (protocol/README.md §16, ADR 0011) --------------------

// PoolsAgent son las operaciones del agente de esta fase (agentclient.Client las tiene).
type PoolsAgent interface {
	PoolCreate(ctx context.Context, a agentproto.PoolCreateArgs) error
	PoolExpand(ctx context.Context, a agentproto.PoolExpandArgs) error
	PoolReplace(ctx context.Context, a agentproto.PoolReplaceArgs) error
	PoolDelete(ctx context.Context, a agentproto.PoolDeleteArgs) error
	LVCreate(ctx context.Context, a agentproto.LVCreateArgs) error
	LVResize(ctx context.Context, a agentproto.LVResizeArgs) error
	LVDelete(ctx context.Context, a agentproto.LVDeleteArgs) error
	LUKSUnlock(ctx context.Context, a agentproto.LUKSUnlockArgs) error
	LUKSLock(ctx context.Context, name string) error
	LUKSPassphrase(ctx context.Context, a agentproto.LUKSPassphraseArgs) error
	LUKSKeyDevice(ctx context.Context, a agentproto.LUKSKeyDeviceArgs) error
	KeyDevices(ctx context.Context) ([]agentproto.KeyDevice, error)
	CacheCreate(ctx context.Context, a agentproto.CacheCreateArgs) error
	CacheRemove(ctx context.Context, a agentproto.CacheRemoveArgs) error
}

// Tipos de plan (el tipo entra en el HMAC: un token no sirve para otro).
const (
	planPoolCreate  = "pool.create"
	planPoolExpand  = "pool.expand"
	planPoolReplace = "pool.replace"
	planPoolDelete  = "pool.delete"
	planCache       = "cache"
)

// Códigos de error de esta fase.
const (
	codePoolExists   = "POOL_EXISTS"
	codePoolNotFound = "POOL_NOT_FOUND"
	codeNoSpace      = "NO_SPACE"
	codeLocked       = "VOLUME_LOCKED"
)

// PoolPlan es un plan sobre un grupo: el VolumePlan de §7 con los tramos.
type PoolPlan struct {
	PlanID       string          `json:"plan_id"`
	Expires      string          `json:"expires"`
	ConfirmToken string          `json:"confirm_token"`
	Summary      PoolPlanSummary `json:"summary"`
}

type PoolPlanSummary struct {
	Name    string             `json:"name"`
	Profile string             `json:"profile"`
	FS      string             `json:"fs"`
	Usable  uint64             `json:"usable"`
	Disks   []PlanDiskOut      `json:"disks"`
	Notes   []string           `json:"notes,omitempty"`
	Tiers   []storage.PoolTier `json:"tiers"`
	Unused  uint64             `json:"unused"`
	Rebuild *RebuildInfo       `json:"rebuild,omitempty"`
}

// KeyDeviceOut es un dispositivo donde guardar un archivo de clave.
type KeyDeviceOut struct {
	UUID  string `json:"uuid"`
	FS    string `json:"fs"`
	Label string `json:"label"`
	Size  uint64 `json:"size"`
	Disk  string `json:"disk"`
	USB   bool   `json:"usb"`
}

type poolsAPI struct {
	s     *storageAPI
	agent PoolsAgent

	mu       sync.Mutex
	failures map[string][]time.Time // volumen → desbloqueos fallidos recientes
	tried    map[string]bool        // volumen+llave ya intentados al arrancar
}

var profileLabels = map[string]string{"basic": "Básico", "raid1": "RAID 1", "raid5": "RAID 5", "raid6": "RAID 6", "raid10": "RAID 10",
	"hybrid1": "Híbrido-1", "hybrid2": "Híbrido-2"}

// registerPools agrega los métodos de §16. Se llama después de registerStorage.
func (a *API) registerPools(r *rtcrpc.Router, d Deps) {
	ag, ok := d.Agent.(PoolsAgent)
	if !ok || a.storage == nil {
		return
	}
	p := &poolsAPI{s: a.storage, agent: ag, failures: map[string][]time.Time{}, tried: map[string]bool{}}
	a.pools = p
	admin := func(sess *rtcrpc.Session) error { return a.requireAdmin(sess) }

	type planP struct {
		Name    string   `json:"name"`
		Profile string   `json:"profile"`
		Disks   []string `json:"disks"`
	}
	type confirmP struct {
		PlanID       string `json:"plan_id"`
		ConfirmToken string `json:"confirm_token"`
		TypedName    string `json:"typed_name"`
	}
	type poolP struct {
		Pool string `json:"pool"`
	}
	type expandP struct {
		Pool string   `json:"pool"`
		Add  []string `json:"add"`
	}
	type replaceP struct {
		Pool        string  `json:"pool"`
		Failed      *string `json:"failed"`
		Replacement string  `json:"replacement"`
	}
	type encP struct {
		Passphrase string  `json:"passphrase"`
		KeyDevice  *string `json:"keyDevice"`
	}
	type volCreateP struct {
		Pool       string `json:"pool"`
		Name       string `json:"name"`
		Size       uint64 `json:"size"`
		FS         string `json:"fs"`
		Encryption *encP  `json:"encryption"`
	}
	type resizeP struct {
		Volume string `json:"volume"`
		Size   uint64 `json:"size"`
	}
	type unlockP struct {
		Volume       string `json:"volume"`
		Passphrase   string `json:"passphrase"`
		UseKeyDevice bool   `json:"useKeyDevice"`
	}
	type volP struct {
		Volume string `json:"volume"`
	}
	type passP struct {
		Volume string `json:"volume"`
		Old    string `json:"old"`
		New    string `json:"new"`
	}
	type keyP struct {
		Volume     string  `json:"volume"`
		Passphrase string  `json:"passphrase"`
		Device     *string `json:"device"`
	}
	type cachePlanP struct {
		Volume string   `json:"volume"`
		Disks  []string `json:"disks"`
		Mode   string   `json:"mode"`
	}
	type jobR struct {
		Job string `json:"job"`
	}

	r.Handle("storage.pools.list", func(ctx context.Context, _ *rtcrpc.Session, _ json.RawMessage) (any, error) {
		pools, err := d.Inventory.Pools(ctx)
		if err != nil {
			return nil, err
		}
		return nonNil(pools), nil
	})
	r.Handle("storage.pools.plan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x planP) (PoolPlan, error) {
		if err := admin(sess); err != nil {
			return PoolPlan{}, err
		}
		return p.createPlan(ctx, x.Name, x.Profile, x.Disks)
	}))
	r.Handle("storage.pools.create", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := p.create(ctx, x.PlanID, x.ConfirmToken, x.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.pools.expandPlan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x expandP) (PoolPlan, error) {
		if err := admin(sess); err != nil {
			return PoolPlan{}, err
		}
		return p.expandPlan(ctx, x.Pool, x.Add)
	}))
	r.Handle("storage.pools.expand", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := p.expand(ctx, x.PlanID, x.ConfirmToken, x.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.pools.replacePlan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x replaceP) (PoolPlan, error) {
		if err := admin(sess); err != nil {
			return PoolPlan{}, err
		}
		failed := ""
		if x.Failed != nil {
			failed = *x.Failed
		}
		return p.replacePlan(ctx, x.Pool, failed, x.Replacement)
	}))
	r.Handle("storage.pools.replace", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := p.replace(ctx, x.PlanID, x.ConfirmToken, x.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.pools.deletePlan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x poolP) (DeletePlan, error) {
		if err := admin(sess); err != nil {
			return DeletePlan{}, err
		}
		return p.deletePlan(ctx, x.Pool)
	}))
	r.Handle("storage.pools.delete", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := p.delete(ctx, x.PlanID, x.ConfirmToken, x.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.poolVolumes.create", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x volCreateP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		pass, key := "", ""
		if x.Encryption != nil {
			pass = x.Encryption.Passphrase
			if x.Encryption.KeyDevice != nil {
				key = *x.Encryption.KeyDevice
			}
			if pass == "" {
				return jobR{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "falta la frase de cifrado")
			}
		}
		id, err := p.createVolume(ctx, x.Pool, x.Name, x.Size, x.FS, pass, key)
		return jobR{id}, err
	}))
	r.Handle("storage.poolVolumes.resize", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x resizeP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := p.resize(ctx, x.Volume, x.Size)
		return jobR{id}, err
	}))
	r.Handle("storage.volumes.unlock", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x unlockP) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, p.unlock(ctx, x.Volume, x.Passphrase, x.UseKeyDevice)
	}))
	r.Handle("storage.volumes.lock", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x volP) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, p.lock(ctx, x.Volume)
	}))
	r.Handle("storage.volumes.passphrase", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x passP) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, p.passphrase(ctx, x.Volume, x.Old, x.New)
	}))
	r.Handle("storage.volumes.keyDevice", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x keyP) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		dev := ""
		if x.Device != nil {
			dev = *x.Device
		}
		return struct{}{}, p.keyDevice(ctx, x.Volume, x.Passphrase, dev)
	}))
	r.Handle("storage.keyDevices.list", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, _ struct{}) ([]KeyDeviceOut, error) {
		if err := admin(sess); err != nil {
			return nil, err
		}
		return p.keyDevices(ctx)
	}))
	r.Handle("storage.cache.plan", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x cachePlanP) (PoolPlan, error) {
		if err := admin(sess); err != nil {
			return PoolPlan{}, err
		}
		return p.cachePlan(ctx, x.Volume, x.Disks, x.Mode)
	}))
	r.Handle("storage.cache.create", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x confirmP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := p.cacheCreate(ctx, x.PlanID, x.ConfirmToken, x.TypedName)
		return jobR{id}, err
	}))
	r.Handle("storage.cache.remove", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x volP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		id, err := p.cacheRemove(ctx, x.Volume)
		return jobR{id}, err
	}))
}

// ---- Ayudas ---------------------------------------------------------------------------------

func (p *poolsAPI) changed(what ...string) {
	p.s.inv.InvalidatePools()
	for _, w := range what {
		p.s.a.d.Hub.Publish("storage.changed", map[string]string{"what": w})
	}
}

func (p *poolsAPI) notify(j jobs.Job, ok, okBody, failTitle string) {
	if j.State == jobs.Succeeded {
		p.s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.storage", Title: ok, Body: okBody})
	} else {
		p.s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.storage", Title: failTitle, Body: j.Error})
	}
}

func (p *poolsAPI) poolByID(ctx context.Context, id string) (storage.Pool, bool) {
	pools, _ := p.s.inv.Pools(ctx)
	for _, pl := range pools {
		if pl.ID == id || pl.Name == id {
			return pl, true
		}
	}
	return storage.Pool{}, false
}

// pick valida discos libres y los inspecciona con el agente (como §7).
func (p *poolsAPI) pick(ctx context.Context, ids []string, check func(storage.Disk) error) ([]planDisk, error) {
	disks, err := p.s.inv.Disks(ctx)
	if err != nil {
		return nil, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
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
			return nil, rtcrpc.Errorf("DEVICE_NOT_FOUND", "no existe el disco %s", id)
		case seen[id]:
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "disco repetido")
		case d.Use == "system":
			return nil, rtcrpc.Errorf(codeNotEligible, "%s es el disco del sistema", label(d))
		case d.Use != "free":
			return nil, rtcrpc.Errorf(codeNotEligible, "%s ya está en uso", label(d))
		case d.ByID == "":
			return nil, rtcrpc.Errorf(codeNotEligible, "%s no tiene una identidad estable", label(d))
		}
		if check != nil {
			if err := check(d); err != nil {
				return nil, err
			}
		}
		seen[id] = true
		chosen = append(chosen, d)
	}
	devIDs := make([]string, len(chosen))
	for i, d := range chosen {
		devIDs[i] = d.ByID
	}
	ins, err := p.s.agent.DiskInspect(ctx, devIDs)
	if err != nil {
		return nil, agentErr(err)
	}
	if len(ins) != len(chosen) {
		return nil, rtcrpc.Errorf(rtcrpc.CodeInternal, "respuesta incompleta del agente")
	}
	out := make([]planDisk, len(chosen))
	for i, d := range chosen {
		if !ins[i].Eligible {
			return nil, rtcrpc.Errorf(codeNotEligible, "%s: %s", label(d), ins[i].Reason)
		}
		out[i] = planDisk{ID: d.ID, ByID: d.ByID, Model: d.Model, Serial: d.Serial, Size: ins[i].Size, Content: ins[i].Content, Fingerprint: ins[i].Fingerprint}
	}
	return out, nil
}

// recheck revalida las huellas del plan justo antes de crear el job.
func (p *poolsAPI) recheck(ctx context.Context, planID string, disks []planDisk) error {
	if len(disks) == 0 {
		return nil
	}
	ids := make([]string, len(disks))
	for i, d := range disks {
		ids[i] = d.ByID
	}
	ins, err := p.s.agent.DiskInspect(ctx, ids)
	if err != nil {
		return agentErr(err)
	}
	for i, d := range disks {
		if i >= len(ins) || ins[i].Fingerprint != d.Fingerprint || ins[i].Size != d.Size {
			p.s.planner.consume(planID)
			return rtcrpc.Errorf(codeChanged, "el disco %s cambió desde el plan: revisalo de nuevo", d.Serial)
		}
		if !ins[i].Eligible {
			p.s.planner.consume(planID)
			return rtcrpc.Errorf(codeNotEligible, "el disco %s ya no es elegible: %s", d.Serial, ins[i].Reason)
		}
	}
	return nil
}

func diskOuts(pds []planDisk) []PlanDiskOut {
	out := []PlanDiskOut{}
	for _, d := range pds {
		out = append(out, PlanDiskOut{ID: d.ID, Model: d.Model, Serial: d.Serial, Size: d.Size, Content: d.Content})
	}
	return out
}

func toPlan(v *volumePlan, sum PoolPlanSummary) PoolPlan {
	if sum.Tiers == nil {
		sum.Tiers = []storage.PoolTier{}
	}
	if sum.Disks == nil {
		sum.Disks = []PlanDiskOut{}
	}
	return PoolPlan{PlanID: v.ID, Expires: v.Expires.UTC().Format(time.RFC3339), ConfirmToken: v.Token, Summary: sum}
}

// tiersOut convierte los tramos calculados (índices de discos) en PoolTier con Disk.id.
func tiersOut(tiers []diskops.Tier, ids []string) []storage.PoolTier {
	out := []storage.PoolTier{}
	for _, t := range tiers {
		pt := storage.PoolTier{Index: t.Index, Size: t.Size, Level: t.Level, Usable: t.Usable, Members: []string{}}
		for _, i := range t.Members {
			pt.Members = append(pt.Members, ids[i])
		}
		out = append(out, pt)
	}
	return out
}

func (p *poolsAPI) nameTaken(ctx context.Context, name string) error {
	pools, _ := p.s.inv.Pools(ctx)
	for _, pl := range pools {
		if pl.Name == name {
			return rtcrpc.Errorf(codePoolExists, "ya existe un grupo %s", name)
		}
	}
	vols, _ := p.s.inv.Volumes(ctx)
	for _, v := range vols {
		if v.Name == name || v.Mount == "/"+name {
			return rtcrpc.Errorf(codeVolumeExists, "%s ya es el nombre de un volumen", name)
		}
	}
	return nil
}

// ---- Crear un grupo ---------------------------------------------------------------------------

func (p *poolsAPI) createPlan(ctx context.Context, name, profile string, ids []string) (PoolPlan, error) {
	if err := diskops.ValidPoolName(name); err != nil {
		return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	if err := diskops.CheckPoolDisks(profile, len(ids)); err != nil {
		return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	if err := p.nameTaken(ctx, name); err != nil {
		return PoolPlan{}, err
	}
	pds, err := p.pick(ctx, ids, nil)
	if err != nil {
		return PoolPlan{}, err
	}
	sizes := make([]uint64, len(pds))
	diskIDs := make([]string, len(pds))
	for i, d := range pds {
		sizes[i], diskIDs[i] = d.Size, d.ID
	}
	tiers, err := diskops.PoolLayout(profile, sizes)
	if err != nil {
		return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	sum := PoolPlanSummary{Name: name, Profile: profile, FS: "lvm", Usable: diskops.TiersUsable(tiers), Disks: diskOuts(pds),
		Tiers: tiersOut(tiers, diskIDs), Unused: diskops.TiersUnused(tiers)}
	switch {
	case diskops.IsHybrid(profile) && sum.Unused > 0:
		sum.Notes = append(sum.Notes, fmt.Sprintf("%s quedan reservados hasta que sumes otro disco igual o más grande que el mayor: ahí se suman al grupo con redundancia.", humanBytes(sum.Unused)))
	case !diskops.IsHybrid(profile) && profile != "basic" && diskops.MixedSizes(sizes):
		sum.Notes = append(sum.Notes, fmt.Sprintf("Los discos tienen distinto tamaño: se usa %s de cada uno. Con RAID híbrido se aprovecharían.", humanBytes(diskops.MinSize(sizes))))
	}
	if diskops.IsHybrid(profile) {
		sum.Notes = append(sum.Notes, fmt.Sprintf("%s: el grupo sigue funcionando si falla %s.", profileLabels[profile], map[string]string{"hybrid1": "un disco", "hybrid2": "hasta dos discos"}[profile]))
	}
	sum.Notes = append(sum.Notes, "Después creás uno o más volúmenes dentro del grupo (Btrfs, con cifrado opcional).")
	v := p.s.planner.store(&volumePlan{Kind: planPoolCreate, Name: name, Profile: profile, Disks: pds})
	return toPlan(v, sum), nil
}

func (p *poolsAPI) create(ctx context.Context, planID, token, typed string) (string, error) {
	v, code, msg := p.s.planner.checkKind(planPoolCreate, planID, token, typed)
	if v == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	if err := p.recheck(ctx, planID, v.Disks); err != nil {
		return "", err
	}
	p.s.planner.consume(planID)
	args := agentproto.PoolCreateArgs{Name: v.Name, Profile: v.Profile}
	for _, d := range v.Disks {
		args.Disks = append(args.Disks, agentproto.VolumeDisk{DevID: d.ByID, Fingerprint: d.Fingerprint})
	}
	j := p.s.jobs.Start("storage.pool.create", "Creando el grupo "+v.Name, fmt.Sprintf("%s · %d discos", profileLabels[v.Profile], len(v.Disks)),
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			if err := p.agent.PoolCreate(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := p.s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			p.changed("pools", "disks", "volumes")
			p.notify(j, "El grupo "+v.Name+" está listo", "Ya podés crear volúmenes en él.", "No se pudo crear el grupo "+v.Name)
		})
	return j.ID, nil
}

// ---- Ampliar ------------------------------------------------------------------------------------

func (p *poolsAPI) expandPlan(ctx context.Context, poolID string, add []string) (PoolPlan, error) {
	pl, ok := p.poolByID(ctx, poolID)
	if !ok {
		return PoolPlan{}, rtcrpc.Errorf(codePoolNotFound, "no existe el grupo")
	}
	switch pl.Profile {
	case "raid5", "raid6", "raid10", "hybrid1", "hybrid2":
	default:
		return PoolPlan{}, rtcrpc.Errorf(codeNotSupported, "un grupo %s no gana espacio sumando discos", profileLabels[pl.Profile])
	}
	if pl.State != "healthy" {
		return PoolPlan{}, rtcrpc.Errorf(codeVolumeBusy, "el grupo tiene que estar sano y sin tareas en curso para ampliarlo")
	}
	if len(add) == 0 || len(add) > 8 {
		return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "elegí entre 1 y 8 discos")
	}
	pds, err := p.pick(ctx, add, nil)
	if err != nil {
		return PoolPlan{}, err
	}
	before := uint64(0)
	for _, t := range pl.Tiers {
		before += t.Usable
	}
	sum := PoolPlanSummary{Name: pl.Name, Profile: pl.Profile, FS: "lvm", Disks: diskOuts(pds)}
	if diskops.IsHybrid(pl.Profile) {
		// Límites actuales y hasta qué tramo llega cada disco.
		sort.Slice(pl.Tiers, func(i, j int) bool { return pl.Tiers[i].Index < pl.Tiers[j].Index })
		var bounds []uint64
		reach := map[string]int{}
		acc := uint64(0)
		for k, t := range pl.Tiers {
			acc += t.Size
			bounds = append(bounds, acc)
			for _, d := range t.Members {
				reach[d] = k
			}
		}
		var usable []uint64
		var ids []string
		for d, k := range reach {
			usable = append(usable, bounds[k])
			ids = append(ids, d)
		}
		for _, d := range pds {
			u := diskops.HybridUsable(d.Size)
			nb, err := diskops.HybridGrow(bounds, u)
			if err != nil {
				return PoolPlan{}, rtcrpc.Errorf(codeTooSmall, "%s tiene %s: %v", d.Serial, humanBytes(d.Size), err)
			}
			bounds = nb
			usable = append(usable, u)
			ids = append(ids, d.ID)
		}
		tiers := diskops.TiersFromBoundaries(pl.Profile, bounds, usable)
		sum.Tiers, sum.Usable, sum.Unused = tiersOut(tiers, ids), diskops.TiersUsable(tiers), diskops.TiersUnused(tiers)
	} else {
		n := len(pl.Disks) + len(pds)
		if err := diskops.CheckProfileDisks(pl.Profile, n); err != nil {
			return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
		size := pl.Tiers[0].Size
		for _, d := range pds {
			if d.Size < size {
				return PoolPlan{}, rtcrpc.Errorf(codeTooSmall, "%s es más chico que los discos del RAID (%s)", d.Serial, humanBytes(size))
			}
		}
		sizes := make([]uint64, n)
		for i := range sizes {
			sizes[i] = size
		}
		sum.Usable = diskops.UsableBytes(pl.Profile, sizes)
		sum.Tiers = []storage.PoolTier{{Index: 1, Size: size, Level: pl.Profile, Usable: sum.Usable, Members: append(slices.Clone(pl.Disks), func() []string {
			var x []string
			for _, d := range pds {
				x = append(x, d.ID)
			}
			return x
		}()...)}}
	}
	sum.Notes = append(sum.Notes, fmt.Sprintf("El grupo pasa de %s a %s útiles; los volúmenes no crecen solos: agrandalos después.", humanBytes(before), humanBytes(sum.Usable)),
		"La reorganización (reshape) mueve datos y puede tardar horas. Los volúmenes siguen disponibles mientras tanto.")
	v := p.s.planner.store(&volumePlan{Kind: planPoolExpand, Name: pl.Name, Profile: pl.Profile, Disks: pds, VolumeID: pl.ID})
	return toPlan(v, sum), nil
}

func (p *poolsAPI) expand(ctx context.Context, planID, token, typed string) (string, error) {
	v, code, msg := p.s.planner.checkKind(planPoolExpand, planID, token, typed)
	if v == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	if err := p.recheck(ctx, planID, v.Disks); err != nil {
		return "", err
	}
	p.s.planner.consume(planID)
	args := agentproto.PoolExpandArgs{Name: v.Name}
	for _, d := range v.Disks {
		args.Add = append(args.Add, agentproto.VolumeDisk{DevID: d.ByID, Fingerprint: d.Fingerprint})
	}
	j := p.s.jobs.Start("storage.pool.expand", "Ampliando el grupo "+v.Name, fmt.Sprintf("%d disco(s) nuevo(s)", len(v.Disks)),
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			if err := p.agent.PoolExpand(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := p.s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			p.changed("pools", "disks", "volumes")
			body := "Tiene más espacio libre para sus volúmenes."
			if pl, ok := p.poolByID(context.Background(), v.VolumeID); ok {
				body = "Ahora tiene " + humanBytes(pl.Free) + " libres para sus volúmenes."
			}
			p.notify(j, "El grupo "+v.Name+" se amplió", body, "No se pudo ampliar el grupo "+v.Name)
		})
	return j.ID, nil
}

// ---- Reemplazar un disco -------------------------------------------------------------------------

func (p *poolsAPI) replacePlan(ctx context.Context, poolID, failedID, replID string) (PoolPlan, error) {
	pl, ok := p.poolByID(ctx, poolID)
	if !ok {
		return PoolPlan{}, rtcrpc.Errorf(codePoolNotFound, "no existe el grupo")
	}
	if pl.Profile == "basic" {
		return PoolPlan{}, rtcrpc.Errorf(codeNotSupported, "un grupo Básico no tiene otra copia de los datos para reconstruir")
	}
	if pl.Task == "reshape" {
		return PoolPlan{}, rtcrpc.Errorf(codeVolumeBusy, "el grupo se está reorganizando: esperá a que termine")
	}
	disks, err := p.s.inv.Disks(ctx)
	if err != nil {
		return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
	}
	byID := map[string]storage.Disk{}
	for _, d := range disks {
		byID[d.ID] = d
	}
	var failed *storage.Disk
	if failedID != "" {
		d, ok := byID[failedID]
		if !ok || !slices.Contains(pl.Disks, failedID) {
			return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "ese disco no es parte del grupo %s", pl.Name)
		}
		if d.ByID == "" {
			return PoolPlan{}, rtcrpc.Errorf(codeNotEligible, "%s no tiene una identidad estable", label(d))
		}
		failed = &d
	} else {
		for _, id := range pl.Disks {
			if d := byID[id]; d.Use == "failed" {
				dd := d
				failed = &dd
			}
		}
		if failed == nil && pl.State != "degraded" {
			return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "el grupo %s no está degradado: indicá qué disco reemplazar", pl.Name)
		}
	}
	// El disco nuevo tiene que cubrir los tramos del que reemplaza.
	need := uint64(0)
	acc := uint64(0)
	sort.Slice(pl.Tiers, func(i, j int) bool { return pl.Tiers[i].Index < pl.Tiers[j].Index })
	for _, t := range pl.Tiers {
		acc += t.Size
		if (failed != nil && slices.Contains(t.Members, failed.ID)) || (failed == nil && t.State == "degraded") {
			need = acc
		}
	}
	if !diskops.IsHybrid(pl.Profile) && len(pl.Tiers) > 0 {
		need = pl.Tiers[0].Size
	}
	pds, err := p.pick(ctx, []string{replID}, func(d storage.Disk) error {
		have := d.Size
		if diskops.IsHybrid(pl.Profile) {
			have = diskops.HybridUsable(d.Size)
		}
		if have < need {
			return rtcrpc.Errorf(codeTooSmall, "%s tiene %s y tiene que cubrir %s", label(d), humanBytes(d.Size), humanBytes(need))
		}
		return nil
	})
	if err != nil {
		return PoolPlan{}, err
	}
	aux := ""
	var from *string
	if failed != nil {
		aux = failed.ByID
		id := failed.ID
		from = &id
	}
	sum := PoolPlanSummary{Name: pl.Name, Profile: pl.Profile, FS: "lvm", Usable: pl.Size, Disks: diskOuts(pds), Tiers: pl.Tiers,
		Rebuild: &RebuildInfo{From: from, To: pds[0].ID, EstimatedSec: int64(float64(max(need, 1)) / rebuildRate)}}
	if failed != nil {
		sum.Notes = append(sum.Notes, label(*failed)+" sale del grupo y se le borran las firmas.")
	}
	sum.Notes = append(sum.Notes, "Mientras se reconstruye, los volúmenes siguen disponibles pero sin redundancia.")
	v := p.s.planner.store(&volumePlan{Kind: planPoolReplace, Name: pl.Name, Profile: pl.Profile, Disks: pds, VolumeID: pl.ID, Aux: []string{aux}})
	return toPlan(v, sum), nil
}

func (p *poolsAPI) replace(ctx context.Context, planID, token, typed string) (string, error) {
	v, code, msg := p.s.planner.checkKind(planPoolReplace, planID, token, typed)
	if v == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	if err := p.recheck(ctx, planID, v.Disks); err != nil {
		return "", err
	}
	p.s.planner.consume(planID)
	repl := v.Disks[0]
	args := agentproto.PoolReplaceArgs{Name: v.Name, Failed: v.Aux[0], Replacement: agentproto.VolumeDisk{DevID: repl.ByID, Fingerprint: repl.Fingerprint}}
	j := p.s.jobs.Start("storage.pool.replace", "Reemplazando un disco del grupo "+v.Name, "Reconstrucción sobre "+repl.Serial,
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			if err := p.agent.PoolReplace(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := p.s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			p.changed("pools", "disks", "volumes")
			p.notify(j, "Disco reemplazado en el grupo "+v.Name, "La reconstrucción terminó: el grupo está sano.", "No se pudo reemplazar el disco del grupo "+v.Name)
		})
	return j.ID, nil
}

// ---- Eliminar un grupo ------------------------------------------------------------------------

func (p *poolsAPI) deletePlan(ctx context.Context, poolID string) (DeletePlan, error) {
	pl, ok := p.poolByID(ctx, poolID)
	if !ok {
		return DeletePlan{}, rtcrpc.Errorf(codePoolNotFound, "no existe el grupo")
	}
	if len(pl.Volumes) > 0 {
		return DeletePlan{}, rtcrpc.Errorf(agentproto.CodePoolNotEmpty, "el grupo %s tiene %d volumen(es): eliminalos primero", pl.Name, len(pl.Volumes))
	}
	disks, err := p.s.inv.Disks(ctx)
	if err != nil {
		return DeletePlan{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
	}
	byID := map[string]storage.Disk{}
	for _, d := range disks {
		byID[d.ID] = d
	}
	out := DeletePlan{Summary: DeleteSummary{Name: pl.Name, Shares: []string{}, Disks: []DeleteDisk{}}}
	var pds []planDisk
	for _, id := range append(slices.Clone(pl.Disks), pl.CacheDisks...) {
		d, ok := byID[id]
		if !ok || d.ByID == "" {
			return DeletePlan{}, rtcrpc.Errorf(codeChanged, "no se pudo identificar un disco del grupo: revisá el inventario")
		}
		pds = append(pds, planDisk{ID: d.ID, ByID: d.ByID, Model: d.Model, Serial: d.Serial, Size: d.Size})
		out.Summary.Disks = append(out.Summary.Disks, DeleteDisk{ID: d.ID, Model: d.Model, Serial: d.Serial})
	}
	v := p.s.planner.store(&volumePlan{Kind: planPoolDelete, Name: pl.Name, Disks: pds, VolumeID: pl.ID})
	out.PlanID, out.ConfirmToken, out.Expires = v.ID, v.Token, v.Expires.UTC().Format(time.RFC3339)
	return out, nil
}

func (p *poolsAPI) delete(ctx context.Context, planID, token, typed string) (string, error) {
	v, code, msg := p.s.planner.checkKind(planPoolDelete, planID, token, typed)
	if v == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	p.s.planner.consume(planID)
	args := agentproto.PoolDeleteArgs{Name: v.Name}
	for _, d := range v.Disks {
		args.DevIDs = append(args.DevIDs, d.ByID)
	}
	j := p.s.jobs.Start("storage.pool.delete", "Eliminando el grupo "+v.Name, fmt.Sprintf("%d discos quedan libres", len(v.Disks)),
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			if err := p.agent.PoolDelete(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := p.s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			p.changed("pools", "disks")
			p.notify(j, "El grupo "+v.Name+" se eliminó", fmt.Sprintf("Los %d discos quedaron libres.", len(v.Disks)), "No se pudo eliminar el grupo "+v.Name)
		})
	return j.ID, nil
}

// ---- Volúmenes de un grupo --------------------------------------------------------------------

func (p *poolsAPI) createVolume(ctx context.Context, poolID, name string, size uint64, fs, pass, keyDev string) (string, error) {
	pl, ok := p.poolByID(ctx, poolID)
	if !ok {
		return "", rtcrpc.Errorf(codePoolNotFound, "no existe el grupo")
	}
	if err := diskops.ValidVolumeName(name); err != nil {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	if fs == "" {
		fs = "btrfs"
	}
	if !diskops.PoolFS[fs] {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "filesystem no soportado (btrfs, xfs o ext4)")
	}
	if err := p.nameTaken(ctx, name); err != nil {
		return "", err
	}
	free := diskops.AlignExtent(pl.Free)
	if size == 0 {
		size = free
	}
	size = diskops.AlignExtent(size)
	if size < 256<<20 {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "un volumen tiene al menos 256 MB")
	}
	if size > free {
		return "", rtcrpc.Errorf(codeNoSpace, "el grupo %s tiene %s libres", pl.Name, humanBytes(free))
	}
	if pass != "" {
		if err := diskops.ValidPassphrase(pass); err != nil {
			return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
	}
	if keyDev != "" {
		if pass == "" {
			return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "el archivo de clave solo sirve para volúmenes cifrados")
		}
		if err := diskops.ValidKeyDeviceUUID(keyDev); err != nil {
			return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
	}
	args := agentproto.LVCreateArgs{Pool: pl.Name, Name: name, Size: size, FS: fs, Passphrase: pass, KeyDevice: keyDev}
	detail := fmt.Sprintf("%s · %s en el grupo %s", strings.ToUpper(fs[:1])+fs[1:], humanBytes(size), pl.Name)
	if pass != "" {
		detail += " · cifrado"
	}
	j := p.s.jobs.Start("storage.volume.create", "Creando "+name, detail,
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			err := p.agent.LVCreate(ctx, args)
			args.Passphrase = "" // no queda en memoria más de lo necesario
			if err != nil {
				return errors.New(agentMessage(err))
			}
			_, err = p.s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			p.changed("pools", "volumes")
			body := fmt.Sprintf("%s montado en /%s.", humanBytes(size), name)
			if pass != "" {
				body += " Cifrado con LUKS2: guardá la frase en un lugar seguro, sin ella no se recuperan los datos."
			}
			p.notify(j, name+" está listo", body, "No se pudo crear "+name)
		})
	return j.ID, nil
}

func (p *poolsAPI) poolVolume(ctx context.Context, volID string) (storage.Volume, error) {
	v, ok := p.s.volumeByID(ctx, volID)
	if !ok {
		return v, rtcrpc.Errorf("VOLUME_NOT_FOUND", "no existe el volumen")
	}
	if v.Kind != "pool" {
		return v, rtcrpc.Errorf(codeNotSupported, "%s es un volumen clásico (sin grupo)", v.Name)
	}
	return v, nil
}

func (p *poolsAPI) resize(ctx context.Context, volID string, size uint64) (string, error) {
	v, err := p.poolVolume(ctx, volID)
	if err != nil {
		return "", err
	}
	if v.Locked {
		return "", rtcrpc.Errorf(codeLocked, "%s está bloqueado: desbloquealo para agrandarlo", v.Name)
	}
	pl, _ := p.poolByID(ctx, v.Pool)
	cur := p.lvSize(ctx, v)
	size = diskops.AlignExtent(size)
	if size <= cur {
		return "", rtcrpc.Errorf(codeNotSupported, "solo se puede agrandar un volumen (hoy tiene %s)", humanBytes(cur))
	}
	if size-cur > pl.Free {
		return "", rtcrpc.Errorf(codeNoSpace, "el grupo %s tiene %s libres", pl.Name, humanBytes(pl.Free))
	}
	args := agentproto.LVResizeArgs{Name: v.Name, Size: size}
	j := p.s.jobs.Start("storage.volume.resize", "Agrandando "+v.Name, "A "+humanBytes(size),
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			if err := p.agent.LVResize(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := p.s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			p.changed("pools", "volumes")
			p.notify(j, v.Name+" se agrandó", "Ahora tiene "+humanBytes(size)+".", "No se pudo agrandar "+v.Name)
		})
	return j.ID, nil
}

// lvSize es el tamaño del volumen lógico (no el del filesystem).
func (p *poolsAPI) lvSize(ctx context.Context, v storage.Volume) uint64 {
	for _, pl := range p.s.inv.PoolReport(ctx).Pools {
		for _, lv := range pl.LVs {
			if lv.UUID == v.ID {
				return lv.Size
			}
		}
	}
	return v.Size
}

// deletePoolVolumePlan es storage.volumes.deletePlan para un volumen de un grupo: no
// borra discos (el espacio vuelve al grupo).
func (s *storageAPI) deletePoolVolumePlan(ctx context.Context, v storage.Volume) (DeletePlan, error) {
	out := DeletePlan{Summary: DeleteSummary{Name: v.Name, Shares: []string{}, Used: v.Used, Disks: []DeleteDisk{}}}
	for _, sh := range s.shares(ctx) {
		if sh.Volume == v.ID {
			out.Summary.Shares = append(out.Summary.Shares, sh.Name)
			out.Summary.Snapshots += len(s.snapshots(ctx, sh.ID))
		}
	}
	p := s.planner.addDelete(v.Name, v.ID, v.Mount, nil, out.Summary.Shares)
	out.PlanID, out.ConfirmToken, out.Expires = p.ID, p.Token, p.Expires.UTC().Format(time.RFC3339)
	return out, nil
}

func (s *storageAPI) deletePoolVolume(p *volumePlan, v storage.Volume) (string, error) {
	pa := s.a.pools
	if pa == nil {
		return "", rtcrpc.Errorf(codeNotSupported, "el agente no maneja grupos de almacenamiento")
	}
	args := agentproto.LVDeleteArgs{Name: v.Name, UUID: v.ID, Shares: p.Shares}
	j := s.jobs.Start("storage.volume.delete", "Eliminando "+v.Name, "El espacio vuelve al grupo",
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			if s.a.files != nil {
				s.a.files.ForgetUnder("/" + v.Name + "/")
				s.a.files.InvalidateWorkers()
			}
			if err := pa.agent.LVDelete(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			if j.State == jobs.Succeeded {
				s.mu.Lock()
				for _, sh := range p.Shares {
					delete(s.m.Shares, strings.ToLower(sh))
					delete(s.m.Perms, strings.ToLower(sh))
				}
				delete(s.m.LastScrub, p.Mount)
				s.save()
				s.mu.Unlock()
			}
			pa.changed("pools", "volumes", "disks")
			s.a.d.Hub.Publish("shares.changed", map[string]string{})
			pa.notify(j, v.Name+" eliminado", "El espacio volvió al grupo.", "No se pudo eliminar "+v.Name)
		})
	return j.ID, nil
}

// ---- Cifrado ------------------------------------------------------------------------------------

const (
	unlockMaxFails = 5
	unlockWindow   = 10 * time.Minute
)

func (p *poolsAPI) encrypted(ctx context.Context, volID string) (storage.Volume, error) {
	v, err := p.poolVolume(ctx, volID)
	if err != nil {
		return v, err
	}
	if !v.Encrypted {
		return v, rtcrpc.Errorf(codeNotSupported, "%s no está cifrado", v.Name)
	}
	return v, nil
}

func (p *poolsAPI) limited(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	var keep []time.Time
	for _, t := range p.failures[name] {
		if time.Since(t) < unlockWindow {
			keep = append(keep, t)
		}
	}
	p.failures[name] = keep
	return len(keep) >= unlockMaxFails
}

func (p *poolsAPI) failed(name string) {
	p.mu.Lock()
	p.failures[name] = append(p.failures[name], time.Now())
	p.mu.Unlock()
}

func (p *poolsAPI) unlock(ctx context.Context, volID, pass string, useKey bool) error {
	if z := p.s.a.zfs; z != nil && strings.HasPrefix(volID, "zfs-") { // cifrado nativo de ZFS (§18)
		if useKey {
			return rtcrpc.Errorf(codeNotSupported, "los volúmenes ZFS se desbloquean con la frase")
		}
		return z.unlock(ctx, volID, pass)
	}
	v, err := p.encrypted(ctx, volID)
	if err != nil {
		return err
	}
	if !useKey && pass == "" {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "falta la frase")
	}
	if p.limited(v.Name) {
		return rtcrpc.Errorf(agentproto.CodeRateLimited, "demasiados intentos fallidos: esperá unos minutos")
	}
	a := agentproto.LUKSUnlockArgs{Name: v.Name, UseKeyDevice: useKey}
	if !useKey {
		a.Passphrase = pass
	}
	if err := p.agent.LUKSUnlock(ctx, a); err != nil {
		var pe *agentproto.Error
		if errors.As(err, &pe) && pe.Code == agentproto.CodeWrongPassphrase {
			p.failed(v.Name)
		}
		return agentErr(err)
	}
	p.changed("pools", "volumes")
	p.s.a.d.Hub.Publish("shares.changed", map[string]string{})
	return nil
}

func (p *poolsAPI) lock(ctx context.Context, volID string) error {
	if z := p.s.a.zfs; z != nil && strings.HasPrefix(volID, "zfs-") {
		return z.lock(ctx, volID)
	}
	v, err := p.encrypted(ctx, volID)
	if err != nil {
		return err
	}
	if p.s.a.files != nil {
		p.s.a.files.ForgetUnder("/" + v.Name + "/")
		p.s.a.files.InvalidateWorkers() // sueltan los archivos abiertos antes de desmontar
	}
	if err := p.agent.LUKSLock(ctx, v.Name); err != nil {
		return agentErr(err)
	}
	p.changed("pools", "volumes")
	p.s.a.d.Hub.Publish("shares.changed", map[string]string{})
	return nil
}

func (p *poolsAPI) passphrase(ctx context.Context, volID, old, newPass string) error {
	if z := p.s.a.zfs; z != nil && strings.HasPrefix(volID, "zfs-") {
		return z.passphrase(ctx, volID, old, newPass)
	}
	v, err := p.encrypted(ctx, volID)
	if err != nil {
		return err
	}
	if err := diskops.ValidPassphrase(newPass); err != nil {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
	}
	if p.limited(v.Name) {
		return rtcrpc.Errorf(agentproto.CodeRateLimited, "demasiados intentos fallidos: esperá unos minutos")
	}
	if err := p.agent.LUKSPassphrase(ctx, agentproto.LUKSPassphraseArgs{Name: v.Name, Old: old, New: newPass}); err != nil {
		var pe *agentproto.Error
		if errors.As(err, &pe) && pe.Code == agentproto.CodeWrongPassphrase {
			p.failed(v.Name)
		}
		return agentErr(err)
	}
	return nil
}

func (p *poolsAPI) keyDevice(ctx context.Context, volID, pass, device string) error {
	v, err := p.encrypted(ctx, volID)
	if err != nil {
		return err
	}
	if device != "" {
		if err := diskops.ValidKeyDeviceUUID(device); err != nil {
			return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
	}
	if p.limited(v.Name) {
		return rtcrpc.Errorf(agentproto.CodeRateLimited, "demasiados intentos fallidos: esperá unos minutos")
	}
	if err := p.agent.LUKSKeyDevice(ctx, agentproto.LUKSKeyDeviceArgs{Name: v.Name, Passphrase: pass, Device: device}); err != nil {
		var pe *agentproto.Error
		if errors.As(err, &pe) && pe.Code == agentproto.CodeWrongPassphrase {
			p.failed(v.Name)
		}
		return agentErr(err)
	}
	p.changed("volumes")
	return nil
}

func (p *poolsAPI) keyDevices(ctx context.Context) ([]KeyDeviceOut, error) {
	devs, err := p.agent.KeyDevices(ctx)
	if err != nil {
		return nil, agentErr(err)
	}
	out := []KeyDeviceOut{}
	for _, d := range devs {
		out = append(out, KeyDeviceOut{UUID: d.UUID, FS: d.FS, Label: d.Label, Size: d.Size, Disk: p.s.inv.DiskID(d.Disk), USB: d.USB})
	}
	return out, nil
}

// autoUnlock desbloquea al arrancar los volúmenes cuyo dispositivo de clave está
// conectado (un intento por volumen y dispositivo, para no insistir si el archivo no sirve).
func (p *poolsAPI) autoUnlock(ctx context.Context) {
	vols, err := p.s.inv.Volumes(ctx)
	if err != nil {
		return
	}
	var want []storage.Volume
	for _, v := range vols {
		if v.Locked && v.KeyDevice != "" {
			p.mu.Lock()
			done := p.tried[v.ID+"|"+v.KeyDevice]
			p.mu.Unlock()
			if !done {
				want = append(want, v)
			}
		}
	}
	if len(want) == 0 {
		return
	}
	devs, err := p.agent.KeyDevices(ctx)
	if err != nil {
		return
	}
	for _, v := range want {
		present := false
		for _, d := range devs {
			present = present || strings.EqualFold(d.UUID, v.KeyDevice)
		}
		if !present {
			continue
		}
		p.mu.Lock()
		p.tried[v.ID+"|"+v.KeyDevice] = true
		p.mu.Unlock()
		if err := p.agent.LUKSUnlock(ctx, agentproto.LUKSUnlockArgs{Name: v.Name, UseKeyDevice: true}); err != nil {
			p.s.a.Notify(Notice{ID: "unlock_" + v.ID + "_" + fmt.Sprint(time.Now().Unix()), Level: "warn", App: "tango.storage",
				Title: v.Name + " sigue bloqueado", Body: "El dispositivo de clave está conectado pero no abrió el volumen: " + agentMessage(err)})
			continue
		}
		p.changed("volumes")
		p.s.a.Notify(Notice{ID: "unlock_" + v.ID + "_" + fmt.Sprint(time.Now().Unix()), Level: "ok", App: "tango.storage",
			Title: v.Name + " desbloqueado", Body: "Se abrió con el archivo de clave del dispositivo conectado."})
	}
}

// ---- Caché SSD -----------------------------------------------------------------------------------

func (p *poolsAPI) cachePlan(ctx context.Context, volID string, ids []string, mode string) (PoolPlan, error) {
	v, err := p.poolVolume(ctx, volID)
	if err != nil {
		return PoolPlan{}, err
	}
	if v.Cache != nil {
		return PoolPlan{}, rtcrpc.Errorf("CACHE_EXISTS", "%s ya tiene caché SSD", v.Name)
	}
	switch {
	case mode == "read" && (len(ids) == 1 || len(ids) == 2):
	case mode == "readwrite" && len(ids) == 2:
	case mode == "readwrite":
		return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la caché de lectura y escritura necesita 2 SSD (en RAID 1)")
	case mode == "read":
		return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la caché de lectura usa 1 o 2 SSD")
	default:
		return PoolPlan{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "modo de caché inválido")
	}
	pds, err := p.pick(ctx, ids, func(d storage.Disk) error {
		if d.Kind == "hdd" {
			return rtcrpc.Errorf("NOT_SSD", "%s no es una SSD", label(d))
		}
		return nil
	})
	if err != nil {
		return PoolPlan{}, err
	}
	pl, _ := p.poolByID(ctx, v.Pool)
	sizes := make([]uint64, len(pds))
	for i, d := range pds {
		sizes[i] = d.Size
	}
	sum := PoolPlanSummary{Name: v.Name, Profile: pl.Profile, FS: v.FS, Usable: diskops.MinSize(sizes), Disks: diskOuts(pds)}
	if len(pds) == 2 {
		sum.Notes = append(sum.Notes, "Las dos SSD van en RAID 1: si una falla, la caché sigue.")
	}
	if mode == "readwrite" {
		sum.Notes = append(sum.Notes, "Lectura y escritura: los datos nuevos quedan primero en las SSD y bajan a los discos después. Si fallan las dos SSD a la vez se pierden esas escrituras.")
	} else {
		sum.Notes = append(sum.Notes, "Solo lectura: acelera lo que más se lee; todo lo que se escribe va también a los discos.")
	}
	vp := p.s.planner.store(&volumePlan{Kind: planCache, Name: v.Name, Profile: mode, Disks: pds, VolumeID: v.ID})
	return toPlan(vp, sum), nil
}

func (p *poolsAPI) cacheCreate(ctx context.Context, planID, token, typed string) (string, error) {
	v, code, msg := p.s.planner.checkKind(planCache, planID, token, typed)
	if v == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	if err := p.recheck(ctx, planID, v.Disks); err != nil {
		return "", err
	}
	p.s.planner.consume(planID)
	args := agentproto.CacheCreateArgs{Name: v.Name, Mode: v.Profile}
	for _, d := range v.Disks {
		args.Disks = append(args.Disks, agentproto.VolumeDisk{DevID: d.ByID, Fingerprint: d.Fingerprint})
	}
	modeLabel := map[string]string{"read": "solo lectura", "readwrite": "lectura y escritura"}[v.Profile]
	j := p.s.jobs.Start("storage.cache.create", "Caché SSD para "+v.Name, modeLabel,
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			if err := p.agent.CacheCreate(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := p.s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			p.changed("pools", "volumes", "disks")
			p.notify(j, v.Name+" tiene caché SSD", "Caché de "+modeLabel+". Los aciertos se ven en el volumen y en el widget de almacenamiento.", "No se pudo agregar la caché a "+v.Name)
		})
	return j.ID, nil
}

func (p *poolsAPI) cacheRemove(ctx context.Context, volID string) (string, error) {
	v, err := p.poolVolume(ctx, volID)
	if err != nil {
		return "", err
	}
	if v.Cache == nil {
		return "", rtcrpc.Errorf("NOT_FOUND", "%s no tiene caché SSD", v.Name)
	}
	j := p.s.jobs.Start("storage.cache.remove", "Quitando la caché de "+v.Name, "Los bloques pendientes bajan a los discos",
		func(ctx context.Context, h *jobs.Handle) error {
			if err := p.agent.CacheRemove(ctx, agentproto.CacheRemoveArgs{OpID: h.ID(), Name: v.Name}); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := p.s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			p.changed("pools", "volumes", "disks")
			p.notify(j, "Caché quitada de "+v.Name, "Las SSD quedaron libres.", "No se pudo quitar la caché de "+v.Name)
		})
	return j.ID, nil
}
