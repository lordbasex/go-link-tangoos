package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRead(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"sys/class/dmi/id/sys_vendor":   "QEMU\n",
		"sys/class/dmi/id/product_name": "Standard PC (Q35 + ICH9, 2009)\n",
		"proc/cpuinfo":                  "processor\t: 0\nmodel name\t: Intel(R) N150\n",
		"proc/meminfo":                  "MemTotal:       16384000 kB\n",
		"proc/uptime":                   "1234.56 999.0\n",
		"etc/debian_version":            "13.7\n",
		"etc/os-release":                "PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nNAME=\"Debian GNU/Linux\"\nVERSION_CODENAME=trixie\n",
	}
	for p, s := range files {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in := Reader{Root: root, Version: "0.1.0-dev"}.Read()
	if in.Model != "QEMU Standard PC (Q35 + ICH9, 2009)" || in.CPU != "Intel(R) N150" || in.MemTotal != 16384000*1024 {
		t.Fatalf("%+v", in)
	}
	if in.Debian != "Debian 13.7 (trixie)" || in.UptimeSec != 1234 || in.Version != "0.1.0-dev" || in.Serial != "" {
		t.Fatalf("%+v", in)
	}
}

// El nombre sale de /etc/hostname en cada llamada (setup.complete lo cambia y tangod,
// con ProtectHostname, no ve el cambio por os.Hostname hasta reiniciar).
func TestHostnameFromEtc(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Reader{Root: root}
	if err := os.WriteFile(filepath.Join(root, "etc/hostname"), []byte("tango-viejo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h := r.Read().Hostname; h != "tango-viejo" {
		t.Fatalf("%q", h)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/hostname"), []byte("tango-lab\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h := r.Read().Hostname; h != "tango-lab" {
		t.Fatalf("no tomó el nombre nuevo: %q", h)
	}
}
