package smart

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// ataEntry escribe una entrada de 12 bytes de la página de datos SMART.
func ataEntry(page []byte, slot int, id, value, worst uint8, raw uint64) {
	e := page[2+slot*12:]
	e[0] = id
	e[3], e[4] = value, worst
	for i := 0; i < 6; i++ {
		e[5+i] = byte(raw >> (8 * i))
	}
}

func thrEntry(page []byte, slot int, id, thr uint8) {
	e := page[2+slot*12:]
	e[0], e[1] = id, thr
}

func TestParseATA(t *testing.T) {
	data := make([]byte, SectorSize)
	thr := make([]byte, SectorSize)
	ataEntry(data, 0, 5, 100, 100, 8)                     // 8 sectores reasignados
	ataEntry(data, 1, 9, 72, 72, 0x0012_0000_5B3A)        // horas en los 32 bits bajos (0x5B3A = 23354)
	ataEntry(data, 2, 194, 64, 45, 0x0000_2D00_1400_0024) // 36 °C (byte bajo)
	ataEntry(data, 3, 197, 100, 100, 0)                   // sin pendientes
	ataEntry(data, 4, 198, 100, 100, 0)                   // sin incorregibles
	ataEntry(data, 5, 231, 4, 4, 0)                       // atributo de fabricante debajo del umbral
	thrEntry(thr, 0, 5, 10)
	thrEntry(thr, 5, 231, 10)

	r, err := ParseATA(data, thr)
	if err != nil {
		t.Fatal(err)
	}
	if r.TempC == nil || *r.TempC != 36 {
		t.Fatalf("temp = %v", r.TempC)
	}
	if r.PowerOnH == nil || *r.PowerOnH != 0x5B3A {
		t.Fatalf("horas = %v", r.PowerOnH)
	}
	if r.Reallocated == nil || *r.Reallocated != 8 {
		t.Fatalf("reasignados = %v", r.Reallocated)
	}
	if !r.AnyFailing {
		t.Fatal("el atributo 231 debajo del umbral tiene que marcar falla")
	}
	if got := HealthATA(r, false); got != "crit" {
		t.Fatalf("health = %s", got)
	}
	// Sin el atributo fallando: reasignados > 0 → warn.
	ataEntry(data, 5, 0, 0, 0, 0)
	r, _ = ParseATA(data, thr)
	if got := HealthATA(r, false); got != "warn" {
		t.Fatalf("health = %s", got)
	}
	if got := HealthATA(r, true); got != "crit" {
		t.Fatalf("RETURN STATUS fallido debe ser crit, got %s", got)
	}
	if _, err := ParseATA(data[:100], nil); err == nil {
		t.Fatal("página corta debe fallar")
	}
}

func TestParseNVMe(t *testing.T) {
	b := make([]byte, SectorSize)
	b[0] = 0
	binary.LittleEndian.PutUint16(b[1:3], 314) // 41 °C
	b[5] = 3
	binary.LittleEndian.PutUint64(b[128:], 3120)
	binary.LittleEndian.PutUint64(b[160:], 0)
	r, err := ParseNVMe(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.TempC != 41 || r.PowerOnH != 3120 || r.PercentageUsed != 3 {
		t.Fatalf("%+v", r)
	}
	if HealthNVMe(r) != "ok" {
		t.Fatal("ok esperado")
	}
	b[0] = 0x04 // reliability degraded
	r, _ = ParseNVMe(b)
	if HealthNVMe(r) != "crit" {
		t.Fatal("critical warning debe ser crit")
	}
	b[0], b[5] = 0, 95
	r, _ = ParseNVMe(b)
	if HealthNVMe(r) != "warn" {
		t.Fatal("desgaste 95 % debe ser warn")
	}
}

func TestParseReturnStatus(t *testing.T) {
	mk := func(mid, high byte) []byte {
		s := make([]byte, 22)
		s[0], s[7] = 0x72, 14
		d := s[8:]
		d[0], d[1] = 0x09, 0x0C
		d[9], d[11] = mid, high
		return s
	}
	if failed, ok := ParseReturnStatus(mk(0x4F, 0xC2)); failed || !ok {
		t.Fatal("4F/C2 = ok")
	}
	if failed, ok := ParseReturnStatus(mk(0xF4, 0x2C)); !failed || !ok {
		t.Fatal("F4/2C = umbral superado")
	}
	if _, ok := ParseReturnStatus([]byte{0x70, 0, 5}); ok {
		t.Fatal("sense fijo no trae el descriptor")
	}
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	p := Paths{ByID: filepath.Join(dir, "by-id"), SysBlock: filepath.Join(dir, "sys")}
	if err := os.MkdirAll(p.ByID, 0o755); err != nil {
		t.Fatal(err)
	}
	// Un enlace a un dispositivo de caracteres (/dev/null) debe rechazarse.
	if err := os.Symlink("/dev/null", filepath.Join(p.ByID, "wwn-0xnull")); err != nil {
		t.Fatal(err)
	}
	// Un archivo común en by-id (no symlink) también.
	if err := os.WriteFile(filepath.Join(p.ByID, "plain"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"../../etc/passwd":   "INVALID_ARGS",
		"wwn-0x1/../../x":    "INVALID_ARGS",
		"":                   "INVALID_ARGS",
		"-rf":                "INVALID_ARGS",
		"no-existe":          "DEVICE_NOT_FOUND",
		"wwn-0xnull":         "DEVICE_INVALID",
		"plain":              "DEVICE_INVALID",
		"scsi-0QEMU..TANGO1": "INVALID_ARGS",
	}
	// Un dispositivo de bloque real que es un CD/DVD (tipo SCSI 5) se rechaza.
	// No hay dispositivos de bloque en el sandbox de tests: se cubre en la VM.
	for id, want := range cases {
		_, e := p.Resolve(id)
		if e == nil || e.Code != want {
			t.Errorf("Resolve(%q) = %v, want %s", id, e, want)
		}
	}
}
