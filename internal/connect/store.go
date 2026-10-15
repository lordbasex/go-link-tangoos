// Package connect es TangoConnect (protocol/README.md §15): la conexión saliente del
// NAS a signalhub, los navegadores vinculados desde el portal, los enlaces públicos y
// el escritorio servido por el NAS (canal bundle).
package connect

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// App es el namespace de TangoOS en signalhub.
const App = "go-link-tangoos"

// Valores por defecto (protocol/README.md §15).
const (
	DefaultSignalURL = "wss://signal.go-link.org/ws?v=1"
	DefaultPortalURL = "https://tangoos.go-link.org"
)

// Link es un navegador vinculado desde el portal. Del token solo se guarda su SHA-256.
type Link struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	TokenHash string `json:"tokenHash"` // hex(SHA-256(token))
	CreatedAt string `json:"createdAt"`
	LastSeen  string `json:"lastSeen,omitempty"`
	IP        string `json:"ip,omitempty"`
}

// ShareLink es un enlace público a un archivo o carpeta.
type ShareLink struct {
	ID           string `json:"id"`
	Owner        string `json:"owner"` // usuario de TangoOS: los archivos se leen con su worker
	Path         string `json:"path"`  // ruta virtual (/Carpeta/sub/x)
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Size         int64  `json:"size"`
	Secret       string `json:"secret"`                 // va en el #fragmento del enlace; se guarda para poder volver a copiarlo
	PasswordHash string `json:"passwordHash,omitempty"` // argon2id
	CreatedAt    string `json:"createdAt"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	MaxDownloads int    `json:"maxDownloads,omitempty"`
	Downloads    int    `json:"downloads"`
	AllowUpload  bool   `json:"allowUpload,omitempty"`
}

// Expired dice si el enlace ya no sirve (vencido o sin descargas disponibles).
func (l ShareLink) Expired(now time.Time) bool {
	if l.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, l.ExpiresAt); err == nil && !now.Before(t) {
			return true
		}
	}
	return l.MaxDownloads > 0 && l.Downloads >= l.MaxDownloads
}

// Config es connect.json.
type Config struct {
	Enabled      bool        `json:"enabled"`
	SignalURL    string      `json:"signalURL"`
	PortalURL    string      `json:"portalURL"`
	DeviceID     string      `json:"deviceID"`
	DeviceSecret string      `json:"deviceSecret"`
	Links        []Link      `json:"links"`
	ShareLinks   []ShareLink `json:"shareLinks"`
}

// Store guarda Config en <dir>/connect.json (0600, escritura atómica).
type Store struct {
	path string
	mu   sync.Mutex
	c    Config
}

// OpenStore lee (o crea) connect.json. La primera vez genera la identidad del
// dispositivo para signalhub: device_id (UUID v4) + device_secret (32 B).
func OpenStore(dir string) (*Store, error) {
	st := &Store{path: filepath.Join(dir, "connect.json")}
	b, err := os.ReadFile(st.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &st.c); err != nil {
			return nil, fmt.Errorf("connect: %s: %w", st.path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}
	changed := false
	if st.c.SignalURL == "" {
		st.c.SignalURL, changed = DefaultSignalURL, true
	}
	if st.c.PortalURL == "" {
		st.c.PortalURL, changed = DefaultPortalURL, true
	}
	if st.c.DeviceID == "" {
		st.c.DeviceID, changed = newUUID(), true
	}
	if st.c.DeviceSecret == "" {
		st.c.DeviceSecret, changed = randB64(32), true
	}
	if changed {
		if err := st.saveLocked(); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// Get devuelve una copia de la configuración.
func (st *Store) Get() Config {
	st.mu.Lock()
	defer st.mu.Unlock()
	c := st.c
	c.Links = append([]Link(nil), st.c.Links...)
	c.ShareLinks = append([]ShareLink(nil), st.c.ShareLinks...)
	return c
}

// Update aplica fn y guarda.
func (st *Store) Update(fn func(c *Config) error) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	prev := st.c
	prev.Links = append([]Link(nil), st.c.Links...)
	prev.ShareLinks = append([]ShareLink(nil), st.c.ShareLinks...)
	if err := fn(&st.c); err != nil {
		st.c = prev
		return err
	}
	if err := st.saveLocked(); err != nil {
		st.c = prev
		return err
	}
	return nil
}

func (st *Store) saveLocked() error {
	if st.c.Links == nil {
		st.c.Links = []Link{}
	}
	if st.c.ShareLinks == nil {
		st.c.ShareLinks = []ShareLink{}
	}
	b, err := json.MarshalIndent(st.c, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// ---- secretos ---------------------------------------------------------------------------

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func randB64(n int) string { return base64.RawURLEncoding.EncodeToString(randBytes(n)) }

func newID() string { return hex.EncodeToString(randBytes(8)) }

func newUUID() string {
	b := randBytes(16)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// hashHex es hex(SHA-256(s)).
func hashHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// equalHex compara dos hashes hex en tiempo constante.
func equalHex(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
