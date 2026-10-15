// Package aptrepo arma el repositorio APT de TangoOS (https://tangoos.go-link.org/apt, docs/08):
// lee el control de cada .deb del pool y escribe dists/<suite>/main/binary-amd64/Packages(.gz) y
// dists/<suite>/Release. La firma (InRelease, Release.gpg) la hace cmd/tango-aptrepo con gpg.
//
// Release lista exactamente los índices que existen y declara Acquire-By-Hash: no. El portal está
// detrás de CloudFront, que responde index.html (200) a rutas inexistentes: apt no tiene que pedir
// nada que no esté acá (Translation, by-hash, Contents).
package aptrepo

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config describe el repositorio.
type Config struct {
	Origin, Label, Suite, Codename, Description string
	Component, Arch                             string
}

// Defaults es la configuración del repositorio público.
func Defaults() Config {
	return Config{
		Origin: "TangoOS", Label: "TangoOS", Suite: "stable", Codename: "stable",
		Description: "TangoOS — https://tangoos.go-link.org",
		Component:   "main", Arch: "amd64",
	}
}

// Package es un .deb del pool con su párrafo de control.
type Package struct {
	Name, Version, Arch string
	Control             string // párrafo de control, sin línea en blanco final
	Filename            string // relativo a la raíz del repo (pool/main/t/tangoos/…)
	Size                int64
	MD5, SHA1, SHA256   string
}

// ReadControl extrae el archivo "control" de un .deb (ar + control.tar.gz).
func ReadControl(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	magic := make([]byte, 8)
	if _, err := io.ReadFull(br, magic); err != nil || string(magic) != "!<arch>\n" {
		return "", errors.New("no es un archivo ar (.deb)")
	}
	hdr := make([]byte, 60)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			if errors.Is(err, io.EOF) {
				return "", errors.New("el .deb no tiene control.tar")
			}
			return "", err
		}
		name := strings.TrimSuffix(strings.TrimSpace(string(hdr[0:16])), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil || size < 0 {
			return "", fmt.Errorf("cabecera ar inválida para %q", name)
		}
		body := io.LimitReader(br, size)
		if strings.HasPrefix(name, "control.tar") {
			if name != "control.tar.gz" {
				return "", fmt.Errorf("%s: solo se soporta control.tar.gz (lo que genera nfpm)", name)
			}
			return controlFromTarGz(body)
		}
		if _, err := io.Copy(io.Discard, body); err != nil {
			return "", err
		}
		if size%2 == 1 { // los miembros ar se alinean a 2 bytes
			if _, err := br.Discard(1); err != nil {
				return "", err
			}
		}
	}
}

func controlFromTarGz(r io.Reader) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errors.New("control.tar.gz sin archivo control")
			}
			return "", err
		}
		if path.Clean(h.Name) == "control" {
			b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return "", err
			}
			return strings.TrimRight(string(b), "\n"), nil
		}
	}
}

// field devuelve el valor de un campo de un párrafo de control (sin continuaciones).
func field(control, name string) string {
	for _, l := range strings.Split(control, "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok && strings.EqualFold(k, name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// PoolPath es la ruta canónica de un paquete en el pool.
func PoolPath(component, name, version, arch string) string {
	v := version
	if i := strings.IndexByte(v, ':'); i >= 0 { // la época no va en el nombre del archivo
		v = v[i+1:]
	}
	return path.Join("pool", component, name[:1], name, fmt.Sprintf("%s_%s_%s.deb", name, v, arch))
}

// Inspect lee un .deb del disco: control y hashes. rel es su ruta relativa en el repo.
func Inspect(file, rel string) (Package, error) {
	f, err := os.Open(file)
	if err != nil {
		return Package{}, err
	}
	defer f.Close()
	ctl, err := ReadControl(f)
	if err != nil {
		return Package{}, fmt.Errorf("%s: %w", file, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Package{}, err
	}
	m, s1, s256 := md5.New(), sha1.New(), sha256.New()
	n, err := io.Copy(io.MultiWriter(m, s1, s256), f)
	if err != nil {
		return Package{}, err
	}
	p := Package{
		Name: field(ctl, "Package"), Version: field(ctl, "Version"), Arch: field(ctl, "Architecture"),
		Control: ctl, Filename: rel, Size: n,
		MD5: hex.EncodeToString(m.Sum(nil)), SHA1: hex.EncodeToString(s1.Sum(nil)), SHA256: hex.EncodeToString(s256.Sum(nil)),
	}
	if p.Name == "" || p.Version == "" || p.Arch == "" {
		return Package{}, fmt.Errorf("%s: el control no tiene Package/Version/Architecture", file)
	}
	return p, nil
}

// AddToPool copia cada .deb a su lugar en el pool de root (no pisa un archivo distinto con el
// mismo nombre: una versión publicada es inmutable).
func AddToPool(root, component string, debs []string) error {
	for _, d := range debs {
		p, err := Inspect(d, "")
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(PoolPath(component, p.Name, p.Version, p.Arch)))
		if old, err := Inspect(dst, ""); err == nil {
			if old.SHA256 != p.SHA256 {
				return fmt.Errorf("%s %s ya está en el pool con otro contenido: subí la versión", p.Name, p.Version)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		b, err := os.ReadFile(d)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Scan devuelve los paquetes del pool de root para arch (o "all"), ordenados.
func Scan(root, arch string) ([]Package, error) {
	var out []Package
	err := filepath.WalkDir(filepath.Join(root, "pool"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".deb") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		pkg, err := Inspect(p, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if pkg.Arch == arch || pkg.Arch == "all" {
			out = append(out, pkg)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Filename < out[j].Filename
	})
	return out, err
}

// PackagesIndex arma el archivo Packages.
func PackagesIndex(pkgs []Package) []byte {
	var b bytes.Buffer
	for i, p := range pkgs {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Control)
		fmt.Fprintf(&b, "\nFilename: %s\nSize: %d\nMD5sum: %s\nSHA1: %s\nSHA256: %s\n", p.Filename, p.Size, p.MD5, p.SHA1, p.SHA256)
	}
	return b.Bytes()
}

// Write genera dists/<suite>/… en root a partir del pool y devuelve la ruta del Release.
func Write(root string, c Config, now time.Time) (string, error) {
	pkgs, err := Scan(root, c.Arch)
	if err != nil {
		return "", err
	}
	if len(pkgs) == 0 {
		return "", errors.New("el pool está vacío")
	}
	dist := filepath.Join(root, "dists", c.Suite)
	bin := path.Join(c.Component, "binary-"+c.Arch)
	if err := os.RemoveAll(dist); err != nil { // sin restos de índices viejos que Release no liste
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dist, filepath.FromSlash(bin)), 0o755); err != nil {
		return "", err
	}
	idx := PackagesIndex(pkgs)
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	zw.ModTime = time.Time{} // reproducible
	if _, err := zw.Write(idx); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	files := map[string][]byte{
		path.Join(bin, "Packages"):    idx,
		path.Join(bin, "Packages.gz"): gz.Bytes(),
	}
	names := make([]string, 0, len(files))
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dist, filepath.FromSlash(n)), b, 0o644); err != nil {
			return "", err
		}
		names = append(names, n)
	}
	sort.Strings(names)
	rel := ReleaseFile(c, now, names, files)
	p := filepath.Join(dist, "Release")
	return p, os.WriteFile(p, rel, 0o644)
}

// ReleaseFile arma el Release con los hashes de files (nombres relativos a dists/<suite>).
func ReleaseFile(c Config, now time.Time, names []string, files map[string][]byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Origin: %s\nLabel: %s\nSuite: %s\nCodename: %s\n", c.Origin, c.Label, c.Suite, c.Codename)
	fmt.Fprintf(&b, "Date: %s\n", now.UTC().Format("Mon, 02 Jan 2006 15:04:05 UTC"))
	fmt.Fprintf(&b, "Architectures: %s\nComponents: %s\nDescription: %s\nAcquire-By-Hash: no\n", c.Arch, c.Component, c.Description)
	for _, alg := range []struct {
		name string
		sum  func([]byte) string
	}{
		{"MD5Sum", func(d []byte) string { s := md5.Sum(d); return hex.EncodeToString(s[:]) }},
		{"SHA1", func(d []byte) string { s := sha1.Sum(d); return hex.EncodeToString(s[:]) }},
		{"SHA256", func(d []byte) string { s := sha256.Sum256(d); return hex.EncodeToString(s[:]) }},
	} {
		b.WriteString(alg.name + ":\n")
		for _, n := range names {
			fmt.Fprintf(&b, " %s %16d %s\n", alg.sum(files[n]), len(files[n]), n)
		}
	}
	return b.Bytes()
}
