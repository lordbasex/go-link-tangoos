// Package tlscert loads tangod's TLS certificate, creating a self-signed
// one on first run. This is the bootstrap step in docs/07 §3: it only
// exists so the first connection is encrypted; trusted certificates
// (ACME, local CA) replace it later.
package tlscert

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
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	certFile = "tangod.crt"
	keyFile  = "tangod.key"
	validity = 825 * 24 * time.Hour // the longest browsers accept
)

// LoadOrCreate returns the certificate stored in dir, creating a new
// self-signed one when none exists. The returned fingerprint is the
// SHA-256 of the DER certificate, shown to the user for verification.
func LoadOrCreate(dir string) (tls.Certificate, string, error) {
	crt, key := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	cert, err := tls.LoadX509KeyPair(crt, key)
	if errors.Is(err, fs.ErrNotExist) {
		if err := create(dir, crt, key); err != nil {
			return tls.Certificate{}, "", err
		}
		cert, err = tls.LoadX509KeyPair(crt, key)
	}
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("load certificate: %w", err)
	}
	sum := sha256.Sum256(cert.Certificate[0])
	return cert, fingerprint(sum[:]), nil
}

func create(dir, crtPath, keyPath string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "tango"
	}
	dns, ips := names(host)
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host, Organization: []string{"TangoOS"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	// Key first: a certificate without its key is useless, and a crash
	// between the two writes must not leave a cert that LoadOrCreate trusts.
	if err := writeAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	return writeAtomic(crtPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// names lists the host name, its .local mDNS name, localhost and every
// non-link-local address of the machine.
func names(host string) ([]string, []net.IP) {
	dns := []string{host, "localhost"}
	if !strings.HasSuffix(host, ".local") {
		dns = append(dns, host+".local")
	}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
			continue
		}
		ips = append(ips, ipnet.IP)
	}
	return dns, ips
}

func writeAtomic(name string, data []byte, perm os.FileMode) error {
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

func fingerprint(sum []byte) string {
	h := strings.ToUpper(hex.EncodeToString(sum))
	parts := make([]string, 0, len(h)/2)
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return "sha-256 " + strings.Join(parts, ":")
}
