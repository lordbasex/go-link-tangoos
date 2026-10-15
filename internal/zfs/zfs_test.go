package zfs

import (
	"reflect"
	"strings"
	"testing"
)

func TestNames(t *testing.T) {
	for _, ok := range []string{"tank", "datos", "pool_1", "z"} {
		if err := ValidPoolName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Tank", "-tank", "mirror", "raidz2x", "c0t0", "log", "a-b", "tank/x", strings.Repeat("a", 33)} {
		if ValidPoolName(bad) == nil {
			t.Errorf("%q debería ser inválido", bad)
		}
	}
	if ValidVolumeName("volume5") != nil || ValidVolumeName("etc") == nil || ValidVolumeName("vol-1") == nil {
		t.Error("ValidVolumeName")
	}
	if ValidSnapName("2026-10-15T02-00-04Z.hourly") != nil || ValidSnapName("2026-10-15T02-00-04Z") != nil || ValidSnapName("x@y") == nil {
		t.Error("ValidSnapName")
	}
	if ValidByID("ata-QEMU_HARDDISK_TANGO-S3") != nil || ValidByID("../sda") == nil || ValidByID("-f") == nil {
		t.Error("ValidByID")
	}
}

func TestLayouts(t *testing.T) {
	cases := []struct {
		layout string
		n      int
		ok     bool
	}{
		{"stripe", 1, true}, {"mirror", 1, false}, {"mirror", 3, true}, {"raidz1", 2, false}, {"raidz1", 3, true},
		{"raidz2", 3, false}, {"raidz2", 4, true}, {"raidz3", 4, false}, {"raidz3", 5, true}, {"draid", 5, false},
	}
	for _, c := range cases {
		if err := CheckLayout(c.layout, c.n); (err == nil) != c.ok {
			t.Errorf("%s con %d discos: %v", c.layout, c.n, err)
		}
	}
	const G = uint64(1 << 30)
	if u := Usable("mirror", []uint64{4 * G, 2 * G}); u != 2*G {
		t.Errorf("mirror %d", u)
	}
	if u := Usable("raidz1", []uint64{2 * G, 2 * G, 4 * G}); u != 4*G {
		t.Errorf("raidz1 %d", u)
	}
	if u := Usable("raidz2", []uint64{G, G, G, G, G, G}); u != 4*G {
		t.Errorf("raidz2 %d", u)
	}
	if u := Usable("stripe", []uint64{G, 2 * G}); u != 3*G {
		t.Errorf("stripe %d", u)
	}
	if w := Wasted("raidz1", []uint64{2 * G, 2 * G, 4 * G}); w != 2*G {
		t.Errorf("wasted %d", w)
	}
	if Tolerance("mirror", 3) != 2 || Tolerance("raidz2", 6) != 2 || Tolerance("stripe", 4) != 0 {
		t.Error("Tolerance")
	}
}

func TestArgs(t *testing.T) {
	a, err := ArgsPoolCreate("tank", "mirror", []string{"ata-A", "ata-B"})
	if err != nil {
		t.Fatal(err)
	}
	want := "create -f -o ashift=12 -o autotrim=on -O compression=lz4 -O atime=off -O xattr=sa -O acltype=posixacl -O dnodesize=auto -O canmount=off -O mountpoint=none tank mirror /dev/disk/by-id/ata-A /dev/disk/by-id/ata-B"
	if strings.Join(a, " ") != want {
		t.Errorf("create:\n%s\n%s", strings.Join(a, " "), want)
	}
	if _, err := ArgsPoolCreate("tank", "mirror", []string{"ata-A", "ata-A"}); err == nil {
		t.Error("disco repetido")
	}
	a, _ = ArgsPoolCreate("tank", "stripe", []string{"ata-A"})
	if a[len(a)-2] != "tank" {
		t.Errorf("stripe sin palabra de vdev: %v", a)
	}
	a, err = ArgsVolumeCreate("tank", "volume3", VolumeOpts{Quota: 1 << 30, Encrypted: true})
	if err != nil {
		t.Fatal(err)
	}
	want = "create -u -o mountpoint=/volume3 -o canmount=on -o quota=1073741824 -o encryption=aes-256-gcm -o keyformat=passphrase -o keylocation=prompt tank/volume3"
	if strings.Join(a, " ") != want {
		t.Errorf("volume:\n%s", strings.Join(a, " "))
	}
	if d, err := ChildDataset("tank/volume3", "@shares/Fotos"); err != nil || d != "tank/volume3/shares/Fotos" {
		t.Errorf("child %q %v", d, err)
	}
	if _, err := ChildDataset("tank/volume3", "@snapshots/Fotos"); err == nil {
		t.Error("@snapshots no es un dataset")
	}
	if _, err := ChildDataset("tank/volume3", "@shares/../x"); err == nil {
		t.Error("traversal")
	}
	a, _ = ArgsAuxAdd("tank", "log", []string{"nvme-A", "nvme-B"})
	if !reflect.DeepEqual(a, []string{"add", "-f", "tank", "log", "mirror", "/dev/disk/by-id/nvme-A", "/dev/disk/by-id/nvme-B"}) {
		t.Errorf("log %v", a)
	}
	if _, err := ArgsZvolCreate("tank", "lun0", 100, false); err == nil {
		t.Error("zvol chico")
	}
	a, _ = ArgsZvolCreate("tank", "lun0", 1<<30, true)
	if strings.Join(a, " ") != "create -p -s -V 1073741824 tank/zvol/lun0" {
		t.Errorf("zvol %v", a)
	}
	a, _ = ArgsImport("123456789", "", true)
	if strings.Join(a, " ") != "import -d /dev/disk/by-id -N -o readonly=on 123456789" {
		t.Errorf("import %v", a)
	}
	if _, err := ArgsImport("-a", "", false); err == nil {
		t.Error("import -a")
	}
	if _, err := ArgsSnapshot("tank/volume3/shares/Fotos", "hoy"); err == nil {
		t.Error("snapshot con nombre libre")
	}
	if got := ArgsSetSize("tank/v", 0, 5); strings.Join(got, " ") != "set quota=none reservation=5 tank/v" {
		t.Errorf("set %v", got)
	}
}
