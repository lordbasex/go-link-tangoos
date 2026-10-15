// Command tango-aptrepo arma el repositorio APT firmado de TangoOS (docs/08 §Instalación por script):
// copia los .deb al pool, genera Packages(.gz) y Release, y los firma con gpg (InRelease y
// Release.gpg). Lo publica scripts/publish-apt.sh en https://tangoos.go-link.org/apt.
//
//	go run ./cmd/tango-aptrepo -out dist/apt dist/tangoos_0.2.0_amd64.deb
//
// La clave privada vive fuera del repo, en un GNUPGHOME propio (-gnupghome, por defecto
// ~/.config/tangoos/apt-gnupg); la pública va en build/keys/tangoos-archive-keyring.gpg y el
// repo la sirve como /apt/tangoos-archive-keyring.gpg.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/aptrepo"
)

func main() {
	home, _ := os.UserHomeDir()
	out := flag.String("out", "dist/apt", "raíz del repositorio (pool/ y dists/)")
	gnupg := flag.String("gnupghome", envOr("TANGOOS_APT_GNUPGHOME", filepath.Join(home, ".config/tangoos/apt-gnupg")), "GNUPGHOME con la clave de firma")
	key := flag.String("key", envOr("TANGOOS_APT_KEY", "TangoOS Archive"), "clave de firma (uid o huella)")
	keyring := flag.String("keyring", "build/keys/tangoos-archive-keyring.gpg", "clave pública (binaria) que se publica junto al repo")
	suite := flag.String("suite", "stable", "suite (canal)")
	unsigned := flag.Bool("unsigned", false, "no firmar (solo para tests)")
	flag.Parse()

	if err := run(*out, *gnupg, *key, *keyring, *suite, *unsigned, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "tango-aptrepo:", err)
		os.Exit(1)
	}
}

func run(out, gnupg, key, keyring, suite string, unsigned bool, debs []string) error {
	c := aptrepo.Defaults()
	c.Suite, c.Codename = suite, suite
	if err := aptrepo.AddToPool(out, c.Component, debs); err != nil {
		return err
	}
	rel, err := aptrepo.Write(out, c, time.Now())
	if err != nil {
		return err
	}
	if !unsigned {
		if err := sign(gnupg, key, rel); err != nil {
			return err
		}
		b, err := os.ReadFile(keyring)
		if err != nil {
			return fmt.Errorf("clave pública: %w", err)
		}
		if err := os.WriteFile(filepath.Join(out, "tangoos-archive-keyring.gpg"), b, 0o644); err != nil {
			return err
		}
	}
	pkgs, err := aptrepo.Scan(out, c.Arch)
	if err != nil {
		return err
	}
	for _, p := range pkgs {
		fmt.Printf("  %s %s (%s)\n", p.Name, p.Version, p.Filename)
	}
	fmt.Printf("repositorio %s: %d paquete(s), suite %s\n", out, len(pkgs), suite)
	return nil
}

// sign escribe InRelease (clearsign) y Release.gpg (firma separada) junto al Release.
func sign(gnupg, key, rel string) error {
	dir := filepath.Dir(rel)
	for _, args := range [][]string{
		{"--clearsign", "-o", filepath.Join(dir, "InRelease"), rel},
		{"--armor", "--detach-sign", "-o", filepath.Join(dir, "Release.gpg"), rel},
	} {
		cmd := exec.Command("gpg", append([]string{"--homedir", gnupg, "--batch", "--yes", "--pinentry-mode", "loopback", "--passphrase", "", "--digest-algo", "SHA512", "--local-user", key}, args...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("gpg %s: %v: %s", args[0], err, stderr.String())
		}
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
