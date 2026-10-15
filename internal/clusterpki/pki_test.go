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

func TestBundleDuringRotation(t *testing.T) {
	oldC, oldK, _ := NewCA("0001")
	newC, newK, _ := NewCA("0002")
	key, _ := NewNodeKey()
	csr, _ := CSR(key, "n1")
	oldCert, _ := Sign(oldC, oldK, csr, nil)
	newCert, _ := Sign(newC, newK, csr, nil)
	co, _ := ParseCert(oldCert)
	cn, _ := ParseCert(newCert)

	b := Bundle(newC, oldC, newC) // repetida: entra una vez
	if cs, err := ParseCerts(b); err != nil || len(cs) != 2 {
		t.Fatalf("bundle: %d %v", len(cs), err)
	}
	if Verify(b, co) != nil || Verify(b, cn) != nil {
		t.Fatal("con las dos CA, los dos certificados tienen que valer")
	}
	// La huella que se muestra es la de la primera.
	if f1, _ := CertFingerprint(b); f1 != func() string { f, _ := CertFingerprint(newC); return f }() {
		t.Fatal("la huella del bundle no es la de la primera CA")
	}
	// Al cerrar la rotación queda solo la nueva: el certificado viejo deja de valer.
	if Verify(newC, co) == nil {
		t.Fatal("la CA nueva validó un certificado de la anterior")
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
