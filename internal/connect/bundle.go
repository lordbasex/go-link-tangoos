package connect

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/fileworker"
)

// Escritorio servido por el NAS (protocol/README.md §15, canal `bundle`). El
// manifiesto se firma en el build (cmd/tango-bundlesign) con la clave Ed25519 de
// release; tangod solo lo sirve. Un bundle sin firmar (build de desarrollo sin `make
// web`) se describe igual pero con signature vacía: el portal lo rechaza salvo en modo dev.

// Archivos del manifiesto dentro del bundle (los excluye el propio manifiesto).
const (
	ManifestFile  = ".tango-manifest.json"
	SignatureFile = ".tango-manifest.sig"
	PublicKeyFile = ".tango-manifest.pub"
)

// ManifestEntry es un archivo del bundle.
type ManifestEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest es lo que se firma.
type Manifest struct {
	V       int             `json:"v"`
	Version string          `json:"version"`
	Files   []ManifestEntry `json:"files"`
}

// BuildManifest recorre el bundle (sin los archivos del manifiesto) en orden.
func BuildManifest(bundle fs.FS, version string) (Manifest, error) {
	m := Manifest{V: 1, Version: version, Files: []ManifestEntry{}}
	err := fs.WalkDir(bundle, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasPrefix(path.Base(p), ".tango-manifest") {
			return nil
		}
		f, err := bundle.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, ManifestEntry{Path: p, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n})
		return nil
	})
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	return m, err
}

// Sign devuelve el texto exacto del manifiesto y su firma en base64.
func Sign(m Manifest, key ed25519.PrivateKey) (text, sig string, err error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", "", err
	}
	return string(b), base64.StdEncoding.EncodeToString(ed25519.Sign(key, b)), nil
}

// Verify comprueba la firma del texto del manifiesto.
func Verify(text, sig string, pub ed25519.PublicKey) bool {
	s, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, []byte(text), s)
}

// KeyID identifica una clave pública: hex(SHA-256(pub))[:16].
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:])[:16]
}

// ManifestResult es lo que devuelve bundle.manifest.
type ManifestResult struct {
	Version   string          `json:"version"`
	Files     []ManifestEntry `json:"files"`
	Signed    string          `json:"signed"`    // texto exacto que se firmó (verificar sobre esto)
	Signature string          `json:"signature"` // base64 Ed25519; "" = sin firmar (desarrollo)
	KeyID     string          `json:"keyId,omitempty"`
}

// Bundle sirve el escritorio embebido.
type Bundle struct {
	fs     fs.FS
	result ManifestResult
	byPath map[string]ManifestEntry
	next   atomic.Uint32
}

// NewBundle carga el manifiesto firmado si está en el bundle; si no, lo calcula
// (sin firma).
func NewBundle(bundle fs.FS, version string) (*Bundle, error) {
	b := &Bundle{fs: bundle, byPath: map[string]ManifestEntry{}}
	text, err1 := fs.ReadFile(bundle, ManifestFile)
	sig, err2 := fs.ReadFile(bundle, SignatureFile)
	if err1 == nil && err2 == nil {
		var m Manifest
		if err := json.Unmarshal(text, &m); err != nil {
			return nil, err
		}
		b.result = ManifestResult{Version: m.Version, Files: m.Files, Signed: string(text), Signature: strings.TrimSpace(string(sig))}
		if pub, err := fs.ReadFile(bundle, PublicKeyFile); err == nil {
			if k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(pub))); err == nil && len(k) == ed25519.PublicKeySize {
				if !Verify(b.result.Signed, b.result.Signature, k) {
					return nil, errors.New("connect: la firma del manifiesto del bundle no coincide con su clave")
				}
				b.result.KeyID = KeyID(k)
			}
		}
	} else {
		m, err := BuildManifest(bundle, version)
		if err != nil {
			return nil, err
		}
		t, _ := json.Marshal(m)
		b.result = ManifestResult{Version: m.Version, Files: m.Files, Signed: string(t)}
	}
	for _, f := range b.result.Files {
		b.byPath[f.Path] = f
	}
	return b, nil
}

// Manifest es el resultado de bundle.manifest.
func (b *Bundle) Manifest() ManifestResult { return b.result }

// BinSender es el canal bundle de una sesión (rtc.BinChan).
type BinSender interface {
	Send(frame []byte) error
	Wait(ctx context.Context, max uint64) error
}

// GetResult es lo que devuelve bundle.get.
type GetResult struct {
	ID      uint32 `json:"id"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Channel string `json:"channel"`
	Chunk   int    `json:"chunk"`
}

const bundleChunk = 60 << 10

// Get empieza a mandar un archivo del manifiesto por el canal bundle.
func (b *Bundle) Get(ch BinSender, p string) (GetResult, error) {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	e, ok := b.byPath[p]
	if !ok {
		return GetResult{}, errNotInBundle
	}
	data, err := fs.ReadFile(b.fs, p)
	if err != nil {
		return GetResult{}, err
	}
	id := b.next.Add(1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		time.Sleep(10 * time.Millisecond) // que la respuesta llegue antes que los datos
		frame := make([]byte, fileworker.FrameHeader+bundleChunk)
		for off := 0; off < len(data) || (off == 0 && len(data) == 0); {
			if err := ch.Wait(ctx, 2<<20); err != nil {
				return
			}
			end := min(off+bundleChunk, len(data))
			if err := ch.Send(fileworker.EncodeFrame(frame, id, uint64(off), data[off:end])); err != nil {
				return
			}
			if len(data) == 0 {
				return
			}
			off = end
		}
	}()
	return GetResult{ID: id, Size: e.Size, SHA256: e.SHA256, Channel: "bundle", Chunk: bundleChunk}, nil
}

var errNotInBundle = errors.New("ese archivo no es parte del escritorio")
