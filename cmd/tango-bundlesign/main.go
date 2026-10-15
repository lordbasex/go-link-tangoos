// Command tango-bundlesign firma el escritorio que tangod sirve por TangoConnect
// (protocol/README.md §15, canal bundle). Corre en `make web`, después de copiar el
// build a internal/webui/dist: escribe .tango-manifest.json (texto exacto firmado),
// .tango-manifest.sig (Ed25519, base64) y .tango-manifest.pub (clave pública, base64).
//
// Clave: -key apunta a un archivo con la semilla Ed25519 (32 B en base64). Si no existe
// se genera (0600): así funciona la clave de desarrollo build/keys/dev-ed25519. La de
// release vive fuera del repo (ver docs/07) y se pasa con TANGO_BUNDLE_KEY.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lordbasex/go-link-tangoos/internal/connect"
)

func main() {
	dir := flag.String("dir", "internal/webui/dist", "bundle a firmar")
	keyPath := flag.String("key", envOr("TANGO_BUNDLE_KEY", "build/keys/dev-ed25519"), "semilla Ed25519 (base64)")
	version := flag.String("version", "dev", "versión del bundle")
	flag.Parse()
	if err := run(*dir, *keyPath, *version); err != nil {
		fmt.Fprintln(os.Stderr, "tango-bundlesign:", err)
		os.Exit(1)
	}
}

func run(dir, keyPath, version string) error {
	key, err := loadKey(keyPath)
	if err != nil {
		return err
	}
	m, err := connect.BuildManifest(os.DirFS(dir), version)
	if err != nil {
		return err
	}
	text, sig, err := connect.Sign(m, key)
	if err != nil {
		return err
	}
	pub := key.Public().(ed25519.PublicKey)
	for name, data := range map[string]string{
		connect.ManifestFile:  text,
		connect.SignatureFile: sig + "\n",
		connect.PublicKeyFile: base64.StdEncoding.EncodeToString(pub) + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("bundle firmado: %d archivos, clave %s\n", len(m.Files), connect.KeyID(pub))
	return nil
}

func loadKey(p string) (ed25519.PrivateKey, error) {
	if b, err := os.ReadFile(p); err == nil {
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("%s: semilla Ed25519 inválida", p)
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte(base64.StdEncoding.EncodeToString(seed)+"\n"), 0o600); err != nil {
		return nil, err
	}
	key := ed25519.NewKeyFromSeed(seed)
	pubPath := p + ".pub"
	_ = os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))+"\n"), 0o644)
	fmt.Fprintf(os.Stderr, "clave de desarrollo nueva en %s (pública en %s)\n", p, pubPath)
	return key, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
