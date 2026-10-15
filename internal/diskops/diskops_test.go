package diskops

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- firmas y huella ------------------------------------------------------------------

func blank() []byte { return make([]byte, HeadSize) }

func TestDetectContent(t *testing.T) {
	cases := map[string]func([]byte){
		"vacío": func([]byte) {},
		"tabla de particiones GPT": func(h []byte) {
			copy(h[512:], "EFI PART")
		},
		"tabla de particiones MBR": func(h []byte) {
			h[510], h[511] = 0x55, 0xaa
			h[446+4] = 0x83
		},
		"ext4": func(h []byte) { binary.LittleEndian.PutUint16(h[0x438:], 0xef53) },
		"Btrfs": func(h []byte) {
			copy(h[btrfsSuperOff+0x40:], "_BHRfS_M")
		},
		"miembro de un RAID md («volume1»)": func(h []byte) {
			sb := h[4096:]
			binary.LittleEndian.PutUint32(sb[0:], mdMagic)
			binary.LittleEndian.PutUint32(sb[4:], 1)
			copy(sb[32:], "tangoos-dev:volume1")
		},
		"datos sin formato reconocido": func(h []byte) { h[100] = 7 },
	}
	for want, mk := range cases {
		h := blank()
		mk(h)
		if got := DetectContent(h, nil); got != want {
			t.Errorf("%q: got %q", want, got)
		}
	}
	// md + Btrfs juntos (un miembro de RAID1 con Btrfs encima)
	h := blank()
	binary.LittleEndian.PutUint32(h[4096:], mdMagic)
	binary.LittleEndian.PutUint32(h[4100:], 1)
	copy(h[btrfsSuperOff+0x40:], "_BHRfS_M")
	if got := DetectContent(h, nil); !strings.Contains(got, "RAID md") || !strings.Contains(got, "Btrfs") {
		t.Errorf("combinado: %q", got)
	}
}

func TestParseMDSuperAndBtrfsFSID(t *testing.T) {
	h := blank()
	sb := h[4096:]
	binary.LittleEndian.PutUint32(sb[0:], mdMagic)
	binary.LittleEndian.PutUint32(sb[4:], 1)
	copy(sb[16:32], []byte{0xde, 0xad, 0xbe, 0xef, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	copy(sb[32:], "host:volume1")
	binary.LittleEndian.PutUint32(sb[72:], 1)
	s, ok := mdSuperAt(h)
	if !ok || s.UUID != "deadbeef:01020304:05060708:090a0b0c" || s.Name != "host:volume1" || s.Level != 1 {
		t.Fatalf("md: %+v %v", s, ok)
	}
	if line := MDConfLine("volume1", s); line != "ARRAY /dev/md/volume1 metadata=1.2 UUID=deadbeef:01020304:05060708:090a0b0c name=host:volume1" {
		t.Fatalf("línea: %s", line)
	}

	copy(h[btrfsSuperOff+0x40:], "_BHRfS_M")
	copy(h[btrfsSuperOff+0x20:], []byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0, 1, 2, 3, 4, 5, 6, 7})
	if id, ok := BtrfsFSID(h); !ok || id != "12345678-9abc-def0-0001-020304050607" {
		t.Fatalf("fsid %q %v", id, ok)
	}
}

func TestFingerprintChanges(t *testing.T) {
	h, tl := blank(), make([]byte, TailSize)
	base := Fingerprint(1<<30, h, tl)
	if Fingerprint(1<<30, h, tl) != base {
		t.Fatal("no determinística")
	}
	if Fingerprint(2<<30, h, tl) == base {
		t.Fatal("el tamaño no cuenta")
	}
	h[512] = 'E' // aparece una tabla de particiones
	if Fingerprint(1<<30, h, tl) == base {
		t.Fatal("la cabecera no cuenta")
	}
	h[512] = 0
	tl[len(tl)-1] = 1 // copia de GPT al final
	if Fingerprint(1<<30, h, tl) == base {
		t.Fatal("la cola no cuenta")
	}
}

// ---- nombres -------------------------------------------------------------------------------

func TestNames(t *testing.T) {
	for _, ok := range []string{"volume1", "datos", "v2"} {
		if err := ValidVolumeName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Volume1", "vol-1", "1vol", "etc", "boot", "a/b", "../x", strings.Repeat("a", 33)} {
		if ValidVolumeName(bad) == nil {
			t.Errorf("%q debería ser inválido", bad)
		}
	}
	for _, ok := range []string{"Familia", "Fotos_2026", "media.tv", "homes"} {
		if err := ValidShareName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Mis fotos", "../etc", "a/b", "global", ".oculta", "x[y]"} {
		if ValidShareName(bad) == nil {
			t.Errorf("%q debería ser inválido", bad)
		}
	}
	if ValidMount("/volume1") != nil || ValidMount("/etc") == nil || ValidMount("/volume1/x") == nil || ValidMount("volume1") == nil {
		t.Error("ValidMount")
	}
	if ValidSubvolPath("@shares/Familia") != nil || ValidSubvolPath("@shares/../x") == nil || ValidSubvolPath("x") == nil {
		t.Error("ValidSubvolPath")
	}
	ts := time.Date(2026, 10, 14, 21, 5, 9, 0, time.UTC)
	n := SnapshotName(ts)
	if back, ok := ParseSnapshotName(n); !ok || !back.Equal(ts) || ValidSnapshotName(n) != nil {
		t.Fatalf("snapshot %s", n)
	}
}

// ---- unidades, mdadm.conf y Samba ------------------------------------------------------

func TestMountUnit(t *testing.T) {
	u := MountUnit("volume1", "1234-uuid")
	for _, want := range []string{"What=/dev/disk/by-uuid/1234-uuid", "Where=/volume1", "Type=btrfs", "WantedBy=multi-user.target"} {
		if !strings.Contains(u, want) {
			t.Errorf("falta %q", want)
		}
	}
	if strings.Contains(u, "After=local-fs.target") {
		t.Error("After=local-fs.target arma un ciclo de orden con las .mount")
	}
	if MountUnitName("volume1") != "volume1.mount" {
		t.Error("nombre de unidad")
	}
}

func TestMergeMDConf(t *testing.T) {
	conf := "HOMEHOST <system>\nMAILADDR root\n"
	a := MergeMDConf(conf, "volume1", "ARRAY /dev/md/volume1 metadata=1.2 UUID=a name=h:volume1")
	if !strings.Contains(a, "MAILADDR root") || strings.Count(a, "ARRAY") != 1 {
		t.Fatalf("agregar:\n%s", a)
	}
	b := MergeMDConf(a, "volume1", "ARRAY /dev/md/volume1 metadata=1.2 UUID=b name=h:volume1")
	if strings.Count(b, "ARRAY") != 1 || !strings.Contains(b, "UUID=b") {
		t.Fatalf("reemplazar:\n%s", b)
	}
	c := MergeMDConf(b, "volume2", "ARRAY /dev/md/volume2 metadata=1.2 UUID=c name=h:volume2")
	if strings.Count(c, "ARRAY") != 2 {
		t.Fatalf("segundo array:\n%s", c)
	}
	if !strings.HasPrefix(MergeMDConf("", "v", "ARRAY /dev/md/v x"), "#") {
		t.Error("archivo nuevo sin encabezado")
	}
}

func TestSamba(t *testing.T) {
	sec := SambaShare("Familia", "/volume1/@shares/Familia", "Lo de\n[global] casa")
	if !strings.Contains(sec, "[Familia]") || !strings.Contains(sec, "path = /volume1/@shares/Familia") || strings.Contains(sec, "[global]") {
		t.Fatalf("sección:\n%s", sec)
	}
	idx := SambaIndex([]string{"fotos.conf", "familia.conf"})
	if strings.Index(idx, "familia.conf") > strings.Index(idx, "fotos.conf") {
		t.Error("índice sin ordenar")
	}
	conf := "[global]\n   workgroup = WORKGROUP\n"
	c1, changed := EnsureSambaInclude(conf)
	if !changed || !strings.HasSuffix(c1, SambaIncludeLine+"\n") {
		t.Fatalf("include:\n%s", c1)
	}
	if c2, changed := EnsureSambaInclude(c1); changed || c2 != c1 {
		t.Fatal("no es idempotente")
	}
}

// ---- elegibilidad sobre un sysfs de mentira -----------------------------------------------

type tree struct {
	t    *testing.T
	root string
}

func (tr tree) file(rel, data string) {
	p := filepath.Join(tr.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr tree) link(rel, target string) {
	p := filepath.Join(tr.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		tr.t.Fatal(err)
	}
}

// Como la VM: sda sistema (/ en sda1), sdb..sde datos; sdc es miembro de md127 con
// Btrfs montado en /volume1 (major:minor anónimo, como reporta Btrfs); sdd tiene
// una partición montada; sde libre; sdf removible.
func eligFixture(t *testing.T) Env {
	tr := tree{t, t.TempDir()}
	for _, d := range []string{"sda", "sdb", "sdc", "sdd", "sde", "sdf"} {
		tr.file("sys/block/"+d+"/ro", "0\n")
		tr.file("sys/block/"+d+"/removable", "0\n")
		tr.file("sys/block/"+d+"/holders/.keep", "")
		_ = os.Remove(filepath.Join(tr.root, "sys/block", d, "holders/.keep"))
		tr.link("sys/class/block/"+d, "../../block/"+d)
	}
	tr.file("sys/block/sdf/removable", "1\n")
	tr.file("sys/block/sda/sda1/partition", "1\n")
	tr.link("sys/dev/block/8:1", "../../block/sda/sda1")
	tr.file("sys/block/sdd/sdd1/partition", "1\n")
	tr.link("sys/dev/block/8:49", "../../block/sdd/sdd1")
	// md127 sobre sdc
	tr.link("sys/block/sdc/holders/md127", "../../md127")
	tr.link("sys/block/md127/slaves/sdc", "../../sdc")
	tr.link("sys/class/block/md127", "../../block/md127")
	tr.link("dev/md127", "/nonexistent") // reemplazado abajo
	_ = os.Remove(filepath.Join(tr.root, "dev/md127"))
	tr.file("dev/md127", "")
	tr.file("proc/1/mountinfo", strings.Join([]string{
		"25 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw",
		"26 25 0:23 / /proc rw - proc proc rw",
		"40 25 0:45 / /volume1 rw,noatime shared:20 - btrfs /dev/md127 rw,space_cache=v2,subvolid=5,subvol=/",
		"41 25 8:49 / /mnt/usb rw - ext4 /dev/sdd1 rw",
	}, "\n")+"\n")
	return Env{Sys: filepath.Join(tr.root, "sys"), Proc: filepath.Join(tr.root, "proc"), Dev: filepath.Join(tr.root, "dev")}
}

func TestEligibility(t *testing.T) {
	e := eligFixture(t)
	if !e.SystemDisks()["sda"] || len(e.SystemDisks()) != 1 {
		t.Fatalf("sistema: %v", e.SystemDisks())
	}
	m := e.MountedDisks()
	if !m["sdc"] || !m["sdd"] || m["sde"] {
		t.Fatalf("montados (Btrfs anónimo sobre md incluido): %v", m)
	}
	want := map[string]string{
		"sda":   "es el disco del sistema",
		"sdc":   "tiene particiones montadas", // montado vía md127
		"sdd":   "tiene particiones montadas",
		"sde":   "",
		"sdf":   "es removible (USB): solo sirve como destino de respaldo",
		"sdz":   "el disco ya no está",
		"../sd": "nombre de dispositivo inválido",
	}
	for disk, reason := range want {
		ok, got := e.Eligible(disk)
		if ok != (reason == "") || got != reason {
			t.Errorf("%s: ok=%v %q, esperado %q", disk, ok, got, reason)
		}
	}
	if src, fs, ok := e.MountSource("/volume1"); !ok || src != "/dev/md127" || fs != "btrfs" {
		t.Errorf("MountSource: %s %s %v", src, fs, ok)
	}
}

func TestEligibilityHolders(t *testing.T) {
	e := eligFixture(t)
	// sde pasa a ser miembro de un md sin montar: tampoco es elegible.
	if err := os.Symlink("../../md5", filepath.Join(e.Sys, "block/sde/holders/md5")); err != nil {
		t.Fatal(err)
	}
	if ok, why := e.Eligible("sde"); ok || why != "ya es miembro de un RAID o de otro dispositivo" {
		t.Fatalf("holders: %v %q", ok, why)
	}
}
