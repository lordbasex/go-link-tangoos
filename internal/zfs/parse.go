package zfs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Salida JSON de OpenZFS 2.3 (`zpool status -j -p`, `zpool list -j -p`,
// `zfs list -j -p`). Con -p los números vienen como strings decimales.

type jsonVdev struct {
	Name     string              `json:"name"`
	Type     string              `json:"vdev_type"`
	GUID     string              `json:"guid"`
	Path     string              `json:"path"`
	State    string              `json:"state"`
	Total    string              `json:"total_space"`
	RepSize  string              `json:"rep_dev_size"`
	Read     string              `json:"read_errors"`
	Write    string              `json:"write_errors"`
	Cksum    string              `json:"checksum_errors"`
	Vdevs    map[string]jsonVdev `json:"vdevs"`
}

type jsonScan struct {
	Function  string `json:"function"`
	State     string `json:"state"`
	Start     string `json:"start_time"`
	End       string `json:"end_time"`
	ToExamine string `json:"to_examine"`
	Examined  string `json:"examined"`
	Issued    string `json:"issued"`
	Errors    string `json:"errors"`
}

type jsonPool struct {
	Name    string              `json:"name"`
	State   string              `json:"state"`
	GUID    string              `json:"pool_guid"`
	Status  string              `json:"status"`
	Errors  string              `json:"error_count"`
	Scan    *jsonScan           `json:"scan_stats"`
	Vdevs   map[string]jsonVdev `json:"vdevs"`
	Logs    map[string]jsonVdev `json:"logs"`
	L2Cache map[string]jsonVdev `json:"l2cache"`
	Spares  map[string]jsonVdev `json:"spares"`
}

func num(s string) uint64 {
	n, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return n
}

func numI(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// Resolver traduce la ruta de un vdev (/dev/disk/by-id/…-part1) al disco entero del
// kernel (sdb); "" si ya no está. Lo da el agente (lee /dev y /sys).
type Resolver func(path string) string

// ParseStatus lee `zpool status -j -p` y arma los pools (sin datasets ni tamaños de
// `zpool list`, que se agregan con MergeList).
func ParseStatus(b []byte, resolve Resolver) ([]agentproto.ZfsPoolInfo, error) {
	var doc struct {
		Pools map[string]jsonPool `json:"pools"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("zpool status: %w", err)
	}
	out := []agentproto.ZfsPoolInfo{}
	for _, name := range SortedKeys(doc.Pools) {
		p := doc.Pools[name]
		pi := agentproto.ZfsPoolInfo{Name: p.Name, GUID: p.GUID, Health: p.State, Vdevs: []agentproto.ZfsVdev{},
			Cache: []agentproto.ZfsLeaf{}, Logs: []agentproto.ZfsVdev{}, Spares: []agentproto.ZfsLeaf{}, Datasets: []agentproto.ZfsDataset{}}
		if num(p.Errors) > 0 {
			pi.Errors = fmt.Sprintf("%d errores de datos", num(p.Errors))
		}
		if s := p.Scan; s != nil {
			sc := &agentproto.ZfsScan{Function: strings.ToLower(s.Function), State: strings.ToLower(s.State),
				Start: numI(s.Start), End: numI(s.End), Examined: num(s.Examined), Total: num(s.ToExamine), Errors: num(s.Errors)}
			done := num(s.Issued)
			if done == 0 {
				done = sc.Examined
			}
			switch {
			case sc.State == "finished":
				sc.Progress = 1
			case sc.Total > 0:
				sc.Progress = min(1, float64(done)/float64(sc.Total))
			}
			pi.Scan = sc
		}
		root, ok := p.Vdevs[name]
		if ok {
			for _, vn := range SortedKeys(root.Vdevs) {
				pi.Vdevs = append(pi.Vdevs, vdevOf(root.Vdevs[vn], resolve))
			}
		}
		for _, vn := range SortedKeys(p.Logs) {
			pi.Logs = append(pi.Logs, vdevOf(p.Logs[vn], resolve))
		}
		for _, vn := range SortedKeys(p.L2Cache) {
			pi.Cache = append(pi.Cache, leafOf(p.L2Cache[vn], resolve))
		}
		for _, vn := range SortedKeys(p.Spares) {
			pi.Spares = append(pi.Spares, leafOf(p.Spares[vn], resolve))
		}
		out = append(out, pi)
	}
	return out, nil
}

// vdevOf: un disco suelto es un vdev "disk" con un solo disco.
func vdevOf(v jsonVdev, resolve Resolver) agentproto.ZfsVdev {
	out := agentproto.ZfsVdev{Name: v.Name, Type: v.Type, State: v.State, Read: num(v.Read), Write: num(v.Write),
		Cksum: num(v.Cksum), Size: num(v.Total), Disks: []agentproto.ZfsLeaf{}}
	switch v.Type {
	case "disk", "file":
		out.Type = "disk"
		out.Disks = append(out.Disks, leafOf(v, resolve))
		return out
	case "raidz":
		// "raidz2-0" → raidz2 (zpool no informa la paridad aparte).
		lvl := strings.SplitN(v.Name, "-", 2)[0]
		if lvl == "raidz" {
			lvl = "raidz1"
		}
		out.Type = lvl
	}
	for _, k := range sortedLeafKeys(v.Vdevs) {
		c := v.Vdevs[k]
		if len(c.Vdevs) > 0 { // replacing-N / spare-N: los discos de adentro
			for _, kk := range sortedLeafKeys(c.Vdevs) {
				l := leafOf(c.Vdevs[kk], resolve)
				l.Resilvering = c.Type == "replacing" || l.Resilvering
				out.Disks = append(out.Disks, l)
			}
			continue
		}
		out.Disks = append(out.Disks, leafOf(c, resolve))
	}
	return out
}

func leafOf(v jsonVdev, resolve Resolver) agentproto.ZfsLeaf {
	l := agentproto.ZfsLeaf{Name: v.Name, GUID: v.GUID, State: v.State, Read: num(v.Read), Write: num(v.Write), Cksum: num(v.Cksum)}
	if resolve != nil && v.Path != "" {
		l.Disk = resolve(v.Path)
	}
	return l
}

// sortedLeafKeys respeta el orden de creación: zpool no lo garantiza en el JSON (un
// mapa), así que se ordena por nombre natural (scsi2 < scsi10).
func sortedLeafKeys(m map[string]jsonVdev) []string {
	keys := SortedKeys(m)
	sort.SliceStable(keys, func(i, j int) bool { return naturalLess(keys[i], keys[j]) })
	return keys
}

func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		da, db := digitsPrefix(a), digitsPrefix(b)
		if da != "" && db != "" {
			na, _ := strconv.ParseUint(da, 10, 64)
			nb, _ := strconv.ParseUint(db, 10, 64)
			if na != nb {
				return na < nb
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digitsPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

type jsonProp struct {
	Value string `json:"value"`
}

// MergeList completa tamaños y readonly desde `zpool list -j -p -o …`.
func MergeList(pools []agentproto.ZfsPoolInfo, b []byte) error {
	var doc struct {
		Pools map[string]struct {
			Properties map[string]jsonProp `json:"properties"`
		} `json:"pools"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("zpool list: %w", err)
	}
	for i := range pools {
		p, ok := doc.Pools[pools[i].Name]
		if !ok {
			continue
		}
		pools[i].Size = num(p.Properties["size"].Value)
		pools[i].Alloc = num(p.Properties["allocated"].Value)
		pools[i].Free = num(p.Properties["free"].Value)
		if f, err := strconv.Atoi(p.Properties["fragmentation"].Value); err == nil {
			pools[i].Frag = &f
		}
		pools[i].ReadOnly = p.Properties["readonly"].Value == "on"
	}
	return nil
}

// DatasetProps son las columnas que pide el agente a `zfs list`.
const DatasetProps = "name,type,guid,mountpoint,mounted,used,avail,refer,quota,reservation,volsize,encryption,keystatus,origin"

// ParseDatasets lee `zfs list -j -p -t filesystem,volume -o DatasetProps` y los
// reparte por pool.
func ParseDatasets(b []byte) (map[string][]agentproto.ZfsDataset, error) {
	var doc struct {
		Datasets map[string]struct {
			Name       string              `json:"name"`
			Pool       string              `json:"pool"`
			Properties map[string]jsonProp `json:"properties"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("zfs list: %w", err)
	}
	out := map[string][]agentproto.ZfsDataset{}
	for _, name := range SortedKeys(doc.Datasets) {
		d := doc.Datasets[name]
		p := d.Properties
		ds := agentproto.ZfsDataset{Name: d.Name, Type: p["type"].Value, GUID: p["guid"].Value,
			Mounted: p["mounted"].Value == "yes", Used: num(p["used"].Value), Avail: num(p["available"].Value),
			Refer: num(p["referenced"].Value), Quota: num(p["quota"].Value), Reservation: num(p["reservation"].Value),
			Volsize: num(p["volsize"].Value)}
		if mp := p["mountpoint"].Value; mp != "" && mp != "-" && mp != "none" && mp != "legacy" {
			ds.Mountpoint = mp
		}
		if enc := p["encryption"].Value; enc != "" && enc != "off" && enc != "-" {
			ds.Encrypted = true
			ds.KeyLoaded = p["keystatus"].Value == "available"
		}
		if o := p["origin"].Value; o != "" && o != "-" {
			ds.Origin = o
		}
		pool := d.Pool
		if pool == "" {
			pool = PoolOfDataset(d.Name)
		}
		out[pool] = append(out[pool], ds)
	}
	return out, nil
}

// ParseImport lee el texto de `zpool import -d /dev/disk/by-id` (sin -j en 2.3): los
// pools que se pueden importar, con sus discos por nombre de by-id.
func ParseImport(text string, resolve Resolver) []agentproto.ZfsImportable {
	out := []agentproto.ZfsImportable{}
	var cur *agentproto.ZfsImportable
	inConfig := false
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "pool: "):
			out = append(out, agentproto.ZfsImportable{Name: strings.TrimPrefix(t, "pool: "), Disks: []string{}})
			cur, inConfig = &out[len(out)-1], false
		case cur == nil:
		case strings.HasPrefix(t, "id: "):
			cur.GUID = strings.TrimPrefix(t, "id: ")
		case strings.HasPrefix(t, "state: "):
			cur.State = strings.TrimPrefix(t, "state: ")
		case strings.HasPrefix(t, "status: "):
			cur.Status = strings.TrimPrefix(t, "status: ")
		case t == "config:":
			inConfig = true
		case inConfig && t != "":
			f := strings.Fields(t)
			if len(f) == 0 || f[0] == cur.Name || strings.HasPrefix(f[0], "mirror-") || strings.HasPrefix(f[0], "raidz") ||
				f[0] == "logs" || f[0] == "cache" || f[0] == "spares" {
				continue
			}
			if resolve != nil {
				if d := resolve("/dev/disk/by-id/" + f[0]); d != "" {
					cur.Disks = append(cur.Disks, d)
				}
			}
		}
	}
	return out
}
