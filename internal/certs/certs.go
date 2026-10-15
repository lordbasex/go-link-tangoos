// Package certs administra el certificado TLS de tangod en caliente (protocol/README.md
// §12, docs/07 §3): arranca con el autofirmado de internal/tlscert y puede pasar a una
// CA local de TangoOS (ECDSA P-384, 10 años) que firma el certificado del servidor
// (397 días, el máximo que aceptan los navegadores). El cambio no requiere reiniciar:
// tls.Config.GetCertificate siempre devuelve el certificado vigente, también en los
// proxies de apps, que clonan esa configuración.
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/tlscert"
)

// Modos.
const (
	ModeSelfSigned = "selfsigned"
	ModeLocalCA    = "localca"
)

const (
	caValidity     = 10 * 365 * 24 * time.Hour
	serverValidity = 397 * 24 * time.Hour
	modeFile       = "mode"
)

// Info es exactamente el CertInfo del cliente.
type Info struct {
	Mode        string   `json:"mode"`
	Subject     string   `json:"subject"`
	SANs        []string `json:"sans"`
	NotAfter    string   `json:"notAfter"`
	Fingerprint string   `json:"fingerprint"`
}

// Manager guarda el certificado vigente.
type Manager struct {
	tlsDir, caDir string
	Hostname      func() string // por defecto os.Hostname

	mu   sync.RWMutex
	cert *tls.Certificate
	mode string
}

// Open carga (o crea) el certificado en <state>/tls; la CA vive en <state>/ca.
func Open(stateDir string) (*Manager, error) {
	m := &Manager{tlsDir: filepath.Join(stateDir, "tls"), caDir: filepath.Join(stateDir, "ca")}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// Reload vuelve a leer el certificado del disco.
func (m *Manager) Reload() error {
	cert, _, err := tlscert.LoadOrCreate(m.tlsDir)
	if err != nil {
		return err
	}
	mode := ModeSelfSigned
	if b, err := os.ReadFile(filepath.Join(m.tlsDir, modeFile)); err == nil && strings.TrimSpace(string(b)) == ModeLocalCA {
		mode = ModeLocalCA
	}
	if cert.Leaf == nil && len(cert.Certificate) > 0 {
		cert.Leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}
	m.mu.Lock()
	m.cert, m.mode = &cert, mode
	m.mu.Unlock()
	return nil
}

// GetCertificate es para tls.Config.
func (m *Manager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cert == nil {
		return nil, errors.New("sin certificado")
	}
	return m.cert, nil
}

// TLSConfig devuelve una configuración que siempre usa el certificado vigente.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: m.GetCertificate}
}

// Info describe el certificado vigente.
func (m *Manager) Info() Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inf := Info{Mode: m.mode, SANs: []string{}}
	if m.cert == nil || m.cert.Leaf == nil {
		return inf
	}
	l := m.cert.Leaf
	inf.Subject = l.Subject.CommonName
	inf.SANs = append(inf.SANs, l.DNSNames...)
	for _, ip := range l.IPAddresses {
		inf.SANs = append(inf.SANs, ip.String())
	}
	inf.NotAfter = l.NotAfter.UTC().Format(time.RFC3339)
	inf.Fingerprint = Fingerprint(l.Raw)
	return inf
}

// Fingerprint es el SHA-256 del DER en hex con dos puntos.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	var b strings.Builder
	b.WriteString("sha-256 ")
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i : i+2])
	}
	return b.String()
}

// CAPEM devuelve el certificado raíz de la CA local (sin la clave). Error si no hay CA.
func (m *Manager) CAPEM() (string, error) {
	b, err := os.ReadFile(filepath.Join(m.caDir, "ca.crt"))
	if err != nil {
		return "", errors.New("todavía no hay una CA local: activala primero")
	}
	return string(b), nil
}

// UseLocalCA crea la CA (si no existe) y un certificado de servidor firmado por ella,
// lo instala y lo recarga en caliente.
func (m *Manager) UseLocalCA() error {
	caCert, caKey, err := m.loadOrCreateCA()
	if err != nil {
		return err
	}
	host := m.hostname()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	dns, ips := Names(host)
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: host, Organization: []string{"TangoOS"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(serverValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})...)
	if err := writeAtomic(filepath.Join(m.tlsDir, "tangod.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(m.tlsDir, "tangod.crt"), chain, 0o644); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(m.tlsDir, modeFile), []byte(ModeLocalCA+"\n"), 0o600); err != nil {
		return err
	}
	return m.Reload()
}

func (m *Manager) loadOrCreateCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	crtPath, keyPath := filepath.Join(m.caDir, "ca.crt"), filepath.Join(m.caDir, "ca.key")
	if c, k, err := loadCA(crtPath, keyPath); err == nil {
		return c, k, nil
	}
	if err := os.MkdirAll(m.caDir, 0o700); err != nil {
		return nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	host := m.hostname()
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "TangoOS CA local (" + host + ")", Organization: []string{"TangoOS"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writeAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, nil, err
	}
	if err := writeAtomic(crtPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, nil, err
	}
	return loadCA(crtPath, keyPath)
}

func loadCA(crtPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cb, err := os.ReadFile(crtPath)
	if err != nil {
		return nil, nil, err
	}
	kb, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	cp, _ := pem.Decode(cb)
	kp, _ := pem.Decode(kb)
	if cp == nil || kp == nil {
		return nil, nil, errors.New("CA ilegible")
	}
	c, err := x509.ParseCertificate(cp.Bytes)
	if err != nil {
		return nil, nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(kp.Bytes)
	if err != nil {
		return nil, nil, err
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("clave de CA no ECDSA")
	}
	return c, ek, nil
}

// Names son los SAN: host, host.local, localhost y las IP de la máquina.
func Names(host string) ([]string, []net.IP) {
	dns := []string{host, host + ".local", "localhost"}
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			ips = append(ips, n.IP)
		}
	}
	return dns, ips
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

func writeAtomic(name string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// hostname lee /etc/hostname (lo actualiza systemd-hostnamed): tangod corre con su
// propio UTS namespace (ProtectHostname) y os.Hostname() puede estar desactualizado.
func (m *Manager) hostname() string {
	if m.Hostname != nil {
		return m.Hostname()
	}
	if b, err := os.ReadFile("/etc/hostname"); err == nil {
		if h := strings.TrimSpace(string(b)); h != "" {
			return h
		}
	}
	if h, _ := os.Hostname(); h != "" {
		return h
	}
	return "tango"
}
