package tlscert

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateIsStable(t *testing.T) {
	dir := t.TempDir()
	c1, fp1, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Fatalf("certificate regenerated: %s != %s", fp1, fp2)
	}
	if !strings.HasPrefix(fp1, "sha-256 ") {
		t.Errorf("fingerprint format: %q", fp1)
	}
	leaf, err := x509.ParseCertificate(c1.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("localhost"); err != nil {
		t.Errorf("localhost not in SANs: %v", err)
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Errorf("127.0.0.1 not in SANs: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %v, want 0600", info.Mode().Perm())
	}
}
