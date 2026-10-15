package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type tree struct {
	t    *testing.T
	root string
}

func (tr tree) file(rel string, data []byte) {
	tr.t.Helper()
	p := filepath.Join(tr.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr tree) dir(rel string) {
	tr.t.Helper()
	if err := os.MkdirAll(filepath.Join(tr.root, rel), 0o755); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr tree) link(rel, target string) {
	tr.t.Helper()
	p := filepath.Join(tr.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		tr.t.Fatal(err)
	}
}

func vpd80(serial string) []byte {
	return append([]byte{0, 0x80, 0, byte(len(serial))}, []byte(serial)...)
}

// Mirrors the dev VM: sda system disk (QEMU, root on sda1), data disks with
// serials in VPD page 0x80 and by-path order that differs from sdX order.
func fixture(t *testing.T) *Inventory {
	tr := tree{t, t.TempDir()}
	disk := func(name, serial string, sectors string) {
		tr.file("sys/block/"+name+"/device/vendor", []byte("QEMU    \n"))
		tr.file("sys/block/"+name+"/device/model", []byte("QEMU HARDDISK   \n"))
		tr.file("sys/block/"+name+"/device/rev", []byte("2.5+\n"))
		tr.file("sys/block/"+name+"/device/vpd_pg80", vpd80(serial))
		tr.file("sys/block/"+name+"/size", []byte(sectors+"\n"))
		tr.file("sys/block/"+name+"/queue/rotational", []byte("1\n"))
		tr.dir("sys/block/" + name + "/holders")
	}
	disk("sda", "TANGO-SYS", "41943040")
	disk("sdb", "TANGO-D2", "33554432")
	disk("sdc", "TANGO-D1", "33554432")
	disk("sdd", "TANGO-D3", "33554432")
	tr.file("sys/block/sda/sda1/partition", []byte("1\n"))
	tr.link("sys/dev/block/8:1", "../../block/sda/sda1")
	tr.dir("sys/block/sr0/device") // optical: skipped
	tr.dir("sys/block/loop0")      // virtual: skipped

	// md0 (RAID1) on sdb1 + sdd1, degraded, sdd faulty; mounted at /volume1
	tr.file("sys/block/sdb/sdb1/partition", []byte("1\n"))
	tr.file("sys/block/sdd/sdd1/partition", []byte("1\n"))
	tr.file("sys/block/md0/md/level", []byte("raid1\n"))
	tr.file("sys/block/md0/md/degraded", []byte("1\n"))
	tr.file("sys/block/md0/md/sync_action", []byte("idle\n"))
	tr.file("sys/block/md0/md/array_state", []byte("clean\n"))
	tr.file("sys/block/md0/size", []byte("33000000\n"))
	tr.link("sys/block/md0/md/dev-sdb1/block", "../../../sdb/sdb1")
	tr.link("sys/block/md0/md/dev-sdd1/block", "../../../sdd/sdd1")
	tr.file("sys/block/md0/md/dev-sdd1/state", []byte("faulty\n"))
	tr.file("sys/block/md0/md/dev-sdb1/state", []byte("in_sync\n"))
	tr.link("sys/dev/block/9:0", "../../block/md0")
	tr.link("dev/md/data", "../md0")

	tr.link("dev/disk/by-path/pci-0000:06:05.0-scsi-0:0:0:0", "../../sda")
	tr.link("dev/disk/by-path/pci-0000:06:05.0-scsi-0:0:0:10", "../../sdd")
	tr.link("dev/disk/by-path/pci-0000:06:05.0-scsi-0:0:0:1", "../../sdc")
	tr.link("dev/disk/by-path/pci-0000:06:05.0-scsi-0:0:0:2", "../../sdb")
	tr.link("dev/disk/by-path/pci-0000:06:05.0-scsi-0:0:0:2-part1", "../../sdb1")
	tr.link("dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_drive-scsi1", "../../sdc")
	tr.link("dev/disk/by-id/wwn-0x5000c500a1b2c3d4", "../../sdb")

	root := tr.root
	tr.file("proc/self/mountinfo", []byte(
		"29 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw\n"+
			"24 29 0:22 / /sys rw - sysfs sysfs rw\n"+
			"90 29 9:0 / /volume\\0401 rw - ext4 /dev/md0 rw\n"))
	return &Inventory{Sys: filepath.Join(root, "sys"), Dev: filepath.Join(root, "dev"), Proc: filepath.Join(root, "proc")}
}

func TestDisks(t *testing.T) {
	inv := fixture(t)
	disks, err := inv.Disks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Disk{}
	for _, d := range disks {
		got[d.Dev] = d
	}
	if len(disks) != 4 {
		t.Fatalf("want 4 disks, got %d: %+v", len(disks), disks)
	}
	sda := got["sda"]
	if sda.Use != "system" || sda.Bay != nil || sda.Serial != "TANGO-SYS" || sda.Model != "QEMU HARDDISK" || sda.Size != 41943040*512 {
		t.Fatalf("sda: %+v", sda)
	}
	if sda.ID != "serial-QEMU_HARDDISK_TANGO-SYS" || got["sdc"].ID != "scsi-0QEMU_QEMU_HARDDISK_drive-scsi1" || got["sdc"].ByID != "scsi-0QEMU_QEMU_HARDDISK_drive-scsi1" || sda.Kind != "hdd" || sda.Smart != "unknown" || sda.TempC != nil {
		t.Fatalf("sda id/kind: %+v", sda)
	}
	// bays follow by-path order with natural sort: sdc(…:1)=1, sdb(…:2)=2, sdd(…:10)=3
	for dev, bay := range map[string]int{"sdc": 1, "sdb": 2, "sdd": 3} {
		if got[dev].Bay == nil || *got[dev].Bay != bay {
			t.Errorf("%s bay = %v, want %d", dev, got[dev].Bay, bay)
		}
	}
	if got["sdb"].ID != "wwn-0x5000c500a1b2c3d4" {
		t.Errorf("sdb id prefers WWN: %s", got["sdb"].ID)
	}
	if got["sdb"].Use != "member" || got["sdb"].Volume != "md-data" {
		t.Errorf("sdb: %+v", got["sdb"])
	}
	if got["sdd"].Use != "failed" || got["sdd"].Volume != "md-data" {
		t.Errorf("sdd: %+v", got["sdd"])
	}
	if got["sdc"].Use != "free" || got["sdc"].Serial != "TANGO-D1" {
		t.Errorf("sdc: %+v", got["sdc"])
	}
}

func TestVolumes(t *testing.T) {
	inv := fixture(t)
	vols, err := inv.Volumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 1 {
		t.Fatalf("vols: %+v", vols)
	}
	v := vols[0]
	if v.ID != "md-data" || v.Profile != "raid1" || v.State != "degraded" || v.Mount != "/volume 1" || v.Name != "volume 1" {
		t.Fatalf("vol: %+v", v)
	}
	// Volume.Disks usa los mismos ids estables que Disk.ID (no sdX).
	if len(v.Disks) != 2 || v.Disks[0] != "wwn-0x5000c500a1b2c3d4" || v.Disks[1] != "serial-QEMU_HARDDISK_TANGO-D3" {
		t.Fatalf("members: %v", v.Disks)
	}

	// Con Btrfs encima, el ID es el UUID del filesystem (estable aunque md se renumere).
	if err := os.MkdirAll(filepath.Join(inv.Sys, "fs/btrfs/0b8f4c2e-1111-4222-8333-444455556666/devices/md0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if vols, _ := inv.Volumes(context.Background()); len(vols) != 1 || vols[0].ID != "0b8f4c2e-1111-4222-8333-444455556666" {
		t.Fatalf("id por UUID: %+v", vols)
	}

	// Degradado pero reconstruyendo (RAID 5 recién creado, disco reemplazado):
	// "building" con progreso, no "degraded".
	_ = os.WriteFile(filepath.Join(inv.Sys, "block/md0/md/sync_action"), []byte("recover\n"), 0o644)
	_ = os.WriteFile(filepath.Join(inv.Sys, "block/md0/md/sync_completed"), []byte("100 / 400\n"), 0o644)
	vols, err = inv.Volumes(context.Background())
	if err != nil || len(vols) != 1 || vols[0].State != "building" || vols[0].Progress == nil || *vols[0].Progress != 0.25 {
		t.Fatalf("reconstruyendo: %+v %v", vols, err)
	}
	// Tarea, velocidad y ETA (sync_speed en KB/s; 300 sectores = 153600 B a 1 MB/s).
	_ = os.WriteFile(filepath.Join(inv.Sys, "block/md0/md/sync_speed"), []byte("1000\n"), 0o644)
	vols, _ = inv.Volumes(context.Background())
	if v := vols[0]; v.Task != "rebuild" || v.TaskSpeed == nil || *v.TaskSpeed != 1024000 || v.TaskEtaSec == nil || *v.TaskEtaSec != 0 {
		t.Fatalf("tarea: %+v", v)
	}
	// El miembro que no está in_sync (ni fallado) recibe la reconstrucción.
	_ = os.WriteFile(filepath.Join(inv.Sys, "block/md0/md/dev-sdd1/state"), []byte("spare\n"), 0o644)
	disks, _ := inv.Disks(context.Background())
	for _, d := range disks {
		if (d.Dev == "sdd") != d.Rebuilding {
			t.Fatalf("rebuilding: %s %v", d.Dev, d.Rebuilding)
		}
	}
}

func TestNatLess(t *testing.T) {
	if !natLess("a:2", "a:10") || natLess("a:10", "a:2") || !natLess("a", "ab") {
		t.Fatal("natLess")
	}
}

func TestLive(t *testing.T) {
	if _, err := os.Stat("/sys/block"); err != nil {
		t.Skip("no sysfs")
	}
	if _, err := New().Disks(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Instalación desde la ISO: la raíz es Btrfs (@rootfs en sda2) con más subvolúmenes
// montados (/.snapshots, /var/log). No son volúmenes de datos. Y un volumen Básico
// (Btrfs directo en el disco entero, sin md) lista su disco con el id estable.
func TestVolumesBtrfsRootAndBasic(t *testing.T) {
	inv := fixture(t)
	root := filepath.Dir(inv.Sys)
	tr := tree{t, root}
	tr.file("sys/block/sda/sda2/partition", []byte("2\n"))
	tr.link("sys/class/block/sda2", "../../block/sda/sda2")
	tr.link("sys/class/block/sdc", "../../block/sdc")
	tr.file("dev/sda2", nil)
	tr.file("dev/sdc", nil)
	tr.link("sys/fs/btrfs/aaaa1111-0000-4000-8000-000000000001/devices/sda2", "../../../../block/sda/sda2")
	tr.link("sys/fs/btrfs/bbbb2222-0000-4000-8000-000000000002/devices/sdc", "../../../../block/sdc")
	tr.file("proc/self/mountinfo", []byte(
		"29 1 0:30 /@rootfs / rw,relatime shared:1 - btrfs /dev/sda2 rw,subvol=/@rootfs\n"+
			"31 29 0:30 /@snapshots /.snapshots rw,relatime - btrfs /dev/sda2 rw,subvol=/@snapshots\n"+
			"32 29 0:30 /@var-log /var/log rw,relatime - btrfs /dev/sda2 rw,subvol=/@var-log\n"+
			"90 29 9:0 / /volume\\0401 rw - ext4 /dev/md0 rw\n"+
			"95 29 0:40 / /volume3 rw - btrfs /dev/sdc rw\n"))
	vols, err := inv.Volumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 2 {
		t.Fatalf("la raíz del sistema no es un volumen: %+v", vols)
	}
	var basic *Volume
	for i := range vols {
		if vols[i].Name == ".snapshots" || vols[i].Name == "log" || vols[i].Mount == "/var/log" {
			t.Fatalf("volumen fantasma: %+v", vols[i])
		}
		if vols[i].Mount == "/volume3" {
			basic = &vols[i]
		}
	}
	if basic == nil || basic.Profile != "basic" || basic.ID != "bbbb2222-0000-4000-8000-000000000002" {
		t.Fatalf("básico: %+v", vols)
	}
	if len(basic.Disks) != 1 || basic.Disks[0] != "scsi-0QEMU_QEMU_HARDDISK_drive-scsi1" {
		t.Fatalf("miembros del básico: %v", basic.Disks)
	}
	// Y el disco aparece como miembro de ese volumen.
	disks, _ := inv.Disks(context.Background())
	for _, d := range disks {
		if d.Dev == "sdc" && (d.Use != "member" || d.Volume != basic.ID) {
			t.Fatalf("sdc: %+v", d)
		}
		if d.Dev == "sda" && d.Use != "system" {
			t.Fatalf("sda: %+v", d)
		}
	}
}
