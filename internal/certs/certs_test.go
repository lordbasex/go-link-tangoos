package certs

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalCAHotReload(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Hostname = func() string { return "tango" }
	before := m.Info()
	if before.Mode != ModeSelfSigned || before.Fingerprint == "" {
		t.Fatalf("initial info: %+v", before)
	}
	if _, err := m.CAPEM(); err == nil {
		t.Fatal("CA PEM must fail before enabling the CA")
	}

	// Servidor TLS que usa GetCertificate: debe cambiar de certificado sin reiniciarse.
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	srv.TLS = m.TLSConfig()
	srv.StartTLS()
	defer srv.Close()

	if err := m.UseLocalCA(); err != nil {
		t.Fatal(err)
	}
	after := m.Info()
	if after.Mode != ModeLocalCA || after.Fingerprint == before.Fingerprint || after.Subject != "tango" {
		t.Fatalf("after: %+v", after)
	}
	caPEM, err := m.CAPEM()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		t.Fatal("bad CA PEM")
	}
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost"}}}
	resp, err := c.Get("https://127.0.0.1:" + port)
	if err != nil {
		t.Fatalf("client trusting only the local CA failed: %v", err)
	}
	resp.Body.Close()

	// CA: P-384, 10 años, clave 0600.
	b, _ := os.ReadFile(filepath.Join(dir, "ca", "ca.crt"))
	p, _ := pem.Decode(b)
	ca, _ := x509.ParseCertificate(p.Bytes)
	if !ca.IsCA || ca.NotAfter.Sub(ca.NotBefore).Hours() < 24*365*9 {
		t.Fatalf("CA: isCA=%v validity=%v", ca.IsCA, ca.NotAfter.Sub(ca.NotBefore))
	}
	st, _ := os.Stat(filepath.Join(dir, "ca", "ca.key"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("CA key perm %v", st.Mode().Perm())
	}
	// Reabrir conserva el modo.
	m2, _ := Open(dir)
	if m2.Info().Mode != ModeLocalCA {
		t.Fatal("mode not persisted")
	}
}
