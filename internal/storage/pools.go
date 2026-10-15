package storage

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Grupos de almacenamiento (protocol/README.md §16, ADR 0011). tangod no corre LVM (no
// es root ni ejecuta binarios): la estructura (VG, PV, LV, caché) la da el agente con
// pool.report y el estado de los md sale de sysfs, como el resto del inventario.

// PoolSource es quien sabe de LVM (tango-agent). nil = sin grupos.
type PoolSource interface {
	PoolReport(ctx context.Context) (*agentproto.PoolReport, error)
}

// Pool es exactamente el Pool de TS.
type Pool struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Profile    string     `json:"profile"`
	State      string     `json:"state"` // healthy | degraded | building | readonly
	Size       uint64     `json:"size"`
	Allocated  uint64     `json:"allocated"`
	Free       uint64     `json:"free"`
	Disks      []string   `json:"disks"`
	CacheDisks []string   `json:"cacheDisks"`
	Tiers      []PoolTier `json:"tiers"`
	Volumes    []string   `json:"volumes"`
	Task       string     `json:"task,omitempty"`
	Progress   *float64   `json:"progress,omitempty"`
}

// PoolTier es exactamente el PoolTier de TS.
type PoolTier struct {
	Index    int      `json:"index"`
	Size     uint64   `json:"size"`
	Members  []string `json:"members"`
	Level    string   `json:"level"`
	Usable   uint64   `json:"usable"`
	State    string   `json:"state,omitempty"`
	Progress *float64 `json:"progress,omitempty"`
	Role     string   `json:"role,omitempty"`
}

// VolumeCache es la caché SSD de un volumen (Volume.cache).
type VolumeCache struct {
	Mode        string   `json:"mode"`
	Disks       []string `json:"disks"`
	Size        uint64   `json:"size"`
	Used        uint64   `json:"used"`
	Dirty       uint64   `json:"dirty"`
	ReadHits    uint64   `json:"readHits"`
	ReadMisses  uint64   `json:"readMisses"`
	WriteHits   uint64   `json:"writeHits"`
	WriteMisses uint64   `json:"writeMisses"`
	HitRatio    *float64 `json:"hitRatio"`
}

// poolCache guarda el último pool.report unos segundos (el monitor y la UI preguntan seguido).
type poolCache struct {
	mu  sync.Mutex
	at  time.Time
	rep *agentproto.PoolReport
}

const poolTTL = 3 * time.Second

// InvalidatePools descarta el reporte en caché (después de una operación).
func (inv *Inventory) InvalidatePools() {
	inv.pc.mu.Lock()
	inv.pc.rep = nil
	inv.pc.mu.Unlock()
}

// PoolReport devuelve el reporte de LVM (en caché por unos segundos). Sin agente, vacío.
func (inv *Inventory) PoolReport(ctx context.Context) *agentproto.PoolReport {
	if inv.PoolAgent == nil {
		return &agentproto.PoolReport{}
	}
	inv.pc.mu.Lock()
	defer inv.pc.mu.Unlock()
	if inv.pc.rep != nil && time.Since(inv.pc.at) < poolTTL {
		return inv.pc.rep
	}
	rep, err := inv.PoolAgent.PoolReport(ctx)
	if err != nil || rep == nil {
		if inv.pc.rep != nil {
			return inv.pc.rep // el último conocido es mejor que "no hay grupos"
		}
		return &agentproto.PoolReport{}
	}
	inv.pc.rep, inv.pc.at = rep, time.Now()
	return rep
}

// mdState lee el estado de un md de sysfs como un Volume.
func (inv *Inventory) mdState(md string) (state, task string, prog *float64) {
	dir := filepath.Join(inv.Sys, "block", md, "md")
	state = "healthy"
	if readUint(filepath.Join(dir, "degraded")) > 0 {
		state = "degraded"
	}
	action := readTrim(filepath.Join(dir, "sync_action"))
	switch action {
	case "resync", "recover", "reshape":
		state, task = "building", mdTasks[action]
		prog = progress(readTrim(filepath.Join(dir, "sync_completed")))
	case "check", "repair":
		if state == "healthy" {
			state = "scrubbing"
		}
		prog = progress(readTrim(filepath.Join(dir, "sync_completed")))
	}
	if strings.Contains(readTrim(filepath.Join(dir, "array_state")), "read-only") {
		state = "readonly"
	}
	return
}

var stateRank = map[string]int{"healthy": 0, "scrubbing": 1, "building": 2, "degraded": 3, "readonly": 4}

// poolsRaw arma los grupos con nombres del kernel (sdb) en Disks/Members.
func (inv *Inventory) poolsRaw(ctx context.Context) []Pool {
	rep := inv.PoolReport(ctx)
	out := []Pool{}
	for _, p := range rep.Pools {
		pl := Pool{ID: p.UUID, Name: p.Name, Profile: p.Profile, State: "healthy", Disks: []string{}, CacheDisks: []string{},
			Tiers: []PoolTier{}, Volumes: []string{}}
		data, cache := map[string]bool{}, map[string]bool{}
		for _, pv := range p.PVs {
			if pv.Role == "cache" {
				for _, d := range pv.Disks {
					cache[d] = true
				}
				continue
			}
			pl.Size += pv.Size
			pl.Free += pv.Free
			for _, d := range pv.Disks {
				data[d] = true
			}
		}
		pl.Allocated = pl.Size - pl.Free
		for _, t := range p.Tiers {
			if t.Role == "cache" {
				continue
			}
			for _, d := range t.Members {
				data[d] = true
			}
			pt := PoolTier{Index: t.Index, Size: t.Size, Members: append([]string{}, t.Members...), Level: t.Level}
			if t.Level == "unused" {
				pt.Level = "unused"
			} else {
				pt.Usable = diskopsUsable(t.Level, len(t.Members), t.Size)
			}
			if t.MD != "" {
				st, task, prog := inv.mdState(t.MD)
				pt.State, pt.Progress = st, prog
				if stateRank[st] > stateRank[pl.State] {
					pl.State = st
				}
				if task != "" && pl.Task == "" {
					pl.Task, pl.Progress = task, prog
				}
			}
			pl.Tiers = append(pl.Tiers, pt)
		}
		for d := range data {
			pl.Disks = append(pl.Disks, d)
		}
		for d := range cache {
			pl.CacheDisks = append(pl.CacheDisks, d)
		}
		sort.Strings(pl.Disks)
		sort.Strings(pl.CacheDisks)
		for _, lv := range p.LVs {
			pl.Volumes = append(pl.Volumes, lv.UUID)
		}
		out = append(out, pl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func diskopsUsable(level string, n int, size uint64) uint64 {
	switch level {
	case "basic", "linear", "raid0":
		return size * uint64(max(n, 1))
	case "raid1":
		return size
	case "raid5":
		return uint64(n-1) * size
	case "raid6":
		return uint64(n-2) * size
	case "raid10":
		return uint64(n/2) * size
	}
	return 0
}

// Pools lista los grupos de almacenamiento con los mismos ids de disco que Disk.ID.
func (inv *Inventory) Pools(ctx context.Context) ([]Pool, error) {
	pools := inv.poolsRaw(ctx)
	ids := inv.stableIDs()
	for i := range pools {
		pools[i].Disks = mapIDs(pools[i].Disks, ids)
		pools[i].CacheDisks = mapIDs(pools[i].CacheDisks, ids)
		for j := range pools[i].Tiers {
			pools[i].Tiers[j].Members = mapIDs(pools[i].Tiers[j].Members, ids)
		}
	}
	return pools, nil
}

func mapIDs(names []string, ids func(string) string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = ids(n)
	}
	return out
}

// stableIDs devuelve la función disco del kernel → Disk.ID.
func (inv *Inventory) stableIDs() func(string) string {
	byID, byPath := inv.links("by-id"), inv.links("by-path")
	return func(name string) string {
		base := filepath.Join(inv.Sys, "block", name)
		return stableID(name, inv.model(base), inv.serial(base), byID[name], byPath[name])
	}
}

// poolVolumes arma los volúmenes de los grupos (nombres del kernel en Disks).
func (inv *Inventory) poolVolumes(ctx context.Context, mounts []mountEntry) []Volume {
	rep := inv.PoolReport(ctx)
	raw := inv.poolsRaw(ctx)
	byName := map[string]Pool{}
	for _, p := range raw {
		byName[p.Name] = p
	}
	mounted := map[string]mountEntry{}
	for _, m := range mounts {
		mounted[m.point] = m
	}
	var out []Volume
	for _, p := range rep.Pools {
		pl := byName[p.Name]
		for _, lv := range p.LVs {
			v := Volume{ID: lv.UUID, Name: lv.Name, Kind: "pool", Pool: p.UUID, Profile: p.Profile, FS: lv.FS,
				Size: lv.Size, State: pl.State, Disks: append([]string{}, pl.Disks...), Growth30d: []float64{},
				Encrypted: lv.Encrypted, KeyDevice: lv.KeyDevice}
			if v.FS == "" {
				v.FS = "btrfs"
			}
			if pl.Task != "" {
				v.Task, v.Progress = pl.Task, pl.Progress
			}
			if m, ok := mounted["/"+lv.Name]; ok {
				v.Mount = m.point
				v.Size, v.Used = statfs(m.point, lv.Size)
			}
			if lv.Encrypted && !lv.Open {
				v.Locked, v.State, v.Task, v.Progress = true, "locked", "", nil
			}
			if c := lv.Cache; c != nil {
				vc := &VolumeCache{Mode: c.Mode, Disks: append([]string{}, c.Disks...), Size: c.Size, Used: c.Used, Dirty: c.Dirty,
					ReadHits: c.ReadHits, ReadMisses: c.ReadMisses, WriteHits: c.WriteHits, WriteMisses: c.WriteMisses}
				if tot := c.ReadHits + c.ReadMisses; tot > 0 {
					r := float64(c.ReadHits) / float64(tot)
					vc.HitRatio = &r
				}
				v.Cache = vc
			}
			out = append(out, v)
		}
	}
	return out
}

// poolDisks mapea disco del kernel → (grupo, rol) para storage.disks.list.
func (inv *Inventory) poolDisks(ctx context.Context) map[string][2]string {
	out := map[string][2]string{}
	for _, p := range inv.poolsRaw(ctx) {
		for _, d := range p.Disks {
			out[d] = [2]string{p.ID, "data"}
		}
		for _, d := range p.CacheDisks {
			out[d] = [2]string{p.ID, "cache"}
		}
	}
	return out
}

// isPoolMD indica si un md es parte de un grupo (sus volúmenes se listan aparte).
func (inv *Inventory) isPoolMD(md string) bool {
	if _, _, _, ok := diskops.ParsePoolMDName(inv.mdName(md)); ok {
		return true
	}
	hs, _ := readDir(filepath.Join(inv.Sys, "block", md, "holders"))
	for _, h := range hs {
		if strings.HasPrefix(h, "dm-") {
			return true
		}
	}
	return false
}

func readDir(p string) ([]string, error) {
	ents, err := os.ReadDir(p)
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out, err
}
