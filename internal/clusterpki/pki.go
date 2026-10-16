// Package clusterpki es la PKI de un clúster de TangoOS (docs/17 §3, ADR 0012): la CA del
// clúster, la clave y el certificado de cada nodo, las huellas que se comparan a ojo al
// unir un nodo y los MAC del flujo de unión.
//
// La CA la genera el primer nodo que suma a otro. Cada nodo nuevo recibe, durante la
// unión, su certificado firmado y una copia de la CA (con su clave: cualquier nodo puede
// sumar a otro aunque el primero ya no esté). GlusterFS usa esos archivos para TLS de
// management y de E/S; tangod los usa para el canal de control entre nodos (mTLS).
package clusterpki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// Vigencias: la CA dura 20 años y los certificados de los nodos 10 (un NAS no rota solo).
const (
	caValidity   = 20 * 365 * 24 * time.Hour
	nodeValidity = 10 * 365 * 24 * time.Hour
)

func newKey() (*ecdsa.PrivateKey, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) }

func serial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

// KeyPEM codifica una clave privada.
func KeyPEM(k *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// ParseKey lee una clave privada EC en PEM.
func ParseKey(b []byte) (*ecdsa.PrivateKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("clave PEM inválida")
	}
	if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("la clave no es EC")
	}
	return ek, nil
}

// ParseCert lee un certificado en PEM (el primero).
func ParseCert(b []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, errors.New("certificado PEM inválido")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func certPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// NewCA crea la CA del clúster. id identifica al clúster (aparece en el CN).
func NewCA(id string) (certPEMb, keyPEMb []byte, err error) {
	k, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber: sn, Subject: pkix.Name{CommonName: "TangoOS cluster CA " + id, Organization: []string{"TangoOS"}},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(caValidity), IsCA: true, BasicConstraintsValid: true,
		MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		return nil, nil, err
	}
	kp, err := KeyPEM(k)
	return certPEM(der), kp, err
}

// NewNodeKey crea la clave privada de un nodo.
func NewNodeKey() ([]byte, error) {
	k, err := newKey()
	if err != nil {
		return nil, err
	}
	return KeyPEM(k)
}

// CSR arma el pedido de certificado de un nodo (CN = su nombre en el clúster).
func CSR(keyPEMb []byte, cn string) ([]byte, error) {
	k, err := ParseKey(keyPEMb)
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// ParseCSR lee y verifica la firma de un pedido.
func ParseCSR(b []byte) (*x509.CertificateRequest, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("pedido de certificado inválido")
	}
	r, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		return nil, err
	}
	if err := r.CheckSignature(); err != nil {
		return nil, fmt.Errorf("firma del pedido inválida: %w", err)
	}
	return r, nil
}

// Sign firma el certificado de un nodo: cliente y servidor (GlusterFS y el canal de
// control usan el mismo), con el CN pedido y los nombres o IPs del nodo como SAN.
func Sign(caCertPEM, caKeyPEM, csrPEM []byte, names []string) ([]byte, error) {
	ca, err := ParseCert(caCertPEM)
	if err != nil {
		return nil, err
	}
	caKey, err := ParseKey(caKeyPEM)
	if err != nil {
		return nil, err
	}
	req, err := ParseCSR(csrPEM)
	if err != nil {
		return nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber: sn, Subject: pkix.Name{CommonName: req.Subject.CommonName, Organization: []string{"TangoOS"}},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(nodeValidity), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true,
	}
	for _, n := range append([]string{req.Subject.CommonName}, names...) {
		if ip := net.ParseIP(n); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else if n != "" {
			tpl.DNSNames = append(tpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, req.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	return certPEM(der), nil
}

// SelfSigned es el certificado provisorio del canal de control antes de unirse a un
// clúster (la identidad la prueba el código de unión, no este certificado).
func SelfSigned(keyPEMb []byte, cn string) ([]byte, error) {
	k, err := ParseKey(keyPEMb)
	if err != nil {
		return nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tpl := &x509.Certificate{SerialNumber: sn, Subject: pkix.Name{CommonName: cn}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(nodeValidity),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		return nil, err
	}
	return certPEM(der), nil
}

// ParseCerts lee todos los certificados de un PEM. Mientras se rota la CA del clúster, los
// nodos confían en dos (la nueva y la anterior) en el mismo archivo.
func ParseCerts(b []byte) ([]*x509.Certificate, error) {
	var out []*x509.Certificate
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("certificado PEM inválido")
	}
	return out, nil
}

// Bundle junta certificados PEM en un archivo de confianza, sin repetir ninguno (el primero
// es el que se muestra como CA del clúster).
func Bundle(pems ...[]byte) []byte {
	var out []byte
	seen := map[string]bool{}
	for _, p := range pems {
		cs, err := ParseCerts(p)
		if err != nil {
			continue
		}
		for _, c := range cs {
			if seen[string(c.Raw)] {
				continue
			}
			seen[string(c.Raw)] = true
			out = append(out, certPEM(c.Raw)...)
		}
	}
	return out
}

// Verify comprueba que un certificado lo firmó la CA del clúster (cualquiera de las del
// archivo, durante una rotación).
func Verify(caCertPEM []byte, c *x509.Certificate) error {
	cas, err := ParseCerts(caCertPEM)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	for _, ca := range cas {
		pool.AddCert(ca)
	}
	_, err = c.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	return err
}

// ---- Huellas --------------------------------------------------------------------------

// Fingerprint es el SHA-256 de la clave pública (SPKI) de un certificado o de una clave,
// para mostrar: 8 grupos de 4 dígitos hexadecimales (los primeros 16 bytes). Es lo que el
// usuario compara entre las dos pantallas al unir un nodo.
func Fingerprint(spki []byte) string {
	sum := sha256.Sum256(spki)
	h := strings.ToUpper(hex.EncodeToString(sum[:16]))
	var g []string
	for i := 0; i < len(h); i += 4 {
		g = append(g, h[i:i+4])
	}
	return strings.Join(g, " ")
}

// CertFingerprint es la huella de la clave de un certificado PEM.
func CertFingerprint(certPEMb []byte) (string, error) {
	c, err := ParseCert(certPEMb)
	if err != nil {
		return "", err
	}
	return Fingerprint(c.RawSubjectPublicKeyInfo), nil
}

// KeyFingerprint es la huella de la clave pública de una clave privada PEM.
func KeyFingerprint(keyPEMb []byte) (string, error) {
	k, err := ParseKey(keyPEMb)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		return "", err
	}
	return Fingerprint(der), nil
}

// ---- Unión: código y MAC ----------------------------------------------------------------

var b32 = base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding)

// NewJoinCode es el código que muestra el nodo invitado y se escribe en el que lo suma:
// 10 caracteres (50 bits) en grupos de 5, sin letras que se confundan (I, O, 0, 1).
func NewJoinCode() string {
	var b [7]byte
	_, _ = rand.Read(b[:])
	s := b32.EncodeToString(b[:])[:10]
	return s[:5] + "-" + s[5:]
}

// NormalizeCode quita espacios y guiones y pasa a mayúsculas.
func NormalizeCode(c string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == ' ':
			return -1
		case r >= 'a' && r <= 'z':
			return r - 32
		}
		return r
	}, c)
}

// MAC autentica un paso de la unión con el código: los dos lados lo conocen y nadie más.
// Incluye la huella del certificado TLS que ve el nodo que suma (vinculación al canal):
// alguien en el medio que reenvía los mensajes presenta otro certificado y el MAC no
// coincide, así que no puede leer lo que viaja después (la clave de la CA).
func MAC(code, step string, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, []byte("tangoos-cluster-join\x00"+NormalizeCode(code)))
	m.Write([]byte(step))
	for _, p := range parts {
		var n [8]byte
		l := uint64(len(p))
		for i := range n {
			n[i] = byte(l >> (8 * i))
		}
		m.Write(n[:])
		m.Write(p)
	}
	return m.Sum(nil)
}

// CheckMAC compara en tiempo constante.
func CheckMAC(want, got []byte) bool { return hmac.Equal(want, got) }

// TLSFingerprint es el SHA-256 completo del certificado DER que presenta un servidor TLS.
func TLSFingerprint(der []byte) []byte {
	s := sha256.Sum256(der)
	return s[:]
}
