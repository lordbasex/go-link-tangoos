package storage

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/zfs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ZFS (protocol/README.md §18, ADR 0013). Como en los grupos LVM, tangod no corre
// zpool/zfs: la estructura la da el agente (zfs.report) y acá se traduce a los tipos de
// la UI. Un volumen ZFS de TangoOS es el dataset <pool>/<volumen> montado en
// /<volumen>; sus carpetas son hijos de <pool>/<volumen>/shares.

// ZfsSource es quien sabe de ZFS (tango-agent). nil = sin ZFS.
type ZfsSource interface {
	ZfsReport(ctx context.Context, importable bool) (*agentproto.ZfsReport, error)
}

type zfsCache struct {
	mu  sync.Mutex
	at  time.Time
	rep *agentproto.ZfsReport
}

// ZfsPool es exactamente el ZfsPool de TS.
type ZfsPool struct {
	ID        string     `json:"id"` // "zfs:<nombre>"
	Name      string     `json:"name"`
	GUID      string     `json:"guid"`
	Health    string     `json:"health"`
	State     string     `json:"state"` // healthy | degraded | faulted | readonly
	ReadOnly  bool       `json:"readonly"`
	Layout    string     `json:"layout"` // stripe | mirror | raidz1 | raidz2 | raidz3 | mixed
	Size      uint64     `json:"size"`   // útil (used + avail del dataset raíz)
	Allocated uint64     `json:"allocated"`
	Free      uint64     `json:"free"`
	Raw       uint64     `json:"raw"` // zpool list size (con paridad)
	Frag      *int       `json:"frag"`
	Vdevs     []ZfsVdev  `json:"vdevs"`
	Logs      []ZfsVdev  `json:"logs"`
	Cache     []ZfsLeaf  `json:"cache"`
	Scan      *ZfsScan   `json:"scan"`
	Errors    string     `json:"errors,omitempty"`
	Volumes   []string   `json:"volumes"` // Volume.id
	Zvols     []ZfsZvol  `json:"zvols"`
	Encrypted []string   `json:"encrypted"` // volúmenes cifrados (Volume.id)
	Disks     []string   `json:"disks"`     // Disk.id de datos
	Other     []ZfsOther `json:"other"`     // datasets fuera del layout de TangoOS (pools importados)
}

type ZfsVdev struct {
	Name  string    `json:"name"`
	Type  string    `json:"type"`
	State string    `json:"state"`
	Read  uint64    `json:"read"`
	Write uint64    `json:"write"`
	Cksum uint64    `json:"cksum"`
	Size  uint64    `json:"size"`
	Disks []ZfsLeaf `json:"disks"`
}

type ZfsLeaf struct {
	ID          string `json:"id"` // Disk.id, "" si el disco no está
	Name        string `json:"name"`
	GUID        string `json:"guid"`
	State       string `json:"state"`
	Read        uint64 `json:"read"`
	Write       uint64 `json:"write"`
	Cksum       uint64 `json:"cksum"`
	Resilvering bool   `json:"resilvering,omitempty"`
}

type ZfsScan struct {
	Function string  `json:"function"`
	State    string  `json:"state"`
	Progress float64 `json:"progress"`
	Start    string  `json:"start,omitempty"`
	End      string  `json:"end,omitempty"`
	Errors   uint64  `json:"errors"`
}

type ZfsZvol struct {
	Name   string `json:"name"`
	Size   uint64 `json:"size"`
	Used   uint64 `json:"used"`
	Device string `json:"device"`
}

type ZfsOther struct {
	Name       string `json:"name"`
	Mountpoint string `json:"mountpoint"`
	Mounted    bool   `json:"mounted"`
	Used       uint64 `json:"used"`
}

// ZfsImportable es un pool que se ve en los discos sin importar (UI: "Importar").
type ZfsImportable struct {
	Name   string   `json:"name"`
	GUID   string   `json:"guid"`
	State  string   `json:"state"`
	Status string   `json:"status,omitempty"`
	Disks  []string `json:"disks"`
}

// InvalidateZfs descarta el reporte en caché (después de una operación).
func (inv *Inventory) InvalidateZfs() {
	inv.zc.mu.Lock()
	inv.zc.rep = nil
	inv.zc.mu.Unlock()
}

// ZfsReport devuelve el último zfs.report (vacío sin agente o sin ZFS).
func (inv *Inventory) ZfsReport(ctx context.Context) *agentproto.ZfsReport {
	empty := &agentproto.ZfsReport{Pools: []agentproto.ZfsPoolInfo{}, Importable: []agentproto.ZfsImportable{}}
	if inv.ZfsAgent == nil {
		return empty
	}
	inv.zc.mu.Lock()
	defer inv.zc.mu.Unlock()
	if inv.zc.rep != nil && time.Since(inv.zc.at) < poolTTL {
		return inv.zc.rep
	}
	rep, err := inv.ZfsAgent.ZfsReport(ctx, false)
	if err != nil || rep == nil {
		if inv.zc.rep != nil {
			return inv.zc.rep
		}
		return empty
	}
	inv.zc.rep, inv.zc.at = rep, time.Now()
	return rep
}

// ZfsImportables busca pools sin importar (lee todos los discos: solo a pedido).
func (inv *Inventory) ZfsImportables(ctx context.Context) []ZfsImportable {
	out := []ZfsImportable{}
	if inv.ZfsAgent == nil {
		return out
	}
	rep, err := inv.ZfsAgent.ZfsReport(ctx, true)
	if err != nil {
		return out
	}
	ids := inv.stableIDs()
	for _, im := range rep.Importable {
		z := ZfsImportable{Name: im.Name, GUID: im.GUID, State: im.State, Status: im.Status, Disks: []string{}}
		for _, d := range im.Disks {
			z.Disks = append(z.Disks, ids(d))
		}
		out = append(out, z)
	}
	return out
}

// ZfsVolumeID es el id estable de un volumen ZFS.
func ZfsVolumeID(guid string) string { return "zfs-" + guid }

// ZfsPoolID es el id de un pool en la UI.
func ZfsPoolID(name string) string { return "zfs:" + name }

func zfsState(p agentproto.ZfsPoolInfo) string {
	switch {
	case p.Health == "DEGRADED":
		return "degraded"
	case p.Health != "ONLINE":
		return "faulted"
	case p.ReadOnly:
		return "readonly"
	}
	return "healthy"
}

func zfsLayout(p agentproto.ZfsPoolInfo) string {
	l := ""
	for _, v := range p.Vdevs {
		t := v.Type
		if t == "disk" {
			t = "stripe"
		}
		if l != "" && l != t {
			return "mixed"
		}
		l = t
	}
	if l == "" {
		return "stripe"
	}
	return l
}

// tangoVolume: <pool>/<vol> montable en /<vol>.
func tangoVolume(pool string, d agentproto.ZfsDataset) (string, bool) {
	rest, ok := strings.CutPrefix(d.Name, pool+"/")
	if !ok || strings.Contains(rest, "/") || zfs.ValidVolumeName(rest) != nil || d.Type != "filesystem" || d.Mountpoint != "/"+rest {
		return "", false
	}
	return rest, true
}

// ZfsPools traduce el reporte a la UI.
func (inv *Inventory) ZfsPools(ctx context.Context) []ZfsPool {
	rep := inv.ZfsReport(ctx)
	ids := inv.stableIDs()
	leaf := func(l agentproto.ZfsLeaf) ZfsLeaf {
		z := ZfsLeaf{Name: l.Name, GUID: l.GUID, State: l.State, Read: l.Read, Write: l.Write, Cksum: l.Cksum, Resilvering: l.Resilvering}
		if l.Disk != "" {
			z.ID = ids(l.Disk)
		}
		return z
	}
	vdev := func(v agentproto.ZfsVdev) ZfsVdev {
		z := ZfsVdev{Name: v.Name, Type: v.Type, State: v.State, Read: v.Read, Write: v.Write, Cksum: v.Cksum, Size: v.Size, Disks: []ZfsLeaf{}}
		for _, l := range v.Disks {
			z.Disks = append(z.Disks, leaf(l))
		}
		return z
	}
	out := []ZfsPool{}
	for _, p := range rep.Pools {
		zp := ZfsPool{ID: ZfsPoolID(p.Name), Name: p.Name, GUID: p.GUID, Health: p.Health, State: zfsState(p), ReadOnly: p.ReadOnly,
			Layout: zfsLayout(p), Raw: p.Size, Frag: p.Frag, Errors: p.Errors, Vdevs: []ZfsVdev{}, Logs: []ZfsVdev{}, Cache: []ZfsLeaf{},
			Volumes: []string{}, Zvols: []ZfsZvol{}, Encrypted: []string{}, Disks: []string{}, Other: []ZfsOther{}}
		for _, v := range p.Vdevs {
			zv := vdev(v)
			zp.Vdevs = append(zp.Vdevs, zv)
			for _, l := range zv.Disks {
				if l.ID != "" {
					zp.Disks = append(zp.Disks, l.ID)
				}
			}
		}
		for _, v := range p.Logs {
			zp.Logs = append(zp.Logs, vdev(v))
		}
		for _, l := range p.Cache {
			zp.Cache = append(zp.Cache, leaf(l))
		}
		if s := p.Scan; s != nil {
			zs := &ZfsScan{Function: s.Function, State: s.State, Progress: s.Progress, Errors: s.Errors}
			if s.Start > 0 {
				zs.Start = time.Unix(s.Start, 0).UTC().Format(time.RFC3339)
			}
			if s.End > 0 && s.State != "scanning" {
				zs.End = time.Unix(s.End, 0).UTC().Format(time.RFC3339)
			}
			zp.Scan = zs
		}
		for _, d := range p.Datasets {
			switch {
			case d.Name == p.Name:
				zp.Allocated, zp.Free, zp.Size = d.Used, d.Avail, d.Used+d.Avail
			case d.Type == "volume" && strings.HasPrefix(d.Name, p.Name+"/zvol/"):
				name := strings.TrimPrefix(d.Name, p.Name+"/zvol/")
				zp.Zvols = append(zp.Zvols, ZfsZvol{Name: name, Size: d.Volsize, Used: d.Used, Device: zfs.ZvolDevice(p.Name, name)})
			default:
				if _, ok := tangoVolume(p.Name, d); ok {
					zp.Volumes = append(zp.Volumes, ZfsVolumeID(d.GUID))
					if d.Encrypted {
						zp.Encrypted = append(zp.Encrypted, ZfsVolumeID(d.GUID))
					}
				} else if d.Type == "filesystem" && d.Mountpoint != "" && !inTango(p.Name, d.Name) {
					zp.Other = append(zp.Other, ZfsOther{Name: d.Name, Mountpoint: d.Mountpoint, Mounted: d.Mounted, Used: d.Used})
				}
			}
		}
		out = append(out, zp)
	}
	return out
}

// inTango: dataset debajo de un volumen de TangoOS (carpetas, apps) o de zvol/.
func inTango(pool, name string) bool {
	rest, ok := strings.CutPrefix(name, pool+"/")
	if !ok {
		return false
	}
	top, _, _ := strings.Cut(rest, "/")
	return top == "zvol" || zfs.ValidVolumeName(top) == nil && strings.Contains(rest, "/")
}

// zfsVolumes son los volúmenes ZFS como Volume (disks con nombres del kernel).
func (inv *Inventory) zfsVolumes(ctx context.Context) []Volume {
	rep := inv.ZfsReport(ctx)
	var out []Volume
	for _, p := range rep.Pools {
		var disks []string
		for _, v := range p.Vdevs {
			for _, l := range v.Disks {
				if l.Disk != "" {
					disks = append(disks, l.Disk)
				}
			}
		}
		for _, d := range p.Datasets {
			vol, ok := tangoVolume(p.Name, d)
			if !ok {
				continue
			}
			v := Volume{ID: ZfsVolumeID(d.GUID), Name: vol, Kind: "zfs", Pool: ZfsPoolID(p.Name), Profile: zfsLayout(p), FS: "zfs",
				Size: d.Used + d.Avail, Used: d.Used, State: map[string]string{"healthy": "healthy", "degraded": "degraded", "readonly": "readonly"}[zfsState(p)],
				Disks: append([]string{}, disks...), Growth30d: []float64{}, Encrypted: d.Encrypted, KeyDevice: d.KeyDevice}
			if v.State == "" {
				v.State = "failed"
			}
			if d.Quota > 0 && d.Quota < v.Size {
				v.Size = d.Quota
			}
			if d.Mounted {
				v.Mount = d.Mountpoint
			}
			if s := p.Scan; s != nil && s.State == "scanning" {
				prog := s.Progress
				v.Progress = &prog
				if s.Function == "resilver" {
					v.Task = "rebuild"
				} else {
					v.State = "scrubbing"
				}
			}
			if s := p.Scan; s != nil && s.End > 0 {
				v.LastScrub = time.Unix(s.End, 0).UTC().Format(time.RFC3339)
			}
			if d.Encrypted && !d.KeyLoaded {
				v.Locked, v.State, v.Task, v.Progress, v.Mount = true, "locked", "", nil, ""
			}
			out = append(out, v)
		}
	}
	return out
}

// zfsDisks mapea disco del kernel → (pool, rol) para storage.disks.list.
func (inv *Inventory) zfsDisks(ctx context.Context) map[string][2]string {
	out := map[string][2]string{}
	for _, p := range inv.ZfsReport(ctx).Pools {
		id := ZfsPoolID(p.Name)
		for _, v := range p.Vdevs {
			for _, l := range v.Disks {
				if l.Disk != "" {
					out[l.Disk] = [2]string{id, "data"}
				}
			}
		}
		for _, v := range p.Logs {
			for _, l := range v.Disks {
				if l.Disk != "" {
					out[l.Disk] = [2]string{id, "log"}
				}
			}
		}
		for _, l := range p.Cache {
			if l.Disk != "" {
				out[l.Disk] = [2]string{id, "cache"}
			}
		}
	}
	return out
}
