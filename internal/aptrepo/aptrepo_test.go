package aptrepo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDeb arma un .deb mínimo (ar: debian-binary, control.tar.gz, data.tar.gz).
func fakeDeb(t *testing.T, control string) []byte {
	t.Helper()
	tgz := func(name, content string) []byte {
		var b bytes.Buffer
		zw := gzip.NewWriter(&b)
		tw := tar.NewWriter(zw)
		if name != "" {
			_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))})
			_, _ = tw.Write([]byte(content))
		}
		_ = tw.Close()
		_ = zw.Close()
		return b.Bytes()
	}
	var ar bytes.Buffer
	ar.WriteString("!<arch>\n")
	member := func(name string, data []byte) {
		fmt.Fprintf(&ar, "%-16s%-12d%-6d%-6d%-8s%-10d`\n", name, 0, 0, 0, "100644", len(data))
		ar.Write(data)
		if len(data)%2 == 1 {
			ar.WriteByte('\n')
		}
	}
	member("debian-binary", []byte("2.0\n"))
	member("control.tar.gz", tgz("./control", control+"\n"))
	member("data.tar.gz", tgz("", ""))
	return ar.Bytes()
}

func ctl(v string) string {
	return "Package: tangoos\nVersion: " + v + "\nArchitecture: amd64\nMaintainer: X <x@y>\nDepends: mdadm, btrfs-progs\nDescription: TangoOS\n multi línea"
}

func TestReadControl(t *testing.T) {
	got, err := ReadControl(bytes.NewReader(fakeDeb(t, ctl("1.2.3"))))
	if err != nil {
		t.Fatal(err)
	}
	if got != ctl("1.2.3") {
		t.Fatalf("control = %q", got)
	}
	if _, err := ReadControl(strings.NewReader("no")); err == nil {
		t.Fatal("aceptó un archivo que no es ar")
	}
}

func TestRepo(t *testing.T) {
	root, src := t.TempDir(), t.TempDir()
	write := func(v string) string {
		p := filepath.Join(src, "tangoos_"+v+"_amd64.deb")
		if err := os.WriteFile(p, fakeDeb(t, ctl(v)), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := AddToPool(root, "main", []string{write("0.1.0"), write("1:0.2.0")}); err != nil {
		t.Fatal(err)
	}
	// misma versión con otro contenido: rechazada
	p := filepath.Join(src, "otro.deb")
	_ = os.WriteFile(p, fakeDeb(t, ctl("0.1.0")+"\nX-Otro: 1"), 0o644)
	if err := AddToPool(root, "main", []string{p}); err == nil {
		t.Fatal("pisó una versión publicada")
	}
	if _, err := os.Stat(filepath.Join(root, "pool/main/t/tangoos/tangoos_0.2.0_amd64.deb")); err != nil {
		t.Fatal("la época no se sacó del nombre:", err)
	}
	// un índice viejo que Release ya no lista desaparece
	_ = os.MkdirAll(filepath.Join(root, "dists/stable/main/i18n"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "dists/stable/main/i18n/Translation-en"), []byte("x"), 0o644)

	rel, err := Write(root, Defaults(), time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "dists/stable/main/i18n")); err == nil {
		t.Fatal("quedaron índices viejos")
	}
	r, _ := os.ReadFile(rel)
	for _, want := range []string{"Suite: stable", "Date: Thu, 15 Oct 2026 12:00:00 UTC", "Acquire-By-Hash: no", "main/binary-amd64/Packages.gz", "SHA256:"} {
		if !strings.Contains(string(r), want) {
			t.Errorf("Release sin %q:\n%s", want, r)
		}
	}
	if strings.Contains(string(r), "Translation") {
		t.Error("Release lista Translation")
	}
	idx, _ := os.ReadFile(filepath.Join(root, "dists/stable/main/binary-amd64/Packages"))
	if n := strings.Count(string(idx), "Package: tangoos"); n != 2 {
		t.Fatalf("Packages con %d entradas:\n%s", n, idx)
	}
	for _, want := range []string{"Filename: pool/main/t/tangoos/tangoos_0.1.0_amd64.deb", "SHA256: ", " multi línea\nFilename:"} {
		if !strings.Contains(string(idx), want) {
			t.Errorf("Packages sin %q", want)
		}
	}
	if !strings.Contains(string(idx), "\n\nPackage:") {
		t.Error("los párrafos no están separados por una línea en blanco")
	}
}

// install.sh embebe la clave pública del repositorio: tiene que ser la de build/keys.
func TestInstallScriptKey(t *testing.T) {
	script, err := os.ReadFile("../../web/apps/connect/public/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile("../../build/keys/tangoos-archive-keyring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(script), "KEY_B64='")
	b64, _, ok2 := strings.Cut(rest, "'")
	if !ok || !ok2 {
		t.Fatal("install.sh sin KEY_B64")
	}
	got, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64), ""))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, key) {
		t.Fatal("la clave de install.sh no es build/keys/tangoos-archive-keyring.gpg")
	}
}
