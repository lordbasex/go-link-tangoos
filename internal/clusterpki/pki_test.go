package clusterpki

import (
	"crypto/tls"
	"strings"
	"testing"
)

func TestCASignVerify(t *testing.T) {
	caC, caK, err := NewCA("abcd")
	if err != nil {
		t.Fatal(err)
	}
	key, _ := NewNodeKey()
	csr, err := CSR(key, "tg-gluster2")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := Sign(caC, caK, csr, []string{"172.17.0.49"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := ParseCert(cert)
	if c.Subject.CommonName != "tg-gluster2" || len(c.IPAddresses) != 1 || c.IPAddresses[0].String() != "172.17.0.49" {
		t.Fatalf("cert: %v %v", c.Subject, c.IPAddresses)
	}
	if err := Verify(caC, c); err != nil {
		t.Fatal(err)
	}
	// otra CA no lo valida
	other, _, _ := NewCA("zzzz")
	if Verify(other, c) == nil {
		t.Fatal("una CA ajena validó el certificado")
	}
	// clave y certificado forman un par TLS
	if _, err := tls.X509KeyPair(cert, key); err != nil {
		t.Fatal(err)
	}
	kf, _ := KeyFingerprint(key)
	cf, _ := CertFingerprint(cert)
	if kf != cf || len(strings.Fields(kf)) != 8 {
		t.Fatalf("huellas %q %q", kf, cf)
	}
}

func TestCSRTamper(t *testing.T) {
	key, _ := NewNodeKey()
	csr, _ := CSR(key, "n1")
	bad := []byte(strings.Replace(string(csr), "A", "B", 1))
	if _, err := ParseCSR(bad); err == nil {
		t.Fatal("aceptó un pedido alterado")
	}
}

func TestJoinCodeAndMAC(t *testing.T) {
	c := NewJoinCode()
	if len(c) != 11 || c[5] != '-' || strings.ContainsAny(c, "IO01") {
		t.Fatalf("código %q", c)
	}
	m := MAC(c, "hello", []byte("nonce"), []byte("fp"))
	if !CheckMAC(m, MAC(strings.ToLower(strings.ReplaceAll(c, "-", " ")), "hello", []byte("nonce"), []byte("fp"))) {
		t.Fatal("el código normalizado no da el mismo MAC")
	}
	if CheckMAC(m, MAC(c, "hello", []byte("nonce"), []byte("otro"))) {
		t.Fatal("el MAC no depende de la huella del canal")
	}
	// las partes no se pueden correr de lugar (longitud incluida)
	if CheckMAC(MAC(c, "s", []byte("ab"), []byte("c")), MAC(c, "s", []byte("a"), []byte("bc"))) {
		t.Fatal("ambigüedad entre partes")
	}
}
