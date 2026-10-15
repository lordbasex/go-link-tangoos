package rtc

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRemoteCertPersistsAndLasts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.pem")
	a, err := loadOrCreateCert(p)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(a.Expires()) < 5*365*24*time.Hour {
		t.Fatalf("vence demasiado pronto: %v", a.Expires())
	}
	b, err := loadOrCreateCert(p)
	if err != nil {
		t.Fatal(err)
	}
	fa, _ := a.GetFingerprints()
	fb, _ := b.GetFingerprints()
	if fa[0].Value != fb[0].Value {
		t.Fatal("el certificado cambió al recargar")
	}
}
