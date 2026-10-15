//go:build linux

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/smart"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// registerPools agrega las operaciones de grupos de almacenamiento (protocol/README.md
// §16, ADR 0011): discos → md (o tramos híbridos) → LVM → volúmenes con LUKS2 opcional
// y caché SSD. Cada escritura revalida los discos con la huella del plan, como §7.
func registerPools(r *agentproto.Registry, paths smart.Paths, env diskops.Env, jobs *asyncJobs, log *slog.Logger) {
	p := &pools{paths: paths, env: env, log: log}
	poolsInst = p

	agentproto.Register(r, agentproto.OpPoolReport, nil, func(ctx context.Context, _ *agentproto.NoArgs) (any, *agentproto.Error) {
		rep, err := p.report(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "no está instalado") {
				return agentproto.PoolReport{Pools: []agentproto.PoolInfo{}}, nil
			}
			log.Warn("pool.report", "err", err, "output", execOutput(err))
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo leer LVM")
		}
		return rep, nil
	})

	agentproto.Register(r, agentproto.OpPoolCreate, validatePoolCreate, func(ctx context.Context, a *agentproto.PoolCreateArgs) (any, *agentproto.Error) {
		if _, e := revalidate(paths, env, a.Disks); e != nil {
			return nil, e
		}
		if e := p.poolFree(ctx, a.Name); e != nil {
			return nil, e
		}
		if e := jobs.start(a.OpID, agentproto.OpPoolCreate, a, func(ctx context.Context, step stepFn) (any, error) {
			return p.create(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpPoolExpand, validatePoolExpand, func(ctx context.Context, a *agentproto.PoolExpandArgs) (any, *agentproto.Error) {
		if _, e := p.findPool(ctx, a.Name); e != nil {
			return nil, e
		}
		if _, e := revalidate(paths, env, a.Add); e != nil {
			return nil, e
		}
		if e := jobs.start(a.OpID, agentproto.OpPoolExpand, a, func(ctx context.Context, step stepFn) (any, error) {
			return p.expand(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpPoolReplace, validatePoolReplace, func(ctx context.Context, a *agentproto.PoolReplaceArgs) (any, *agentproto.Error) {
		if _, e := p.findPool(ctx, a.Name); e != nil {
			return nil, e
		}
		if _, e := revalidate(paths, env, []agentproto.VolumeDisk{a.Replacement}); e != nil {
			return nil, e
		}
		if e := jobs.start(a.OpID, agentproto.OpPoolReplace, a, func(ctx context.Context, step stepFn) (any, error) {
			return p.replace(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpPoolDelete, validatePoolDelete, func(ctx context.Context, a *agentproto.PoolDeleteArgs) (any, *agentproto.Error) {
		pi, e := p.findPool(ctx, a.Name)
		if e != nil {
			return nil, e
		}
		if len(pi.LVs) > 0 {
			return nil, agentproto.Errf(agentproto.CodePoolNotEmpty, "el grupo %s todavía tiene volúmenes: eliminalos primero", a.Name)
		}
		if _, e := p.poolDevs(pi, a.DevIDs); e != nil {
			return nil, e
		}
		if e := jobs.start(a.OpID, agentproto.OpPoolDelete, a, func(ctx context.Context, step stepFn) (any, error) {
			return p.delete(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpLVCreate, validateLVCreate, func(ctx context.Context, a *agentproto.LVCreateArgs) (any, *agentproto.Error) {
		if _, e := p.findPool(ctx, a.Pool); e != nil {
			return nil, e
		}
		if e := p.volumeNameFree(ctx, a.Name); e != nil {
			return nil, e
		}
		// La frase nunca va al journal: se registra una copia sin ella.
		if e := jobs.start(a.OpID, agentproto.OpLVCreate, a.Redacted(), func(ctx context.Context, step stepFn) (any, error) {
			return p.createLV(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpLVResize, validateLVResize, func(ctx context.Context, a *agentproto.LVResizeArgs) (any, *agentproto.Error) {
		if _, _, e := p.findLV(ctx, a.Name); e != nil {
			return nil, e
		}
		if e := jobs.start(a.OpID, agentproto.OpLVResize, a, func(ctx context.Context, step stepFn) (any, error) {
			return p.resizeLV(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpLVDelete, validateLVDelete, func(ctx context.Context, a *agentproto.LVDeleteArgs) (any, *agentproto.Error) {
		_, lv, e := p.findLV(ctx, a.Name)
		if e != nil {
			return nil, e
		}
		if lv.UUID != a.UUID {
			return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "el volumen %s no es el que vio el plan", a.Name)
		}
		if e := jobs.start(a.OpID, agentproto.OpLVDelete, a, func(ctx context.Context, step stepFn) (any, error) {
			return p.deleteLV(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpLUKSUnlock, validateLUKSUnlock, p.unlock)
	agentproto.Register(r, agentproto.OpLUKSLock, validateLUKSLock, p.lock)
	agentproto.Register(r, agentproto.OpLUKSPassphrase, validateLUKSPassphrase, p.passphrase)
	agentproto.Register(r, agentproto.OpLUKSKeyDevice, validateLUKSKeyDevice, p.keyDevice)
	agentproto.Register(r, agentproto.OpKeyDevList, nil, func(context.Context, *agentproto.NoArgs) (any, *agentproto.Error) {
		return agentproto.KeyDevListResult{Devices: p.keyDevices()}, nil
	})

	agentproto.Register(r, agentproto.OpCacheCreate, validateCacheCreate, func(ctx context.Context, a *agentproto.CacheCreateArgs) (any, *agentproto.Error) {
		_, lv, e := p.findLV(ctx, a.Name)
		if e != nil {
			return nil, e
		}
		if lv.Cache != nil {
			return nil, agentproto.Errf("CACHE_EXISTS", "%s ya tiene caché SSD", a.Name)
		}
		devs, e := revalidate(paths, env, a.Disks)
		if e != nil {
			return nil, e
		}
		for i, d := range devs {
			if diskops.IsRotational(diskops.DevName(d)) {
				return nil, agentproto.Errf("NOT_SSD", "%s no es una SSD", a.Disks[i].DevID)
			}
		}
		if e := jobs.start(a.OpID, agentproto.OpCacheCreate, a, func(ctx context.Context, step stepFn) (any, error) {
			return p.createCache(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})

	agentproto.Register(r, agentproto.OpCacheRemove, validateCacheRemove, func(ctx context.Context, a *agentproto.CacheRemoveArgs) (any, *agentproto.Error) {
		_, lv, e := p.findLV(ctx, a.Name)
		if e != nil {
			return nil, e
		}
		if lv.Cache == nil {
			return nil, agentproto.Errf(agentproto.CodeNotFound, "%s no tiene caché SSD", a.Name)
		}
		if e := jobs.start(a.OpID, agentproto.OpCacheRemove, a, func(ctx context.Context, step stepFn) (any, error) {
			return p.removeCache(ctx, step, a)
		}); e != nil {
			return nil, e
		}
		return agentproto.OpRef{OpID: a.OpID}, nil
	})
}

// poolsInst es la instancia de los grupos (el clúster crea bricks en LV dedicados).
var poolsInst *pools

type pools struct {
	paths smart.Paths
	env   diskops.Env
	log   *slog.Logger
	luks  sync.Mutex // una operación de LUKS a la vez
}

// ---- Reporte --------------------------------------------------------------------------

// mdLinks mapea md del kernel (md125) → nombre (/dev/md/<nombre>).
func mdLinks() map[string]string {
	out := map[string]string{}
	ents, _ := os.ReadDir("/dev/md")
	for _, e := range ents {
		if t, err := filepath.EvalSymlinks(filepath.Join("/dev/md", e.Name())); err == nil {
			out[filepath.Base(t)] = e.Name()
		}
	}
	return out
}

// slavesOf devuelve los dispositivos (sdb2, sdc) que forman un md.
func slavesOf(md string) []string {
	ents, _ := os.ReadDir(filepath.Join("/sys/block", md, "slaves"))
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// diskOfPart lleva sdb2 → sdb (o deja sdb si ya es un disco).
func diskOfPart(dev string) string {
	p, err := filepath.EvalSymlinks(filepath.Join("/sys/class/block", dev))
	if err != nil {
		return dev
	}
	if _, err := os.Stat(filepath.Join(p, "partition")); err == nil {
		return filepath.Base(filepath.Dir(p))
	}
	return dev
}

func blockBytes(dev string) uint64 {
	b, _ := os.ReadFile(filepath.Join("/sys/class/block", dev, "size"))
	n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n * 512
}

func lvVisible(name string) bool {
	return !strings.HasPrefix(name, "[") && diskops.ValidVolumeName(name) == nil
}

func (p *pools) report(ctx context.Context) (*agentproto.PoolReport, error) {
	vgs, pvs, lvs, err := diskops.LVMReport(ctx)
	if err != nil {
		return nil, err
	}
	links := mdLinks()
	out := &agentproto.PoolReport{Pools: []agentproto.PoolInfo{}}
	for _, vg := range vgs {
		if !slices.Contains(vg.Tags, diskops.TagPool) {
			continue
		}
		pi := agentproto.PoolInfo{Name: vg.Name, UUID: vg.UUID, Profile: diskops.TagValue(vg.Tags, diskops.TagProfile),
			Size: vg.Size, Free: vg.Free, PVs: []agentproto.PoolPV{}, Tiers: []agentproto.PoolTier{}, LVs: []agentproto.PoolLV{}}
		pvByDev := map[string]agentproto.PoolPV{}
		memberDisks := map[string]bool{}
		for _, pv := range pvs {
			if pv.VG != vg.Name {
				continue
			}
			real, _ := filepath.EvalSymlinks(pv.Dev)
			k := filepath.Base(real)
			x := agentproto.PoolPV{Dev: pv.Dev, Role: diskops.TagValue(pv.Tags, diskops.TagRole), Size: pv.Size, Free: pv.Free}
			if strings.HasPrefix(k, "md") {
				x.MD = links[k]
				for _, s := range slavesOf(k) {
					x.Disks = append(x.Disks, diskOfPart(s))
				}
			} else {
				x.Disks = []string{diskOfPart(k)}
			}
			pi.PVs = append(pi.PVs, x)
			pvByDev[k] = x
			if x.Role != "cache" {
				for _, d := range x.Disks {
					memberDisks[d] = true
				}
			}
			// Tramo de datos o md de caché.
			t := agentproto.PoolTier{Members: []string{}}
			switch {
			case x.MD != "":
				pool, kind, n, ok := diskops.ParsePoolMDName(x.MD)
				if !ok || pool != vg.Name {
					continue
				}
				in := diskops.ReadMD(k)
				t.Index, t.MD, t.Name, t.Level = n, k, x.MD, in.Level
				comp, _ := os.ReadFile(filepath.Join("/sys/block", k, "md", "component_size"))
				kb, _ := strconv.ParseUint(strings.TrimSpace(string(comp)), 10, 64)
				t.Size = kb * 1024
				for _, s := range slavesOf(k) {
					t.Members = append(t.Members, diskOfPart(s))
					if diskOfPart(s) != s {
						t.Parts = append(t.Parts, s)
					}
				}
				if kind == "cache" {
					t.Role = "cache"
				}
			default:
				t.Index, t.Level, t.Size, t.Members = 1, "basic", blockBytes(k), x.Disks
				if x.Role == "cache" {
					t.Role = "cache"
				}
			}
			pi.Tiers = append(pi.Tiers, t)
		}
		// Híbridos: particiones de tramos que todavía no tienen md.
		if diskops.IsHybrid(pi.Profile) {
			used := map[string]bool{}
			for _, t := range pi.Tiers {
				for _, part := range t.Parts {
					used[part] = true
				}
			}
			unused := map[int]*agentproto.PoolTier{}
			for d := range memberDisks {
				for _, part := range diskops.Partitions(d) {
					pool, k, ok := diskops.ParseHybridPartName(diskops.PartName(part))
					if !ok || pool != vg.Name || used[part] {
						continue
					}
					t := unused[k]
					if t == nil {
						t = &agentproto.PoolTier{Index: k, Level: "unused", Size: blockBytes(part), Members: []string{}}
						unused[k] = t
					}
					t.Members = append(t.Members, d)
					t.Parts = append(t.Parts, part)
				}
			}
			for _, t := range unused {
				sort.Strings(t.Members)
				pi.Tiers = append(pi.Tiers, *t)
			}
		}
		sort.Slice(pi.Tiers, func(i, j int) bool {
			if pi.Tiers[i].Role != pi.Tiers[j].Role {
				return pi.Tiers[i].Role == ""
			}
			return pi.Tiers[i].Index < pi.Tiers[j].Index
		})

		// Volúmenes lógicos.
		rows := map[string]diskops.LVRow{}
		for _, lv := range lvs {
			if lv.VG == vg.Name {
				rows[lv.Name] = lv
			}
		}
		for _, lv := range lvs {
			if lv.VG != vg.Name || !lvVisible(lv.Name) {
				continue
			}
			if strings.HasSuffix(lv.Name, "_cache") {
				continue // volumen de caché todavía sin enganchar
			}
			x := agentproto.PoolLV{Name: lv.Name, UUID: lv.UUID, Size: lv.Size, FS: diskops.TagValue(lv.Tags, diskops.TagFS),
				Active: len(lv.Attr) > 4 && lv.Attr[4] == 'a', Encrypted: diskops.TagValue(lv.Tags, diskops.TagEnc) != "",
				KeyDevice: diskops.TagValue(lv.Tags, diskops.TagKey)}
			dev := "/dev/" + vg.Name + "/" + lv.Name
			if t, err := filepath.EvalSymlinks(dev); err == nil {
				x.DM = filepath.Base(t)
			}
			if x.Encrypted {
				if _, u := diskops.ReadFSInfo(dev); u != "" {
					x.LUKSUUID = u
				}
				x.Open = diskops.LUKSIsOpen(diskops.CryptName(lv.Name))
				if x.Open {
					_, x.FSUUID = diskops.ReadFSInfo("/dev/mapper/" + diskops.CryptName(lv.Name))
				}
			} else if x.Active {
				_, x.FSUUID = diskops.ReadFSInfo(dev)
			}
			if lv.Segtype == "cache" {
				c := &agentproto.CacheInfo{Mode: diskops.TagValue(lv.Tags, diskops.TagCache), Size: lv.CacheTotal * lv.ChunkSize,
					Used: lv.CacheUsed * lv.ChunkSize, Dirty: lv.CacheDirty * lv.ChunkSize, ReadHits: lv.ReadHits,
					ReadMisses: lv.ReadMisses, WriteHits: lv.WriteHits, WriteMisses: lv.WriteMisses, Disks: []string{}}
				if c.Mode == "" {
					c.Mode = map[string]string{"writeback": "readwrite"}[lv.CacheMode]
					if c.Mode == "" {
						c.Mode = "read"
					}
				}
				if cv, ok := rows["["+diskops.CacheLVName(lv.Name)+"_cvol]"]; ok {
					for _, pv := range cacheDevs(cv.Devices) {
						real, _ := filepath.EvalSymlinks(pv)
						c.Disks = append(c.Disks, pvByDev[filepath.Base(real)].Disks...)
					}
					if c.Size == 0 {
						c.Size = cv.Size
					}
				}
				x.Cache = c
				for i := range pi.Tiers {
					if pi.Tiers[i].Role == "cache" && len(c.Disks) > 0 && slices.Contains(c.Disks, firstOr(pi.Tiers[i].Members)) {
						pi.Tiers[i].Volume = lv.Name
					}
				}
			}
			pi.LVs = append(pi.LVs, x)
		}
		out.Pools = append(out.Pools, pi)
	}
	return out, nil
}

func firstOr(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

// cacheDevs lee el campo "devices" de lvs ("/dev/md125(0),/dev/sdx(12)").
func cacheDevs(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if i := strings.IndexByte(part, '('); i > 0 {
			part = part[:i]
		}
		if part = strings.TrimSpace(part); strings.HasPrefix(part, "/dev/") {
			out = append(out, part)
		}
	}
	return out
}

func (p *pools) findPool(ctx context.Context, name string) (*agentproto.PoolInfo, *agentproto.Error) {
	rep, err := p.report(ctx)
	if err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo leer LVM: %v", err)
	}
	for i := range rep.Pools {
		if rep.Pools[i].Name == name {
			return &rep.Pools[i], nil
		}
	}
	return nil, agentproto.Errf("POOL_NOT_FOUND", "no existe el grupo %s", name)
}

func (p *pools) findLV(ctx context.Context, name string) (*agentproto.PoolInfo, *agentproto.PoolLV, *agentproto.Error) {
	rep, err := p.report(ctx)
	if err != nil {
		return nil, nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo leer LVM: %v", err)
	}
	for i := range rep.Pools {
		for j := range rep.Pools[i].LVs {
			if rep.Pools[i].LVs[j].Name == name {
				return &rep.Pools[i], &rep.Pools[i].LVs[j], nil
			}
		}
	}
	return nil, nil, agentproto.Errf("VOLUME_NOT_FOUND", "no existe el volumen %s", name)
}

func dataPVs(pi *agentproto.PoolInfo) ([]string, uint64) {
	var out []string
	var free uint64
	for _, pv := range pi.PVs {
		if pv.Role != "cache" {
			out = append(out, pv.Dev)
			free += pv.Free
		}
	}
	return out, free
}

// poolFree verifica que el nombre no choque con otro grupo ni con un md.
func (p *pools) poolFree(ctx context.Context, name string) *agentproto.Error {
	if rep, err := p.report(ctx); err == nil {
		for _, pi := range rep.Pools {
			if pi.Name == name {
				return agentproto.Errf("POOL_EXISTS", "ya existe un grupo %s", name)
			}
		}
	}
	if _, err := os.Lstat("/dev/" + name); err == nil {
		return agentproto.Errf("POOL_EXISTS", "ya existe /dev/%s", name)
	}
	if _, err := os.Lstat("/dev/md/" + diskops.PoolMDName(name, 1)); err == nil {
		return agentproto.Errf("POOL_EXISTS", "ya existe un RAID %s", diskops.PoolMDName(name, 1))
	}
	if _, err := os.Lstat("/dev/md/" + name); err == nil {
		return agentproto.Errf("POOL_EXISTS", "%s ya es el nombre de un volumen", name)
	}
	return nil
}

// volumeNameFree verifica que el nombre no choque con ningún volumen (clásico o de grupo).
func (p *pools) volumeNameFree(ctx context.Context, name string) *agentproto.Error {
	if e := volumeFree(name); e != nil {
		return e
	}
	if _, _, e := p.findLV(ctx, name); e == nil {
		return agentproto.Errf(agentproto.CodeExists, "ya existe un volumen %s", name)
	}
	if rep, err := p.report(ctx); err == nil {
		for _, pi := range rep.Pools {
			if pi.Name == name {
				return agentproto.Errf(agentproto.CodeExists, "%s es el nombre de un grupo", name)
			}
		}
	}
	return nil
}

// ---- Crear un grupo ----------------------------------------------------------------------

func (p *pools) create(ctx context.Context, step stepFn, a *agentproto.PoolCreateArgs) (any, error) {
	devs, e := revalidate(p.paths, p.env, a.Disks)
	if e != nil {
		return nil, e
	}
	sizes := make([]uint64, len(devs))
	for i, d := range devs {
		snap, err := diskops.ReadDisk(d)
		if err != nil {
			return nil, fmt.Errorf("leer %s: %w", a.Disks[i].DevID, err)
		}
		sizes[i] = snap.Size
	}
	tiers, err := diskops.PoolLayout(a.Profile, sizes)
	if err != nil {
		return nil, agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	step(0.05, "Borrando firmas de los discos")
	for i, d := range devs {
		if err := diskops.Wipe(d, a.Disks[i].Fingerprint); err != nil {
			if errors.Is(err, diskops.ErrChanged) {
				return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "%s cambió desde el plan", a.Disks[i].DevID)
			}
			return nil, fmt.Errorf("borrar firmas de %s: %w", a.Disks[i].DevID, err)
		}
	}
	var created []string // md creados (para deshacer)
	var pvs []string
	undo := func() {
		bg := context.Background()
		_ = diskops.VGRemove(bg, a.Name)
		for _, pv := range pvs {
			_ = diskops.PVRemove(bg, pv)
		}
		for _, md := range created {
			if k, err := diskops.MDOf(md); err == nil {
				members := slavesOf(k)
				if err := diskops.StopMD(bg, k); err != nil {
					p.log.Warn("no se pudo detener el md al deshacer", "md", md, "err", err)
				}
				for _, m := range members {
					_ = diskops.ZeroSuperblock(bg, "/dev/"+m)
				}
			}
			_ = diskops.RemoveMDConfLine(md)
		}
		for _, d := range devs {
			_ = diskops.WipeMember(d)
		}
	}

	if diskops.IsHybrid(a.Profile) {
		step(0.12, "Particionando los discos en tramos")
		bounds := tierBounds(tiers)
		for i, d := range devs {
			parts := diskops.HybridParts(a.Name, bounds, diskops.HybridUsable(sizes[i]))
			if err := diskops.WritePartitions(ctx, d, parts); err != nil {
				undo()
				return nil, fmt.Errorf("particionar %s: %w", a.Disks[i].DevID, err)
			}
		}
	}
	for n, t := range tiers {
		if t.Level == "unused" {
			continue
		}
		var members []string
		for _, i := range t.Members {
			if diskops.IsHybrid(a.Profile) {
				members = append(members, diskops.PartDev(devs[i], t.Index))
			} else {
				members = append(members, devs[i])
			}
		}
		if t.Level == "basic" {
			pvs = append(pvs, members[0])
			continue
		}
		name := diskops.PoolMDName(a.Name, t.Index)
		step(0.2+0.4*float64(n)/float64(len(tiers)), fmt.Sprintf("Creando el RAID del tramo %d (%s)", t.Index, t.Level))
		md, err := diskops.CreateMD(ctx, name, diskops.RAIDLevel[t.Level], members)
		if err != nil {
			undo()
			return nil, fmt.Errorf("crear %s: %w", name, err)
		}
		created = append(created, name)
		if s, err := diskops.MDSuperOf(members[0]); err == nil {
			if err := diskops.WriteMDConf(name, s); err != nil {
				p.log.Warn("mdadm.conf no actualizado", "err", err)
			}
		}
		pvs = append(pvs, "/dev/"+md)
	}
	step(0.7, "Creando el grupo de almacenamiento (LVM)")
	for _, pv := range pvs {
		if err := diskops.PVCreate(ctx, pv); err != nil {
			undo()
			return nil, fmt.Errorf("pvcreate %s: %w", pv, err)
		}
	}
	if err := diskops.VGCreate(ctx, a.Name, a.Profile, pvs); err != nil {
		undo()
		return nil, fmt.Errorf("vgcreate: %w", err)
	}
	for _, pv := range pvs {
		if err := diskops.PVTag(ctx, pv, "data"); err != nil {
			undo()
			return nil, fmt.Errorf("etiquetar %s: %w", pv, err)
		}
	}
	pi, pe := p.findPool(ctx, a.Name)
	if pe != nil {
		return nil, pe
	}
	return agentproto.PoolCreateResult{Name: a.Name, UUID: pi.UUID, MDs: created, Bytes: pi.Size}, nil
}

// tierBounds son los límites acumulados de los tramos.
func tierBounds(tiers []diskops.Tier) []uint64 {
	out := make([]uint64, len(tiers))
	for i, t := range tiers {
		out[i] = t.Start + t.Size
	}
	return out
}

// hybridBounds lee los límites de un grupo híbrido de las particiones de sus discos:
// el disco con más tramos los tiene todos.
func hybridBounds(pool string, disks []string) ([]uint64, map[string]map[int]string) {
	byDisk := map[string]map[int]string{} // disco → tramo → partición
	var best []uint64
	for _, d := range disks {
		ents, err := diskops.ReadPartitions("/dev/" + d)
		if err != nil {
			continue
		}
		var b []uint64
		parts := map[int]string{}
		for _, e := range ents {
			pl, k, ok := diskops.ParseHybridPartName(e.Name)
			if !ok || pl != pool {
				continue
			}
			parts[k] = filepath.Base(diskops.PartDev("/dev/"+d, e.Index))
			for len(b) < k {
				b = append(b, 0)
			}
			b[k-1] = e.Start + e.Size - diskops.PartAlign
		}
		byDisk[d] = parts
		if len(b) > len(best) {
			best = b
		}
	}
	return best, byDisk
}

// ---- Ampliar un grupo ----------------------------------------------------------------------

func (p *pools) expand(ctx context.Context, step stepFn, a *agentproto.PoolExpandArgs) (any, error) {
	pi, e := p.findPool(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	before := pi.Size
	devs, e := revalidate(p.paths, p.env, a.Add)
	if e != nil {
		return nil, e
	}
	switch {
	case diskops.IsHybrid(pi.Profile):
		for i, d := range devs {
			if err := p.expandHybrid(ctx, step, pi, d, a.Add[i], float64(i)/float64(len(devs)), 1/float64(len(devs))); err != nil {
				return nil, err
			}
			if pi, e = p.findPool(ctx, a.Name); e != nil {
				return nil, e
			}
		}
	case pi.Profile == "raid5" || pi.Profile == "raid6" || pi.Profile == "raid10":
		if err := p.expandRAID(ctx, step, pi, devs, a.Add); err != nil {
			return nil, err
		}
	default:
		return nil, agentproto.Errf(agentproto.CodeNotSupported, "un grupo %s no se amplía agregando discos", pi.Profile)
	}
	after, e := p.findPool(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	return agentproto.PoolExpandResult{Before: before, After: after.Size}, nil
}

func (p *pools) expandRAID(ctx context.Context, step stepFn, pi *agentproto.PoolInfo, devs []string, add []agentproto.VolumeDisk) error {
	name := diskops.PoolMDName(pi.Name, 1)
	md, err := diskops.MDOf(name)
	if err != nil {
		return agentproto.Errf(agentproto.CodeNotFound, "%v", err)
	}
	in := diskops.ReadMD(md)
	if in.Degraded > 0 || in.Action != "idle" {
		return agentproto.Errf(agentproto.CodeBusy, "el RAID tiene que estar sano y sin tareas en curso (%s)", in.Action)
	}
	n := in.RaidDisks + len(devs)
	if pi.Profile == "raid10" && n%2 != 0 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "RAID 10 necesita una cantidad par de discos")
	}
	step(0.03, "Borrando firmas de los discos nuevos")
	for i, d := range devs {
		if err := diskops.Wipe(d, add[i].Fingerprint); err != nil {
			return fmt.Errorf("borrar firmas de %s: %w", add[i].DevID, err)
		}
	}
	for _, d := range devs {
		if err := diskops.MDManage(ctx, name, "add", d); err != nil {
			return fmt.Errorf("agregar al RAID: %w", err)
		}
	}
	if err := p.reshape(ctx, step, name, md, n, 0.05, 0.85); err != nil {
		return err
	}
	step(0.92, "Agrandando el grupo (pvresize)")
	return diskops.PVResize(ctx, "/dev/"+md)
}

// reshape lleva un md a n discos activos y espera a que termine (como md.grow).
func (p *pools) reshape(ctx context.Context, step stepFn, name, md string, n int, from, span float64) error {
	before := diskops.ReadMD(md).SizeBytes
	if err := diskops.MDGrow(ctx, name, n, ""); err != nil {
		if strings.Contains(strings.ToLower(execOutput(err)), "backup") {
			backup := filepath.Join("/var/lib/tango-agent", "md-grow-"+name+".bak")
			if err = diskops.MDGrow(ctx, name, n, backup); err == nil {
				defer os.Remove(backup)
			}
		}
		if err != nil {
			return fmt.Errorf("ampliar %s: %s", name, strings.TrimSpace(firstLine(execOutput(err), err)))
		}
	}
	seen := false
	start := time.Now()
	for {
		in := diskops.ReadMD(md)
		if in.Action == "reshape" {
			seen = true
			step(from+span*in.Progress, "Reorganizando "+name+" (reshape)")
		} else if in.Action == "idle" && (seen || in.RaidDisks == n) && in.SizeBytes > before {
			return nil
		} else if in.Action == "idle" && time.Since(start) > 2*time.Minute {
			return fmt.Errorf("el reshape de %s no avanzó", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (p *pools) expandHybrid(ctx context.Context, step stepFn, pi *agentproto.PoolInfo, dev string, vd agentproto.VolumeDisk, from, span float64) error {
	members := map[string]bool{}
	for _, pv := range pi.PVs {
		if pv.Role != "cache" {
			for _, d := range pv.Disks {
				members[d] = true
			}
		}
	}
	for _, t := range pi.Tiers {
		if t.Role == "" {
			for _, d := range t.Members {
				members[d] = true
			}
		}
	}
	var disks []string
	for d := range members {
		disks = append(disks, d)
	}
	sort.Strings(disks)
	bounds, byDisk := hybridBounds(pi.Name, disks)
	if len(bounds) == 0 {
		return errors.New("no se pudieron leer los tramos del grupo")
	}
	snap, err := diskops.ReadDisk(dev)
	if err != nil {
		return err
	}
	usable := diskops.HybridUsable(snap.Size)
	newBounds, err := diskops.HybridGrow(bounds, usable)
	if err != nil {
		return agentproto.Errf("DEVICE_TOO_SMALL", "%v", err)
	}
	step(from+span*0.02, "Borrando firmas del disco nuevo")
	if err := diskops.Wipe(dev, vd.Fingerprint); err != nil {
		return fmt.Errorf("borrar firmas: %w", err)
	}
	step(from+span*0.05, "Particionando el disco nuevo")
	if err := diskops.WritePartitions(ctx, dev, diskops.HybridParts(pi.Name, newBounds, usable)); err != nil {
		return fmt.Errorf("particionar: %w", err)
	}
	for k := 1; k <= len(newBounds); k++ {
		var old []string
		for _, d := range disks {
			if part, ok := byDisk[d][k]; ok {
				old = append(old, "/dev/"+part)
			}
		}
		newPart := diskops.PartDev(dev, k)
		count := len(old) + 1
		level := diskops.HybridLevel(pi.Profile, count)
		name := diskops.PoolMDName(pi.Name, k)
		f := from + span*(0.1+0.85*float64(k-1)/float64(len(newBounds)))
		md, mdErr := diskops.MDOf(name)
		switch {
		case mdErr == nil:
			in := diskops.ReadMD(md)
			if in.Degraded > 0 {
				return agentproto.Errf(agentproto.CodeBusy, "%s está degradado: reemplazá el disco antes de ampliar", name)
			}
			if in.Level == "raid1" && level == "raid5" {
				step(f, "Convirtiendo "+name+" de RAID 1 a RAID 5")
				if err := diskops.MDGrowLevel(ctx, name, "5"); err != nil {
					return fmt.Errorf("convertir %s: %w", name, err)
				}
			}
			if err := diskops.MDManage(ctx, name, "add", newPart); err != nil {
				return fmt.Errorf("agregar a %s: %w", name, err)
			}
			if err := p.reshape(ctx, step, name, md, count, f, span*0.85/float64(len(newBounds))); err != nil {
				return err
			}
			if err := diskops.PVResize(ctx, "/dev/"+md); err != nil {
				return fmt.Errorf("pvresize: %w", err)
			}
		case level != "unused":
			step(f, fmt.Sprintf("Creando el RAID del tramo %d (%s)", k, level))
			created, err := diskops.CreateMD(ctx, name, diskops.RAIDLevel[level], append(old, newPart))
			if err != nil {
				return fmt.Errorf("crear %s: %w", name, err)
			}
			if s, err := diskops.MDSuperOf(newPart); err == nil {
				_ = diskops.WriteMDConf(name, s)
			}
			if err := diskops.PVCreate(ctx, "/dev/"+created); err != nil {
				return fmt.Errorf("pvcreate: %w", err)
			}
			if err := diskops.VGExtend(ctx, pi.Name, "/dev/"+created); err != nil {
				return fmt.Errorf("vgextend: %w", err)
			}
			if err := diskops.PVTag(ctx, "/dev/"+created, "data"); err != nil {
				return fmt.Errorf("etiquetar: %w", err)
			}
		}
	}
	return nil
}

// ---- Reemplazar un disco -------------------------------------------------------------------

func (p *pools) replace(ctx context.Context, step stepFn, a *agentproto.PoolReplaceArgs) (any, error) {
	pi, e := p.findPool(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	if pi.Profile == "basic" {
		return nil, agentproto.Errf(agentproto.CodeNotSupported, "un grupo Básico no tiene redundancia para reconstruir")
	}
	if !diskops.IsHybrid(pi.Profile) {
		// Un solo md con discos enteros: el mismo flujo que md.replace.
		ma := &agentproto.MDReplaceArgs{OpID: a.OpID, Name: diskops.PoolMDName(pi.Name, 1), Failed: a.Failed, Replacement: a.Replacement}
		md, failedDev, e := replaceTargets(p.paths, p.env, ma)
		if e != nil {
			return nil, e
		}
		return replaceDisk(ctx, step, p.paths, p.env, ma, md, failedDev, p.log)
	}

	// Híbrido: un md por tramo.
	var tiersMD []agentproto.PoolTier
	for _, t := range pi.Tiers {
		if t.Role == "" && t.MD != "" {
			tiersMD = append(tiersMD, t)
		}
	}
	failedDisk := ""
	if a.Failed != "" {
		dev, pe := p.paths.Resolve(a.Failed)
		if pe != nil {
			return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "%s ya no está: %s", a.Failed, pe.Message)
		}
		failedDisk = diskops.DevName(dev)
	}
	// Tramos donde está (o estaba) el disco: los que lo tienen como miembro, o los degradados.
	need := 0
	for _, t := range tiersMD {
		in := diskops.ReadMD(t.MD)
		if (failedDisk != "" && slices.Contains(t.Members, failedDisk)) || (failedDisk == "" && in.Degraded > 0) {
			need = max(need, t.Index)
		}
	}
	if need == 0 {
		return nil, agentproto.Errf(agentproto.CodeInvalidArgs, "el grupo no está degradado: indicá qué disco reemplazar")
	}
	var disks []string
	for _, t := range pi.Tiers {
		disks = append(disks, t.Members...)
	}
	slices.Sort(disks)
	disks = slices.Compact(disks)
	bounds, _ := hybridBounds(pi.Name, disks)
	if len(bounds) < need {
		return nil, errors.New("no se pudieron leer los tramos del grupo")
	}
	devs, e := revalidate(p.paths, p.env, []agentproto.VolumeDisk{a.Replacement})
	if e != nil {
		return nil, e
	}
	snap, err := diskops.ReadDisk(devs[0])
	if err != nil {
		return nil, err
	}
	usable := diskops.HybridUsable(snap.Size)
	if usable < bounds[need-1] {
		return nil, agentproto.Errf("DEVICE_TOO_SMALL", "el disco nuevo tiene que tener al menos %d MB útiles", bounds[need-1]>>20)
	}
	if failedDisk != "" {
		step(0.05, "Sacando el disco de cada RAID")
		for _, t := range tiersMD {
			for _, part := range t.Parts {
				if diskOfPart(part) != failedDisk {
					continue
				}
				if !strings.Contains(diskops.ReadMD(t.MD).Members[failedDisk], "faulty") {
					_ = diskops.MDManage(ctx, t.Name, "fail", "/dev/"+part)
				}
				var rmErr error
				for range 20 {
					if rmErr = diskops.MDManage(ctx, t.Name, "remove", "/dev/"+part); rmErr == nil {
						break
					}
					time.Sleep(500 * time.Millisecond)
				}
				if rmErr != nil {
					return nil, fmt.Errorf("sacar %s de %s: %w", part, t.Name, rmErr)
				}
			}
		}
		if err := diskops.WipeMember("/dev/" + failedDisk); err != nil {
			p.log.Info("no se pudieron borrar las firmas del disco reemplazado", "err", err)
		}
	}
	step(0.12, "Particionando el disco nuevo")
	if err := diskops.Wipe(devs[0], a.Replacement.Fingerprint); err != nil {
		return nil, fmt.Errorf("borrar firmas: %w", err)
	}
	if err := diskops.WritePartitions(ctx, devs[0], diskops.HybridParts(pi.Name, bounds, usable)); err != nil {
		return nil, fmt.Errorf("particionar: %w", err)
	}
	for _, t := range tiersMD {
		if t.Index > need {
			continue
		}
		if diskops.ReadMD(t.MD).Degraded == 0 {
			continue
		}
		if err := diskops.MDManage(ctx, t.Name, "add", diskops.PartDev(devs[0], t.Index)); err != nil {
			return nil, fmt.Errorf("agregar a %s: %w", t.Name, err)
		}
	}
	// Esperar las reconstrucciones (md las hace de a una si comparten discos).
	idle := time.Now()
	for {
		pending, prog := 0, 0.0
		for _, t := range tiersMD {
			in := diskops.ReadMD(t.MD)
			if in.Degraded > 0 || in.Action == "recover" || in.Action == "resync" {
				pending++
				prog += in.Progress
				idle = time.Now()
			}
		}
		if pending == 0 {
			step(1, "Grupo sano")
			return agentproto.MDReplaceResult{Removed: a.Failed, Added: a.Replacement.DevID}, nil
		}
		done := float64(len(tiersMD) - pending)
		step(0.2+0.8*(done+prog/float64(pending))/float64(len(tiersMD)), "Reconstruyendo el grupo")
		if time.Since(idle) > 30*time.Second {
			return nil, errors.New("md no empezó a reconstruir")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// ---- Eliminar un grupo ---------------------------------------------------------------------

// poolDevs exige que los discos del plan sean exactamente los del grupo (datos y caché).
func (p *pools) poolDevs(pi *agentproto.PoolInfo, devIDs []string) ([]string, *agentproto.Error) {
	want := map[string]bool{}
	for _, t := range pi.Tiers {
		for _, d := range t.Members {
			want[d] = true
		}
	}
	for _, pv := range pi.PVs {
		for _, d := range pv.Disks {
			want[d] = true
		}
	}
	sys := p.env.SystemDisks()
	var devs []string
	got := map[string]bool{}
	for _, id := range devIDs {
		dev, e := p.paths.Resolve(id)
		if e != nil {
			return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "%s ya no está: %s", id, e.Message)
		}
		n := diskops.DevName(dev)
		if sys[n] {
			return nil, agentproto.Errf(agentproto.CodeNotEligible, "%s es el disco del sistema", id)
		}
		got[n] = true
		devs = append(devs, dev)
	}
	if len(got) != len(want) {
		return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "los discos del grupo %s no coinciden con el plan", pi.Name)
	}
	for d := range want {
		if !got[d] {
			return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "los discos del grupo %s no coinciden con el plan", pi.Name)
		}
	}
	return devs, nil
}

func (p *pools) delete(ctx context.Context, step stepFn, a *agentproto.PoolDeleteArgs) (any, error) {
	pi, e := p.findPool(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	if len(pi.LVs) > 0 {
		return nil, agentproto.Errf(agentproto.CodePoolNotEmpty, "el grupo %s todavía tiene volúmenes", a.Name)
	}
	devs, e := p.poolDevs(pi, a.DevIDs)
	if e != nil {
		return nil, e
	}
	step(0.1, "Borrando el grupo (LVM)")
	if err := diskops.VGRemove(ctx, a.Name); err != nil {
		return nil, fmt.Errorf("vgremove: %w", err)
	}
	for _, pv := range pi.PVs {
		if err := diskops.PVRemove(ctx, pv.Dev); err != nil {
			p.log.Warn("pvremove", "dev", pv.Dev, "err", err)
		}
	}
	step(0.4, "Deteniendo los RAID")
	if err := p.stopMDs(ctx, pi.Tiers); err != nil {
		return nil, err
	}
	step(0.8, "Borrando firmas de los discos")
	res := agentproto.VolumeDeleteResult{Wiped: []string{}}
	for i, d := range devs {
		if err := diskops.WipeMember(d); err != nil {
			return nil, fmt.Errorf("borrar firmas de %s: %w", a.DevIDs[i], err)
		}
		res.Wiped = append(res.Wiped, a.DevIDs[i])
	}
	return res, nil
}

// stopMDs detiene los md de estos tramos y borra sus superbloques.
func (p *pools) stopMDs(ctx context.Context, tiers []agentproto.PoolTier) error {
	for _, t := range tiers {
		if t.MD == "" {
			continue
		}
		members := t.Parts
		if len(members) == 0 {
			members = t.Members
		}
		if err := diskops.StopMD(ctx, t.MD); err != nil {
			return fmt.Errorf("detener %s: %w", t.Name, err)
		}
		for range 50 {
			if _, err := os.Stat("/sys/block/" + t.MD + "/md"); err != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		for _, m := range members {
			if err := diskops.ZeroSuperblock(ctx, "/dev/"+m); err != nil {
				return fmt.Errorf("borrar el superbloque de %s: %w", m, err)
			}
		}
		if err := diskops.RemoveMDConfLine(t.Name); err != nil {
			p.log.Warn("mdadm.conf no actualizado", "err", err)
		}
	}
	return nil
}

// ---- Volúmenes -----------------------------------------------------------------------------

func lvDev(pool, name string) string { return "/dev/" + pool + "/" + name }

func (p *pools) createLV(ctx context.Context, step stepFn, a *agentproto.LVCreateArgs) (any, error) {
	pi, e := p.findPool(ctx, a.Pool)
	if e != nil {
		return nil, e
	}
	if e := p.volumeNameFree(ctx, a.Name); e != nil {
		return nil, e
	}
	pvs, free := dataPVs(pi)
	size := a.Size
	if size == 0 {
		size = free
	}
	size = diskops.AlignExtent(size)
	if size > diskops.AlignExtent(free) || size == 0 {
		return nil, agentproto.Errf(agentproto.CodeNoSpace, "el grupo tiene %d MB libres", free>>20)
	}
	tags := []string{diskops.TagFS + a.FS}
	if a.Passphrase != "" {
		tags = append(tags, diskops.TagEnc+"luks2")
	}
	step(0.05, "Creando el volumen lógico")
	if err := diskops.LVCreate(ctx, a.Pool, a.Name, size, tags, pvs); err != nil {
		return nil, fmt.Errorf("lvcreate: %w", err)
	}
	dev := lvDev(a.Pool, a.Name)
	undo := func() {
		bg := context.Background()
		diskops.RemoveMount(bg, a.Name)
		if diskops.LUKSIsOpen(diskops.CryptName(a.Name)) {
			_ = diskops.LUKSClose(bg, diskops.CryptName(a.Name))
		}
		_ = diskops.LVRemove(bg, a.Pool, a.Name)
	}
	if err := diskops.WaitDev(ctx, dev); err != nil {
		undo()
		return nil, err
	}
	target := dev
	if a.Passphrase != "" {
		step(0.2, "Cifrando el volumen (LUKS2, argon2id)")
		if err := diskops.LUKSFormat(ctx, dev, a.Passphrase, "tg-"+a.Name); err != nil {
			undo()
			return nil, fmt.Errorf("luksFormat: %w", err)
		}
		if err := diskops.LUKSOpen(ctx, dev, diskops.CryptName(a.Name), []byte(a.Passphrase)); err != nil {
			undo()
			return nil, fmt.Errorf("abrir el volumen cifrado: %w", err)
		}
		target = "/dev/mapper/" + diskops.CryptName(a.Name)
		if err := diskops.WaitDev(ctx, target); err != nil {
			undo()
			return nil, err
		}
	}
	step(0.45, "Creando el sistema de archivos "+a.FS)
	uuid, err := diskops.Mkfs(ctx, a.FS, a.Name, target)
	if err != nil {
		undo()
		return nil, fmt.Errorf("mkfs: %w", err)
	}
	step(0.7, "Montando /"+a.Name)
	if err := diskops.InstallMountFS(ctx, a.Name, uuid, a.FS, a.Passphrase == ""); err != nil {
		undo()
		return nil, fmt.Errorf("montar: %w", err)
	}
	if a.FS == "btrfs" {
		step(0.85, "Creando subvolúmenes")
		for _, sv := range []string{"@shares", "@snapshots", "@apps"} {
			if err := diskops.CreateSubvolume("/"+a.Name, sv); err != nil && !errors.Is(err, os.ErrExist) {
				undo()
				return nil, fmt.Errorf("crear %s: %w", sv, err)
			}
		}
	}
	if a.KeyDevice != "" {
		step(0.92, "Guardando el archivo de clave en el dispositivo")
		if e := p.setKeyDevice(ctx, a.Pool, a.Name, a.Passphrase, a.KeyDevice); e != nil {
			// El volumen ya está listo y se abre con la frase: el archivo de clave se puede
			// volver a intentar desde la UI.
			p.log.Warn("archivo de clave", "volume", a.Name, "code", e.Code, "msg", e.Message)
		}
	}
	_, lv, _ := p.findLV(ctx, a.Name)
	res := agentproto.LVCreateResult{Name: a.Name, Mount: "/" + a.Name, FSUUID: uuid, Size: size}
	if lv != nil {
		res.UUID = lv.UUID
	}
	return res, nil
}

func (p *pools) resizeLV(ctx context.Context, step stepFn, a *agentproto.LVResizeArgs) (any, error) {
	pi, lv, e := p.findLV(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	size := diskops.AlignExtent(a.Size)
	if size <= lv.Size {
		return nil, agentproto.Errf(agentproto.CodeNotSupported, "solo se puede agrandar un volumen (hoy tiene %d MB)", lv.Size>>20)
	}
	pvs, free := dataPVs(pi)
	if size-lv.Size > free {
		return nil, agentproto.Errf(agentproto.CodeNoSpace, "el grupo tiene %d MB libres", free>>20)
	}
	if lv.Encrypted && !lv.Open {
		return nil, agentproto.Errf(agentproto.CodeLocked, "%s está bloqueado: desbloquealo para agrandarlo", a.Name)
	}
	if !diskops.IsMounted(a.Name) {
		return nil, agentproto.Errf(agentproto.CodeNotFound, "/%s no está montado", a.Name)
	}
	step(0.1, "Agrandando el volumen lógico")
	if err := diskops.LVExtend(ctx, pi.Name, a.Name, size, pvs); err != nil {
		if lv.Cache != nil {
			return nil, agentproto.Errf(agentproto.CodeNotSupported, "LVM no agranda este volumen con la caché puesta: quitá la caché, agrandalo y volvé a ponerla")
		}
		return nil, fmt.Errorf("lvextend: %w", err)
	}
	dev := lvDev(pi.Name, a.Name)
	if lv.Encrypted {
		step(0.4, "Agrandando el contenedor cifrado")
		if err := diskops.LUKSResize(ctx, diskops.CryptName(a.Name)); err != nil {
			return nil, fmt.Errorf("cryptsetup resize: %w", err)
		}
		dev = "/dev/mapper/" + diskops.CryptName(a.Name)
	}
	step(0.7, "Agrandando el sistema de archivos")
	if err := diskops.GrowFS(ctx, lv.FS, "/"+a.Name, dev); err != nil {
		return nil, fmt.Errorf("agrandar %s: %w", lv.FS, err)
	}
	res := agentproto.LVResizeResult{Size: size}
	res.FSSize, _ = fsSize("/" + a.Name)
	return res, nil
}

func (p *pools) deleteLV(ctx context.Context, step stepFn, a *agentproto.LVDeleteArgs) (any, error) {
	pi, lv, e := p.findLV(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	if lv.UUID != a.UUID {
		return nil, agentproto.Errf(agentproto.CodeDeviceChanged, "el volumen %s no es el que vio el plan", a.Name)
	}
	mount := "/" + a.Name
	step(0.05, "Despublicando las carpetas compartidas")
	if _, err := diskops.RemoveSambaSharesUnder(ctx, mount); err != nil {
		p.log.Warn("samba", "err", err)
	}
	if _, err := os.Stat(filepath.Join("/etc/systemd/system", diskops.MountUnitName(a.Name))); err == nil {
		step(0.2, "Desmontando "+mount)
		if err := diskops.UninstallMount(ctx, a.Name); err != nil {
			return nil, fmt.Errorf("desmontar: %w", err)
		}
	}
	if diskops.IsMounted(a.Name) {
		return nil, agentproto.Errf("VOLUME_BUSY", "%s sigue montado (algo lo está usando)", mount)
	}
	_ = os.Remove(mount)
	if lv.Encrypted && diskops.LUKSIsOpen(diskops.CryptName(a.Name)) {
		step(0.35, "Cerrando el volumen cifrado")
		if err := diskops.LUKSClose(ctx, diskops.CryptName(a.Name)); err != nil {
			return nil, fmt.Errorf("cerrar LUKS: %w", err)
		}
	}
	if lv.Cache != nil {
		step(0.5, "Quitando la caché SSD")
		if err := p.dropCache(ctx, pi, a.Name); err != nil {
			return nil, err
		}
	}
	step(0.8, "Borrando el volumen lógico")
	if err := diskops.LVRemove(ctx, pi.Name, a.Name); err != nil {
		return nil, fmt.Errorf("lvremove: %w", err)
	}
	return agentproto.VolumeDeleteResult{Wiped: []string{}}, nil
}

// ---- LUKS ------------------------------------------------------------------------------------

func luksErr(err error, log *slog.Logger) *agentproto.Error {
	if errors.Is(err, diskops.ErrWrongKey) {
		return agentproto.Errf(agentproto.CodeWrongPassphrase, "la frase no abre este volumen")
	}
	log.Warn("cryptsetup", "err", err, "output", execOutput(err))
	return agentproto.Errf(agentproto.CodeFailed, "cryptsetup falló: %v", err)
}

func (p *pools) encryptedLV(ctx context.Context, name string) (*agentproto.PoolInfo, *agentproto.PoolLV, *agentproto.Error) {
	pi, lv, e := p.findLV(ctx, name)
	if e != nil {
		return nil, nil, e
	}
	if !lv.Encrypted {
		return nil, nil, agentproto.Errf(agentproto.CodeNotSupported, "%s no está cifrado", name)
	}
	return pi, lv, nil
}

func (p *pools) unlock(ctx context.Context, a *agentproto.LUKSUnlockArgs) (any, *agentproto.Error) {
	p.luks.Lock()
	defer p.luks.Unlock()
	pi, lv, e := p.encryptedLV(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	if !lv.Open {
		key := []byte(a.Passphrase)
		if a.UseKeyDevice {
			if lv.KeyDevice == "" || lv.LUKSUUID == "" {
				return nil, agentproto.Errf(agentproto.CodeKeyDeviceMissing, "%s no tiene un dispositivo de clave configurado", a.Name)
			}
			k, e := p.readKeyFile(ctx, lv.KeyDevice, lv.LUKSUUID)
			if e != nil {
				return nil, e
			}
			key = k
		}
		if err := diskops.LUKSOpen(ctx, lvDev(pi.Name, a.Name), diskops.CryptName(a.Name), key); err != nil {
			return nil, luksErr(err, p.log)
		}
		if err := diskops.WaitDev(ctx, "/dev/mapper/"+diskops.CryptName(a.Name)); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "%v", err)
		}
	}
	if !diskops.IsMounted(a.Name) {
		if err := diskops.StartMount(ctx, a.Name); err != nil {
			return nil, agentproto.Errf(agentproto.CodeFailed, "se abrió pero no se pudo montar: %v", err)
		}
	}
	return struct{}{}, nil
}

func (p *pools) lock(ctx context.Context, a *agentproto.LUKSLockArgs) (any, *agentproto.Error) {
	p.luks.Lock()
	defer p.luks.Unlock()
	if _, _, e := p.encryptedLV(ctx, a.Name); e != nil {
		return nil, e
	}
	if diskops.IsMounted(a.Name) {
		if err := diskops.StopMount(ctx, a.Name); err != nil {
			return nil, agentproto.Errf(agentproto.CodeBusy, "no se pudo desmontar /%s: algo lo está usando", a.Name)
		}
	}
	if diskops.LUKSIsOpen(diskops.CryptName(a.Name)) {
		if err := diskops.LUKSClose(ctx, diskops.CryptName(a.Name)); err != nil {
			return nil, agentproto.Errf(agentproto.CodeBusy, "no se pudo cerrar el volumen cifrado: %v", err)
		}
	}
	return struct{}{}, nil
}

func (p *pools) passphrase(ctx context.Context, a *agentproto.LUKSPassphraseArgs) (any, *agentproto.Error) {
	p.luks.Lock()
	defer p.luks.Unlock()
	pi, _, e := p.encryptedLV(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	if err := diskops.LUKSChangeKey(ctx, lvDev(pi.Name, a.Name), a.Old, a.New); err != nil {
		return nil, luksErr(err, p.log)
	}
	return struct{}{}, nil
}

func (p *pools) keyDevice(ctx context.Context, a *agentproto.LUKSKeyDeviceArgs) (any, *agentproto.Error) {
	p.luks.Lock()
	defer p.luks.Unlock()
	pi, lv, e := p.encryptedLV(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	if a.Device == "" {
		if slot := lvSlot(ctx, pi.Name, a.Name); slot > 0 {
			if err := diskops.LUKSKillSlot(ctx, lvDev(pi.Name, a.Name), slot, []byte(a.Passphrase)); err != nil {
				return nil, luksErr(err, p.log)
			}
		}
		_ = diskops.LVTags(ctx, pi.Name, a.Name, nil, keyTags(ctx, pi.Name, a.Name))
		return struct{}{}, nil
	}
	_ = lv
	if e := p.setKeyDevice(ctx, pi.Name, a.Name, a.Passphrase, a.Device); e != nil {
		return nil, e
	}
	return struct{}{}, nil
}

// keyTags son las etiquetas tg.key= y tg.slot= actuales de un volumen.
func keyTags(ctx context.Context, vg, lv string) []string {
	_, _, lvs, err := diskops.LVMReport(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range lvs {
		if r.VG == vg && r.Name == lv {
			for _, t := range r.Tags {
				if strings.HasPrefix(t, diskops.TagKey) || strings.HasPrefix(t, diskops.TagKeySlot) {
					out = append(out, t)
				}
			}
		}
	}
	return out
}

func lvSlot(ctx context.Context, vg, lv string) int {
	for _, t := range keyTags(ctx, vg, lv) {
		if v, ok := strings.CutPrefix(t, diskops.TagKeySlot); ok {
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	return 0
}

// keyFileName es la ruta del archivo de clave de un volumen dentro del dispositivo.
func keyFileName(luksUUID string) string {
	return filepath.Join(diskops.KeyMountDir, "tangoos-keys", luksUUID+".key")
}

func validLUKSUUID(u string) bool {
	if len(u) != 36 {
		return false
	}
	for _, r := range u {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r == '-') {
			return false
		}
	}
	return true
}

// setKeyDevice escribe un archivo de clave nuevo en el dispositivo y lo agrega como ranura LUKS.
func (p *pools) setKeyDevice(ctx context.Context, vg, name, passphrase, device string) *agentproto.Error {
	dev := lvDev(vg, name)
	_, luksUUID := diskops.ReadFSInfo(dev)
	if !validLUKSUUID(luksUUID) {
		return agentproto.Errf(agentproto.CodeFailed, "no se pudo leer el encabezado LUKS de %s", name)
	}
	kd := p.keyDeviceByUUID(device)
	if kd == nil {
		return agentproto.Errf(agentproto.CodeKeyDeviceMissing, "no está conectado el dispositivo %s", device)
	}
	key := make([]byte, 4096)
	if _, err := rand.Read(key); err != nil {
		return agentproto.Errf(agentproto.CodeInternal, "sin aleatoriedad")
	}
	if err := diskops.MountKeyDevice(ctx, device, kd.FS, true); err != nil {
		return agentproto.Errf(agentproto.CodeFailed, "no se pudo montar el dispositivo: %v", err)
	}
	path := keyFileName(luksUUID)
	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err == nil {
		err = writeSynced(path, key)
	}
	diskops.UnmountKeyDevice(context.Background())
	if err != nil {
		return agentproto.Errf(agentproto.CodeFailed, "no se pudo escribir el archivo de clave: %v", err)
	}
	// Una ranura vieja de otro archivo de clave se reemplaza.
	if old := lvSlot(ctx, vg, name); old > 0 {
		if err := diskops.LUKSKillSlot(ctx, dev, old, []byte(passphrase)); err != nil && !errors.Is(err, diskops.ErrWrongKey) {
			p.log.Warn("no se pudo borrar la ranura vieja", "slot", old, "err", err)
		} else if errors.Is(err, diskops.ErrWrongKey) {
			return luksErr(err, p.log)
		}
	}
	slot, err := diskops.LUKSAddKey(ctx, dev, []byte(passphrase), key)
	if err != nil {
		return luksErr(err, p.log)
	}
	_ = diskops.LVTags(ctx, vg, name, []string{diskops.TagKey + device, diskops.TagKeySlot + strconv.Itoa(slot)}, keyTags(ctx, vg, name))
	return nil
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (p *pools) readKeyFile(ctx context.Context, device, luksUUID string) ([]byte, *agentproto.Error) {
	if !validLUKSUUID(luksUUID) {
		return nil, agentproto.Errf(agentproto.CodeFailed, "encabezado LUKS ilegible")
	}
	kd := p.keyDeviceByUUID(device)
	if kd == nil {
		return nil, agentproto.Errf(agentproto.CodeKeyDeviceMissing, "conectá el dispositivo con el archivo de clave")
	}
	if err := diskops.MountKeyDevice(ctx, device, kd.FS, false); err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo montar el dispositivo: %v", err)
	}
	defer diskops.UnmountKeyDevice(context.Background())
	key, err := os.ReadFile(keyFileName(luksUUID))
	if err != nil || len(key) == 0 {
		return nil, agentproto.Errf(agentproto.CodeKeyDeviceMissing, "el dispositivo no tiene el archivo de clave de este volumen")
	}
	return key, nil
}

// keyDevices lista los filesystems vfat, exFAT o ext4 que pueden guardar un archivo de
// clave: fuera del sistema, de los RAID y de LVM, y sin montar.
func (p *pools) keyDevices() []agentproto.KeyDevice {
	sys := p.env.SystemDisks()
	mounted := p.env.MountedDisks()
	out := []agentproto.KeyDevice{}
	ents, _ := os.ReadDir("/dev/disk/by-uuid")
	for _, e := range ents {
		real, err := filepath.EvalSymlinks(filepath.Join("/dev/disk/by-uuid", e.Name()))
		if err != nil {
			continue
		}
		dev := filepath.Base(real)
		if strings.HasPrefix(dev, "dm-") || strings.HasPrefix(dev, "md") || strings.HasPrefix(dev, "loop") || strings.HasPrefix(dev, "sr") {
			continue
		}
		disk := diskOfPart(dev)
		if sys[disk] || mounted[disk] || p.env.HasHolders(disk) {
			continue
		}
		fs, uuid := diskops.ReadFSInfo(real)
		if fs != "vfat" && fs != "exfat" && fs != "ext4" || uuid == "" {
			continue
		}
		kd := agentproto.KeyDevice{UUID: e.Name(), FS: fs, Size: blockBytes(dev), Disk: disk, Dev: dev}
		kd.Label = diskops.FSLabel(real, fs)
		if b, _ := os.ReadFile(filepath.Join("/sys/block", disk, "removable")); strings.TrimSpace(string(b)) == "1" {
			kd.USB = true
		}
		if l, err := os.Readlink(filepath.Join("/sys/block", disk)); err == nil && strings.Contains(l, "/usb") {
			kd.USB = true
		}
		out = append(out, kd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dev < out[j].Dev })
	return out
}

func (p *pools) keyDeviceByUUID(uuid string) *agentproto.KeyDevice {
	for _, kd := range p.keyDevices() {
		if strings.EqualFold(kd.UUID, uuid) {
			return &kd
		}
	}
	return nil
}

// ---- Caché SSD -------------------------------------------------------------------------------

func (p *pools) createCache(ctx context.Context, step stepFn, a *agentproto.CacheCreateArgs) (any, error) {
	pi, lv, e := p.findLV(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	if lv.Cache != nil {
		return nil, agentproto.Errf("CACHE_EXISTS", "%s ya tiene caché SSD", a.Name)
	}
	devs, e := revalidate(p.paths, p.env, a.Disks)
	if e != nil {
		return nil, e
	}
	step(0.05, "Borrando firmas de las SSD")
	for i, d := range devs {
		if err := diskops.Wipe(d, a.Disks[i].Fingerprint); err != nil {
			return nil, fmt.Errorf("borrar firmas de %s: %w", a.Disks[i].DevID, err)
		}
	}
	pv, mdName := devs[0], ""
	if len(devs) == 2 {
		k := 1
		for {
			if _, err := os.Lstat("/dev/md/" + diskops.PoolCacheMDName(pi.Name, k)); err != nil {
				break
			}
			k++
		}
		mdName = diskops.PoolCacheMDName(pi.Name, k)
		step(0.15, "Creando el RAID 1 de las SSD")
		md, err := diskops.CreateMD(ctx, mdName, "1", devs)
		if err != nil {
			return nil, fmt.Errorf("crear %s: %w", mdName, err)
		}
		if s, err := diskops.MDSuperOf(devs[0]); err == nil {
			_ = diskops.WriteMDConf(mdName, s)
		}
		pv = "/dev/" + md
	}
	undo := func() {
		bg := context.Background()
		_ = diskops.VGReduce(bg, pi.Name, pv)
		_ = diskops.PVRemove(bg, pv)
		if mdName != "" {
			if k, err := diskops.MDOf(mdName); err == nil {
				_ = diskops.StopMD(bg, k)
			}
			_ = diskops.RemoveMDConfLine(mdName)
		}
		for _, d := range devs {
			_ = diskops.WipeMember(d)
		}
	}
	step(0.35, "Sumando las SSD al grupo")
	if err := diskops.PVCreate(ctx, pv); err != nil {
		undo()
		return nil, fmt.Errorf("pvcreate: %w", err)
	}
	if err := diskops.VGExtend(ctx, pi.Name, pv); err != nil {
		undo()
		return nil, fmt.Errorf("vgextend: %w", err)
	}
	if err := diskops.PVTag(ctx, pv, "cache"); err != nil {
		undo()
		return nil, fmt.Errorf("etiquetar: %w", err)
	}
	step(0.6, "Enganchando la caché al volumen")
	if err := diskops.CacheAttach(ctx, pi.Name, a.Name, pv, a.Mode); err != nil {
		undo()
		return nil, fmt.Errorf("lvmcache: %w", err)
	}
	_ = diskops.LVTags(ctx, pi.Name, a.Name, []string{diskops.TagCache + a.Mode}, nil)
	return struct{}{}, nil
}

// dropCache saca la caché de un volumen y libera sus SSD.
func (p *pools) dropCache(ctx context.Context, pi *agentproto.PoolInfo, name string) error {
	var cachePVs []agentproto.PoolPV
	var cacheTiers []agentproto.PoolTier
	_, lv, e := p.findLV(ctx, name)
	if e != nil {
		return e
	}
	if lv.Cache == nil {
		return nil
	}
	for _, pv := range pi.PVs {
		if pv.Role == "cache" && len(pv.Disks) > 0 && slices.Contains(lv.Cache.Disks, pv.Disks[0]) {
			cachePVs = append(cachePVs, pv)
		}
	}
	for _, t := range pi.Tiers {
		if t.Role == "cache" && t.MD != "" && len(t.Members) > 0 && slices.Contains(lv.Cache.Disks, t.Members[0]) {
			cacheTiers = append(cacheTiers, t)
		}
	}
	if err := diskops.CacheDetach(ctx, pi.Name, name); err != nil {
		return fmt.Errorf("vaciar y quitar la caché: %w", err)
	}
	if mode := lv.Cache.Mode; mode != "" {
		_ = diskops.LVTags(ctx, pi.Name, name, nil, []string{diskops.TagCache + mode})
	}
	for _, pv := range cachePVs {
		if err := diskops.VGReduce(ctx, pi.Name, pv.Dev); err != nil {
			return fmt.Errorf("vgreduce: %w", err)
		}
		_ = diskops.PVRemove(ctx, pv.Dev)
	}
	if err := p.stopMDs(ctx, cacheTiers); err != nil {
		return err
	}
	for _, pv := range cachePVs {
		for _, d := range pv.Disks {
			if err := diskops.WipeMember("/dev/" + d); err != nil {
				p.log.Warn("borrar firmas de la SSD", "disk", d, "err", err)
			}
		}
	}
	return nil
}

func (p *pools) removeCache(ctx context.Context, step stepFn, a *agentproto.CacheRemoveArgs) (any, error) {
	pi, _, e := p.findLV(ctx, a.Name)
	if e != nil {
		return nil, e
	}
	step(0.1, "Vaciando la caché (los bloques sucios vuelven a los discos)")
	if err := p.dropCache(ctx, pi, a.Name); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}
