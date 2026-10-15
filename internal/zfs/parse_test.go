package zfs

import (
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Fixtures capturados en la VM de laboratorio (OpenZFS 2.3.9, Debian 13): pool fx
// (mirror + log + cache), fz (raidz1, con un disco offline en status2).
func fakeResolve(p string) string {
	p = strings.TrimSuffix(p, "-part1")
	i := strings.LastIndex(p, "scsi")
	return "sd" + p[i+4:]
}

func TestParseStatus(t *testing.T) {
	pools, err := ParseStatus(fixture(t, "status.json"), fakeResolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 1 || pools[0].Name != "fx" || pools[0].Health != "ONLINE" || pools[0].GUID != "10628678190683096599" {
		t.Fatalf("%+v", pools)
	}
	p := pools[0]
	if len(p.Vdevs) != 1 || p.Vdevs[0].Type != "mirror" || len(p.Vdevs[0].Disks) != 2 {
		t.Fatalf("vdevs %+v", p.Vdevs)
	}
	if d := p.Vdevs[0].Disks[0]; d.Name != "scsi-0QEMU_QEMU_HARDDISK_drive-scsi7" || d.Disk != "sd7" || d.State != "ONLINE" {
		t.Errorf("orden natural / disco: %+v", d)
	}
	if len(p.Logs) != 1 || p.Logs[0].Type != "disk" || len(p.Cache) != 1 || p.Cache[0].Disk != "sd13" {
		t.Errorf("log/cache %+v %+v", p.Logs, p.Cache)
	}
	if p.Scan != nil {
		t.Error("sin scrub todavía")
	}

	pools, err = ParseStatus(fixture(t, "status2.json"), fakeResolve)
	if err != nil || len(pools) != 2 {
		t.Fatal(err, len(pools))
	}
	fx, fz := pools[0], pools[1]
	if fx.Scan == nil || fx.Scan.Function != "scrub" || fx.Scan.State != "finished" || fx.Scan.Progress != 1 {
		t.Errorf("scan %+v", fx.Scan)
	}
	if fz.Health != "DEGRADED" || fz.Vdevs[0].Type != "raidz1" || fz.Vdevs[0].State != "DEGRADED" {
		t.Errorf("fz %+v", fz.Vdevs)
	}
	off := 0
	for _, d := range fz.Vdevs[0].Disks {
		if d.State == "OFFLINE" {
			off++
		}
	}
	if off != 1 || len(fz.Vdevs[0].Disks) != 3 || fz.Vdevs[0].Disks[0].Disk != "sd3" {
		t.Errorf("fz discos %+v", fz.Vdevs[0].Disks)
	}
}

func TestMergeListAndDatasets(t *testing.T) {
	pools, _ := ParseStatus(fixture(t, "status.json"), nil)
	if err := MergeList(pools, fixture(t, "list.json")); err != nil {
		t.Fatal(err)
	}
	if pools[0].Size != 2013265920 || pools[0].Free == 0 || pools[0].Frag == nil {
		t.Errorf("list %+v", pools[0])
	}
	ds, err := ParseDatasets(fixture(t, "zfs.json"))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]bool{}
	for _, d := range ds["fx"] {
		by[d.Name] = true
		switch d.Name {
		case "fx":
			if d.Mountpoint != "" || d.Mounted {
				t.Errorf("raíz sin montar: %+v", d)
			}
		case "fx/fxvol":
			if d.Mountpoint != "/fxvol" || !d.Mounted || d.Encrypted {
				t.Errorf("fxvol %+v", d)
			}
		case "fx/fxenc":
			if !d.Encrypted || d.KeyLoaded || d.Mounted {
				t.Errorf("fxenc bloqueado %+v", d)
			}
		case "fx/zvol/lun0":
			if d.Type != "volume" || d.Volsize != 128<<20 {
				t.Errorf("zvol %+v", d)
			}
		}
	}
	for _, n := range []string{"fx", "fx/fxvol", "fx/fxvol/shares", "fx/fxvol/shares/Fotos", "fx/fxenc", "fx/zvol", "fx/zvol/lun0"} {
		if !by[n] {
			t.Errorf("falta %s", n)
		}
	}
}

func TestParseImport(t *testing.T) {
	im := ParseImport(string(fixture(t, "import.txt")), fakeResolve)
	if len(im) != 1 || im[0].Name != "fz" || im[0].GUID != "15037992200479423202" || im[0].State != "ONLINE" {
		t.Fatalf("%+v", im)
	}
	if strings.Join(im[0].Disks, ",") != "sd11,sd12,sd3" {
		t.Errorf("discos %v", im[0].Disks)
	}
}
