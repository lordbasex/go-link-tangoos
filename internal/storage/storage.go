// Package storage is the read-only storage inventory (docs/04, prompt 04):
// disks from sysfs, md arrays from /sys/block/md*/md, Btrfs from mountinfo
// and /sys/fs/btrfs. No exec, no root (docs/adr/0009). It never formats,
// mounts or repairs anything.
package storage

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Disk is exactly the TS Disk (web/packages/client/src/protocol.ts). Unknown
// values are null, never an invented 0 (protocol/README.md §6).
type Disk struct {
	ID           string   `json:"id"`
	Dev          string   `json:"dev"`
	Bay          *int     `json:"bay"`
	Model        string   `json:"model"`
	Serial       string   `json:"serial"`
	Firmware     string   `json:"firmware"`
	Size         uint64   `json:"size"`
	Kind         string   `json:"kind"` // hdd | ssd | nvme
	RPM          int      `json:"rpm,omitempty"`
	TempC        *float64 `json:"tempC"`
	PowerOnHours *int     `json:"powerOnHours"`
	Smart        string   `json:"smart"` // ok | warn | crit | unknown
	Use          string   `json:"use"`   // system | member | free | failed
	Volume       string   `json:"volume,omitempty"`
	Reallocated  int      `json:"reallocated"`
	Pending      int      `json:"pending"`
	// Rebuilding: el disco está recibiendo la reconstrucción de su array (§11).
	Rebuilding bool `json:"rebuilding,omitempty"`
	// Pool y Role: grupo de almacenamiento al que pertenece y su papel (§16).
	Pool string `json:"pool,omitempty"`
	Role string `json:"role,omitempty"` // data | cache
	USB  bool   `json:"-"`
	// ByID is the /dev/disk/by-id link name (no prefix) that tango-agent
	// takes to address the disk ("" when udev made none).
	ByID string `json:"-"`
}

// Volume is exactly the TS Volume.
type Volume struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Mount     string    `json:"mount"`
	Profile   string    `json:"profile"` // basic | raid1 | raid5 | raid6 | raid10
	FS        string    `json:"fs"`
	Size      uint64    `json:"size"`
	Used      uint64    `json:"used"`
	State     string    `json:"state"` // healthy | degraded | building | scrubbing | readonly
	Disks     []string  `json:"disks"`
	Progress  *float64  `json:"progress,omitempty"`
	LastScrub string    `json:"lastScrub"` // ISO; "" when unknown (TODO: scrub history)
	Growth30d []float64 `json:"growth30d"`
	// Tarea de md en curso (protocol/README.md §11): rebuild | reshape | resync; vacío si
	// está quieto. TaskSpeed en bytes/s (sync_speed) y TaskEtaSec estimado.
	Task       string  `json:"task,omitempty"`
	TaskSpeed  *uint64 `json:"taskSpeed,omitempty"`
	TaskEtaSec *int64  `json:"taskEtaSec,omitempty"`
	// Grupos de almacenamiento (§16): kind "pool" = volumen lógico de un grupo.
	Kind       string       `json:"kind,omitempty"` // classic | pool
	Pool       string       `json:"pool,omitempty"` // Pool.id
	Encrypted  bool         `json:"encrypted,omitempty"`
	Locked     bool         `json:"locked,omitempty"`
	KeyDevice  string       `json:"keyDevice,omitempty"`
	Cache      *VolumeCache `json:"cache,omitempty"`
	rebuilding []string     // discos (sdX) que reciben la reconstrucción
}

// SmartInfo is what a SMART source (the privileged agent) knows of a disk.
type SmartInfo struct {
	Health       string // ok | warn | crit | unknown
	TempC        *float64
	PowerOnHours *int
	Reallocated  int
	Pending      int
	RPM          int
}

// SmartSource reads SMART. Reading it needs root, so tangod gets it from
// tango-agent (docs/02); nil means "unknown" for every disk.
type SmartSource interface {
	Smart(ctx context.Context, d Disk) (SmartInfo, error)
}

// Inventory reads under Sys, Dev and Proc (tests point them at fixtures).
type Inventory struct {
	Sys   string
	Dev   string
	Proc  string
	Smart SmartSource
	// PoolAgent es tango-agent (pool.report); nil = sin grupos de almacenamiento.
	PoolAgent PoolSource
	pc        poolCache
}

// New returns an inventory of the running system.
func New() *Inventory { return &Inventory{Sys: "/sys", Dev: "/dev", Proc: "/proc"} }

// systemMounts are the mount points whose backing disks are "system".
var systemMounts = map[string]bool{"/": true, "/boot": true, "/boot/efi": true, "/usr": true, "/var": true, "/var/lib/tango": true}

type mountEntry struct {
	majMin string
	point  string
	fstype string
	source string
}

func (inv *Inventory) mounts() []mountEntry {
	var out []mountEntry
	forLines(filepath.Join(inv.Proc, "self", "mountinfo"), func(line string) {
		pre, post, ok := strings.Cut(line, " - ")
		if !ok {
			return
		}
		f, g := strings.Fields(pre), strings.Fields(post)
		if len(f) < 5 || len(g) < 2 {
			return
		}
		out = append(out, mountEntry{majMin: f[2], point: unescape(f[4]), fstype: g[0], source: g[1]})
	})
	return out
}

// diskOf resolves a block device (by major:minor) to the whole disks under
// it: partitions go to their parent, dm/md to their slaves, recursively.
func (inv *Inventory) diskOf(majMin string) []string {
	p, err := filepath.EvalSymlinks(filepath.Join(inv.Sys, "dev", "block", majMin))
	if err != nil {
		return nil
	}
	return inv.disksUnder(p, 0)
}

// blockOf returns the sysfs path of the block device behind a mount. Btrfs
// reports an anonymous major:minor (0:N) in mountinfo, so for those the mount
// source (/dev/md127, /dev/sdb1, /dev/mapper/x) is resolved instead.
func (inv *Inventory) blockOf(m mountEntry) (string, bool) {
	if !strings.HasPrefix(m.majMin, "0:") {
		p, err := filepath.EvalSymlinks(filepath.Join(inv.Sys, "dev", "block", m.majMin))
		return p, err == nil
	}
	if !strings.HasPrefix(m.source, "/dev/") {
		return "", false
	}
	d, err := filepath.EvalSymlinks(filepath.Join(inv.Dev, strings.TrimPrefix(m.source, "/dev/")))
	if err != nil {
		return "", false
	}
	p, err := filepath.EvalSymlinks(filepath.Join(inv.Sys, "class", "block", filepath.Base(d)))
	return p, err == nil
}

func (inv *Inventory) disksUnder(sysPath string, depth int) []string {
	if depth > 8 {
		return nil
	}
	name := filepath.Base(sysPath)
	if exists(filepath.Join(sysPath, "partition")) {
		return []string{filepath.Base(filepath.Dir(sysPath))}
	}
	slaves, _ := os.ReadDir(filepath.Join(sysPath, "slaves"))
	if len(slaves) > 0 {
		var out []string
		for _, s := range slaves {
			sp, err := filepath.EvalSymlinks(filepath.Join(sysPath, "slaves", s.Name()))
			if err == nil {
				out = append(out, inv.disksUnder(sp, depth+1)...)
			}
		}
		return out
	}
	return []string{name}
}

// Disks lists the physical disks.
func (inv *Inventory) Disks(ctx context.Context) ([]Disk, error) {
	ents, err := os.ReadDir(filepath.Join(inv.Sys, "block"))
	if err != nil {
		return nil, err
	}
	system := map[string]bool{}
	for _, m := range inv.mounts() {
		if systemMounts[m.point] {
			if p, ok := inv.blockOf(m); ok {
				for _, d := range inv.disksUnder(p, 0) {
					system[d] = true
				}
			}
		}
	}
	vols, _ := inv.volumes(ctx)
	poolOf := inv.poolDisks(ctx)
	memberOf := map[string]string{}
	failed := map[string]bool{}
	rebuilding := map[string]bool{}
	for _, v := range vols {
		for _, d := range v.Disks {
			memberOf[d] = v.ID
		}
		for _, d := range v.rebuilding {
			rebuilding[d] = true
		}
	}
	for _, f := range inv.faultyMembers() {
		failed[f] = true
	}
	byID := inv.links("by-id")
	byPath := inv.links("by-path")

	var disks []Disk
	for _, e := range ents {
		name := e.Name()
		if skipDisk(name) {
			continue
		}
		base := filepath.Join(inv.Sys, "block", name)
		if !exists(filepath.Join(base, "device")) { // virtual block device
			continue
		}
		d := Disk{
			Dev:      name,
			Model:    inv.model(base),
			Serial:   inv.serial(base),
			Firmware: firstNonEmpty(readTrim(filepath.Join(base, "device", "rev")), readTrim(filepath.Join(base, "device", "firmware_rev"))),
			Size:     readUint(filepath.Join(base, "size")) * 512,
			Smart:    "unknown",
			Use:      "free",
		}
		switch {
		case strings.HasPrefix(name, "nvme"):
			d.Kind = "nvme"
		case readTrim(filepath.Join(base, "queue", "rotational")) == "0":
			d.Kind = "ssd"
		default:
			d.Kind = "hdd"
		}
		d.ID = stableID(name, d.Model, d.Serial, byID[name], byPath[name])
		d.ByID = preferredByID(byID[name])
		for _, p := range byPath[name] {
			if strings.Contains(p, "-usb-") {
				d.USB = true
			}
		}
		d.TempC = inv.hwmonTemp(base)
		switch {
		case system[name]:
			d.Use = "system"
		case failed[name]:
			d.Use, d.Volume = "failed", memberOf[name]
			if pr, ok := poolOf[name]; ok {
				d.Volume, d.Pool, d.Role = "", pr[0], pr[1]
			}
		case poolOf[name][0] != "":
			d.Use, d.Pool, d.Role = "member", poolOf[name][0], poolOf[name][1]
			d.Rebuilding = rebuilding[name]
		case memberOf[name] != "":
			d.Use, d.Volume = "member", memberOf[name]
			d.Rebuilding = rebuilding[name]
		case inv.hasHolders(base, name):
			d.Use = "member"
		}
		if inv.Smart != nil {
			if si, err := inv.Smart.Smart(ctx, d); err == nil {
				d.Smart = si.Health
				if si.TempC != nil {
					d.TempC = si.TempC
				}
				d.PowerOnHours, d.Reallocated, d.Pending, d.RPM = si.PowerOnHours, si.Reallocated, si.Pending, si.RPM
			}
		}
		disks = append(disks, d)
	}
	inv.assignBays(disks, byPath)
	return disks, nil
}

// assignBays numbers bays by port order (/dev/disk/by-path), excluding the
// system disk and USB disks (protocol/README.md §6).
func (inv *Inventory) assignBays(disks []Disk, byPath map[string][]string) {
	type slot struct {
		path string
		i    int
	}
	var slots []slot
	for i, d := range disks {
		if d.Use == "system" || d.USB {
			continue
		}
		p := d.Dev
		if ps := byPath[d.Dev]; len(ps) > 0 {
			p = ps[0]
		}
		slots = append(slots, slot{p, i})
	}
	sort.Slice(slots, func(a, b int) bool { return natLess(slots[a].path, slots[b].path) })
	for n, s := range slots {
		bay := n + 1
		disks[s.i].Bay = &bay
	}
}

// links maps whole-disk names to their /dev/disk/<kind> link names, sorted.
func (inv *Inventory) links(kind string) map[string][]string {
	out := map[string][]string{}
	dir := filepath.Join(inv.Dev, "disk", kind)
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), "-part") {
			continue
		}
		t, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out[filepath.Base(t)] = append(out[filepath.Base(t)], e.Name())
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// stableID: WWN, then NVMe EUI, then the best /dev/disk/by-id name, then
// model+serial, then the port path. Never the bare /dev/sdX name unless
// nothing else exists.
func stableID(dev, model, serial string, ids, paths []string) string {
	if id := preferredByID(ids); id != "" {
		return id
	}
	if serial != "" {
		return "serial-" + sanitize(model) + "_" + sanitize(serial)
	}
	if len(paths) > 0 {
		return "path-" + paths[0]
	}
	return "dev-" + dev
}

// preferredByID picks the by-id link to name a disk by: wwn-, NVMe EUI/NGUID,
// then ata-, nvme-, scsi-, usb-, then any other.
func preferredByID(ids []string) string {
	for _, prefix := range []string{"wwn-", "nvme-eui.", "nvme-nguid.", "ata-", "nvme-", "scsi-", "usb-"} {
		for _, id := range ids {
			if strings.HasPrefix(id, prefix) {
				return id
			}
		}
	}
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

func (inv *Inventory) model(base string) string {
	vendor := readTrim(filepath.Join(base, "device", "vendor"))
	model := readTrim(filepath.Join(base, "device", "model"))
	if vendor != "" && vendor != "ATA" && !strings.HasPrefix(strings.ToUpper(model), strings.ToUpper(vendor)) {
		return vendor + " " + model
	}
	return model
}

// serial: device/serial (NVMe, some SATA), else the SCSI VPD page 0x80
// (unit serial number), which SATA-behind-SCSI and virtio-scsi expose.
func (inv *Inventory) serial(base string) string {
	if s := readTrim(filepath.Join(base, "device", "serial")); s != "" {
		return s
	}
	b, err := os.ReadFile(filepath.Join(base, "device", "vpd_pg80"))
	if err != nil || len(b) < 4 || b[1] != 0x80 {
		return ""
	}
	n := int(b[2])<<8 | int(b[3])
	if 4+n > len(b) {
		n = len(b) - 4
	}
	return strings.TrimSpace(strings.Trim(string(b[4:4+n]), "\x00"))
}

func (inv *Inventory) hwmonTemp(base string) *float64 {
	for _, pat := range []string{"device/hwmon/hwmon*/temp1_input", "device/hwmon*/temp1_input"} {
		ms, _ := filepath.Glob(filepath.Join(base, pat))
		for _, m := range ms {
			if v := readUint(m); v > 0 {
				t := float64(v) / 1000
				return &t
			}
		}
	}
	return nil
}

func (inv *Inventory) hasHolders(base, name string) bool {
	if hs, _ := os.ReadDir(filepath.Join(base, "holders")); len(hs) > 0 {
		return true
	}
	parts, _ := os.ReadDir(base)
	for _, p := range parts {
		if strings.HasPrefix(p.Name(), name) {
			if hs, _ := os.ReadDir(filepath.Join(base, p.Name(), "holders")); len(hs) > 0 {
				return true
			}
		}
	}
	return false
}

// ---- Volumes -------------------------------------------------------------------

func (inv *Inventory) mdArrays() []string {
	ents, _ := os.ReadDir(filepath.Join(inv.Sys, "block"))
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "md") && exists(filepath.Join(inv.Sys, "block", e.Name(), "md")) {
			out = append(out, e.Name())
		}
	}
	return out
}

// mdMembers returns the whole disks of an array and which of them are faulty.
func (inv *Inventory) mdMembers(md string) (disks []string, faulty []string) {
	dir := filepath.Join(inv.Sys, "block", md, "md")
	devs, _ := filepath.Glob(filepath.Join(dir, "dev-*"))
	seen := map[string]bool{}
	for _, dv := range devs {
		p, err := filepath.EvalSymlinks(filepath.Join(dv, "block"))
		if err != nil {
			continue
		}
		for _, d := range inv.disksUnder(p, 0) {
			if seen[d] {
				continue
			}
			seen[d] = true
			disks = append(disks, d)
			if strings.Contains(readTrim(filepath.Join(dv, "state")), "faulty") {
				faulty = append(faulty, d)
			}
		}
	}
	sort.Strings(disks)
	return
}

func (inv *Inventory) faultyMembers() []string {
	var out []string
	for _, md := range inv.mdArrays() {
		_, f := inv.mdMembers(md)
		out = append(out, f...)
	}
	return out
}

var mdProfiles = map[string]string{"raid1": "raid1", "raid5": "raid5", "raid6": "raid6", "raid10": "raid10", "raid0": "basic", "linear": "basic"}

// Volumes lists md arrays (with their mounted filesystem, if any) and
// mounted Btrfs filesystems that are not on md. System mounts are excluded.
// Volume.Disks carries the same stable ids as Disk.ID.
func (inv *Inventory) Volumes(ctx context.Context) ([]Volume, error) {
	vols, err := inv.volumes(ctx)
	if err != nil {
		return nil, err
	}
	byID, byPath := inv.links("by-id"), inv.links("by-path")
	for i := range vols {
		for j, name := range vols[i].Disks {
			base := filepath.Join(inv.Sys, "block", name)
			vols[i].Disks[j] = stableID(name, inv.model(base), inv.serial(base), byID[name], byPath[name])
		}
	}
	return vols, nil
}

// volumes is Volumes with kernel disk names (sdb) in Volume.Disks.
func (inv *Inventory) volumes(ctx context.Context) ([]Volume, error) {
	mounts := inv.mounts()
	// Dispositivos del sistema: el Btrfs de la raíz (instalación desde la ISO) tiene más
	// subvolúmenes montados (/.snapshots, /var/log) que no son volúmenes de datos.
	systemDev := map[string]bool{}
	for _, m := range mounts {
		if systemMounts[m.point] {
			if p, ok := inv.blockOf(m); ok {
				systemDev[filepath.Base(p)] = true
			}
		}
	}
	mountOf := map[string]mountEntry{} // whole-device name → mount
	for _, m := range mounts {
		if systemMounts[m.point] {
			continue
		}
		p, ok := inv.blockOf(m)
		if !ok || systemDev[filepath.Base(p)] {
			continue
		}
		if _, ok := mountOf[filepath.Base(p)]; !ok {
			mountOf[filepath.Base(p)] = m
		}
	}

	var vols []Volume
	onMD := map[string]bool{}
	for _, md := range inv.mdArrays() {
		dir := filepath.Join(inv.Sys, "block", md, "md")
		disks, _ := inv.mdMembers(md)
		st := readTrim(filepath.Join(dir, "array_state"))
		stale := len(disks) == 0 && (readTrim(filepath.Join(dir, "level")) == "" || st == "clear" || st == "inactive")
		if stale || inv.isPoolMD(md) {
			// Un nodo md vacío que dejó una creación fallida, o un md de un grupo: sus
			// volúmenes son los lógicos (poolVolumes).
			for _, d := range disks {
				onMD[d] = true
			}
			continue
		}
		v := Volume{
			ID: md, Name: inv.mdName(md), Profile: mdProfiles[readTrim(filepath.Join(dir, "level"))],
			Size: readUint(filepath.Join(inv.Sys, "block", md, "size")) * 512, State: "healthy",
			Disks: disks, Growth30d: []float64{},
		}
		if v.Profile == "" {
			v.Profile = "basic"
		}
		for _, d := range disks {
			onMD[d] = true
		}
		if m, ok := mountOf[md]; ok {
			v.Mount, v.FS = m.point, m.fstype
			v.Name = filepath.Base(m.point)
			v.Size, v.Used = statfs(m.point, v.Size)
		}
		// ID estable (protocol/README.md §11): el UUID del Btrfs, que no cambia entre
		// arranques ni si el kernel renumera el array (md127 → md126). Sin Btrfs
		// visible, el nombre del array ("md-volume2").
		if uuid := inv.btrfsUUID(md); uuid != "" {
			v.ID = uuid
		} else {
			v.ID = "md-" + inv.mdName(md)
		}
		if readUint(filepath.Join(dir, "degraded")) > 0 {
			v.State = "degraded"
		}
		action := readTrim(filepath.Join(dir, "sync_action"))
		if task := mdTasks[action]; task != "" {
			v.Task = task
			if kb := readUint(filepath.Join(dir, "sync_speed")); kb > 0 {
				speed := kb * 1024
				v.TaskSpeed = &speed
				if done, total, ok := syncCounts(readTrim(filepath.Join(dir, "sync_completed"))); ok && total > done {
					eta := int64((total - done) * 512 / speed)
					v.TaskEtaSec = &eta
				}
			}
		}
		if action == "recover" {
			v.rebuilding = inv.recovering(md)
		}
		switch action {
		case "resync", "recover", "reshape":
			// También si figura degradado: md arma un RAID 5/6 nuevo recuperando el
			// último disco ([UU_]), y un disco reemplazado se reconstruye igual. Mientras
			// avanza es "construyendo", con progreso; degradado es cuando no avanza.
			v.State = "building"
			v.Progress = progress(readTrim(filepath.Join(dir, "sync_completed")))
		case "check", "repair":
			if v.State == "healthy" {
				v.State = "scrubbing"
			}
			v.Progress = progress(readTrim(filepath.Join(dir, "sync_completed")))
		}
		if strings.Contains(readTrim(filepath.Join(dir, "array_state")), "read-only") {
			v.State = "readonly"
		}
		vols = append(vols, v)
	}

	// Btrfs not on md (single disk or Btrfs RAID1, ADR 0004).
	seenFS := map[string]bool{}
	for dev, m := range mountOf {
		if m.fstype != "btrfs" || strings.HasPrefix(dev, "md") || strings.HasPrefix(dev, "dm-") {
			continue
		}
		uuid := inv.btrfsUUID(dev)
		if uuid == "" || seenFS[uuid] {
			continue
		}
		seenFS[uuid] = true
		v := Volume{ID: uuid, Name: filepath.Base(m.point), Mount: m.point, FS: "btrfs", State: "healthy", Profile: "basic", Growth30d: []float64{}}
		devs, _ := os.ReadDir(filepath.Join(inv.Sys, "fs", "btrfs", uuid, "devices"))
		for _, d := range devs {
			// devices/<x> apunta al dispositivo de bloque mismo (disco entero o partición):
			// disksUnder lo lleva al disco (antes se pasaba el directorio padre, "block").
			if p, err := filepath.EvalSymlinks(filepath.Join(inv.Sys, "fs", "btrfs", uuid, "devices", d.Name())); err == nil {
				for _, w := range inv.disksUnder(p, 0) {
					if !onMD[w] {
						v.Disks = append(v.Disks, w)
					}
				}
			}
		}
		if exists(filepath.Join(inv.Sys, "fs", "btrfs", uuid, "allocation", "data", "raid1")) {
			v.Profile = "raid1"
		}
		v.Size, v.Used = statfs(m.point, 0)
		vols = append(vols, v)
	}
	vols = append(vols, inv.poolVolumes(ctx, mounts)...)
	sort.Slice(vols, func(i, j int) bool { return vols[i].Name < vols[j].Name })
	return vols, nil
}

// mdTasks traduce sync_action a Volume.task.
var mdTasks = map[string]string{"recover": "rebuild", "reshape": "reshape", "resync": "resync"}

// syncCounts lee sync_completed ("hechos / total", en sectores).
func syncCounts(s string) (done, total uint64, ok bool) {
	a, b, found := strings.Cut(s, "/")
	if !found {
		return 0, 0, false
	}
	x, err1 := strconv.ParseUint(strings.TrimSpace(a), 10, 64)
	y, err2 := strconv.ParseUint(strings.TrimSpace(b), 10, 64)
	return x, y, err1 == nil && err2 == nil
}

// recovering devuelve los discos de un array que están recibiendo la reconstrucción:
// los miembros que no están in_sync ni fallados (md los marca como spare mientras dura).
func (inv *Inventory) recovering(md string) []string {
	var out []string
	devs, _ := filepath.Glob(filepath.Join(inv.Sys, "block", md, "md", "dev-*"))
	for _, dv := range devs {
		st := readTrim(filepath.Join(dv, "state"))
		if strings.Contains(st, "in_sync") || strings.Contains(st, "faulty") {
			continue
		}
		if p, err := filepath.EvalSymlinks(filepath.Join(dv, "block")); err == nil {
			out = append(out, inv.disksUnder(p, 0)...)
		}
	}
	return out
}

func (inv *Inventory) mdName(md string) string {
	ents, _ := os.ReadDir(filepath.Join(inv.Dev, "md"))
	for _, e := range ents {
		if t, err := os.Readlink(filepath.Join(inv.Dev, "md", e.Name())); err == nil && filepath.Base(t) == md {
			return e.Name()
		}
	}
	return md
}

func (inv *Inventory) btrfsUUID(dev string) string {
	fss, _ := os.ReadDir(filepath.Join(inv.Sys, "fs", "btrfs"))
	for _, fs := range fss {
		if exists(filepath.Join(inv.Sys, "fs", "btrfs", fs.Name(), "devices", dev)) {
			return fs.Name()
		}
		// mounted through a partition: devices/<sdb1>
		parts, _ := os.ReadDir(filepath.Join(inv.Sys, "fs", "btrfs", fs.Name(), "devices"))
		for _, p := range parts {
			if strings.HasPrefix(p.Name(), dev) {
				return fs.Name()
			}
		}
	}
	return ""
}

func statfs(path string, fallback uint64) (size, used uint64) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return fallback, 0
	}
	bs := uint64(st.Bsize)
	return st.Blocks * bs, (st.Blocks - st.Bfree) * bs
}

func progress(s string) *float64 { // "1234 / 5678"
	a, b, ok := strings.Cut(s, "/")
	if !ok {
		return nil
	}
	x, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	y, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil || y == 0 {
		return nil
	}
	p := x / y
	return &p
}

// ---- helpers -------------------------------------------------------------------

func skipDisk(name string) bool {
	for _, p := range []string{"loop", "ram", "zram", "sr", "md", "dm-", "fd", "nbd", "zd"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func readTrim(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readUint(p string) uint64 { n, _ := strconv.ParseUint(readTrim(p), 10, 64); return n }

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		}
		return '_'
	}, strings.TrimSpace(s))
}

func unescape(s string) string { // mountinfo octal escapes (\040 = space)
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// natLess compares with digit runs as numbers ("scsi-0:0:0:10" after ":2").
func natLess(a, b string) bool {
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da > 0 && db > 0 {
			na, _ := strconv.Atoi(a[:da])
			nb, _ := strconv.Atoi(b[:db])
			if na != nb {
				return na < nb
			}
			a, b = a[da:], b[db:]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

func forLines(path string, fn func(string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		fn(sc.Text())
	}
}
