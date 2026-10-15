// Package auth keeps the local users, verifies passwords (argon2id) and
// issues session tokens (protocol/README.md §4, docs/07 §2).
//
// Users live in <state>/users.json (0600, atomic writes). Tokens are 32
// random bytes; only their SHA-256 is kept, in memory and in
// <state>/sessions.json (0600), so sessions survive a tangod restart
// (protocol/README.md §8).
package auth

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
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalid = errors.New("usuario o contraseña incorrectos")
	ErrLocked  = errors.New("demasiados intentos: esperá unos minutos")
	ErrToken   = errors.New("la sesión expiró")
)

// User is exactly the TS User (web/packages/client/src/protocol.ts).
type User struct {
	Name      string   `json:"name"`
	Full      string   `json:"full"`
	Email     string   `json:"email"`
	Groups    []string `json:"groups"`
	Admin     bool     `json:"admin"`
	TwoFA     bool     `json:"twofa"`
	LastLogin string   `json:"lastLogin"`
	QuotaUsed uint64   `json:"quotaUsed"`
	Quota     *uint64  `json:"quota"`
	Color     string   `json:"color"`
}

// PublicUser is what the login screen may see before authentication.
type PublicUser struct {
	Name  string `json:"name"`
	Full  string `json:"full"`
	Color string `json:"color"`
}

type record struct {
	User
	Hash    string    `json:"hash"` // $argon2id$v=19$m=…,t=…,p=…$salt$key
	Created time.Time `json:"created"`
	// Verificación en dos pasos (totp.go). El archivo es 0600 y solo lo lee tangod.
	TOTPSecret  string   `json:"totp_secret,omitempty"`
	TOTPPending string   `json:"totp_pending,omitempty"`
	TOTPLast    int64    `json:"totp_last,omitempty"` // último contador aceptado (sin reuso)
	Recovery    []string `json:"recovery,omitempty"`  // SHA-256 de los códigos de recuperación
	// Contraseñas de app (v10.go): solo su SHA-256.
	AppPasswords []appPassword `json:"app_passwords,omitempty"`
}

type token struct {
	user     string
	lastUsed time.Time
}

// sessionRec is a persisted token: never the token itself, only its SHA-256.
type sessionRec struct {
	Hash     string    `json:"hash"`
	User     string    `json:"user"`
	LastUsed time.Time `json:"lastUsed"`
}

type attempt struct {
	fails int
	until time.Time
}

// Params for argon2id (OWASP 2024: m=19 MiB, t=2, p=1).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32

	TokenIdle   = 12 * time.Hour
	maxFails    = 5
	lockoutTime = 5 * time.Minute
)

// Store is the users file plus the in-memory tokens and lockouts.
type Store struct {
	path     string
	sessPath string
	now      func() time.Time
	// lastSave limits how often Touch writes sessions.json.
	lastSave time.Time

	mu       sync.Mutex
	users    map[string]*record
	tokens   map[string]*token // sha256(token) hex → token
	attempts map[string]*attempt
}

// Open loads <dir>/users.json (missing file = no users).
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "users.json"), sessPath: filepath.Join(dir, "sessions.json"), now: time.Now,
		users: map[string]*record{}, tokens: map[string]*token{}, attempts: map[string]*attempt{}}
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	defer s.loadSessions()
	var recs []*record
	if err := json.Unmarshal(b, &recs); err != nil {
		return nil, fmt.Errorf("auth: %s: %w", s.path, err)
	}
	for _, r := range recs {
		s.users[r.Name] = r
	}
	return s, nil
}

// loadSessions restores the persisted tokens that are still valid.
func (s *Store) loadSessions() {
	b, err := os.ReadFile(s.sessPath)
	if err != nil {
		return
	}
	var recs []sessionRec
	if json.Unmarshal(b, &recs) != nil {
		return
	}
	now := s.now()
	for _, r := range recs {
		if _, ok := s.users[r.User]; ok && now.Sub(r.LastUsed) <= TokenIdle && len(r.Hash) == 64 {
			s.tokens[r.Hash] = &token{user: r.User, lastUsed: r.LastUsed}
		}
	}
}

func (s *Store) saveSessionsLocked() {
	now := s.now()
	recs := []sessionRec{}
	for h, t := range s.tokens {
		if now.Sub(t.lastUsed) > TokenIdle {
			delete(s.tokens, h)
			continue
		}
		recs = append(recs, sessionRec{Hash: h, User: t.user, LastUsed: t.lastUsed})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Hash < recs[j].Hash })
	if b, err := json.MarshalIndent(recs, "", "  "); err == nil && s.sessPath != "" {
		_ = writeAtomic(s.sessPath, b)
		s.lastSave = now
	}
}

// SetPassword changes a user's password and revokes their other sessions
// (keep = the caller's token, "" to revoke all).
func (s *Store) SetPassword(name, password, keep string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	if !ok {
		return fmt.Errorf("auth: no existe el usuario %q", name)
	}
	r.Hash = hashPassword(password)
	if err := s.saveLocked(); err != nil {
		return err
	}
	keepHash := ""
	if keep != "" {
		keepHash = digest(keep)
	}
	for h, t := range s.tokens {
		if t.user == name && h != keepHash {
			delete(s.tokens, h)
		}
	}
	s.saveSessionsLocked()
	if name == "admin" { // la contraseña inicial solo deja de valer cuando cambia la de admin
		_ = os.Remove(filepath.Join(filepath.Dir(s.path), "initial-admin-password"))
	}
	return nil
}

// EnsureAdmin creates "admin" with a random password when there are no
// users, writes the password to <dir>/initial-admin-password (0600) and
// returns it ("" when users already existed).
func (s *Store) EnsureAdmin(dir string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.users) > 0 {
		return "", nil
	}
	pw := randomPassword()
	if err := s.addLocked(User{Name: "admin", Full: "Administrador", Groups: []string{"administrators"}, Admin: true, Color: "#2F7CF6"}, pw); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(dir, "initial-admin-password"), []byte(pw+"\n")); err != nil {
		return "", err
	}
	return pw, nil
}

// Add creates a user.
func (s *Store) Add(u User, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addLocked(u, password)
}

func (s *Store) addLocked(u User, password string) error {
	if u.Name == "" || strings.ContainsAny(u.Name, " /:\\") {
		return fmt.Errorf("auth: nombre de usuario inválido %q", u.Name)
	}
	if _, ok := s.users[u.Name]; ok {
		return fmt.Errorf("auth: el usuario %q ya existe", u.Name)
	}
	if u.Groups == nil {
		u.Groups = []string{}
	}
	s.users[u.Name] = &record{User: u, Hash: hashPassword(password), Created: s.now()}
	return s.saveLocked()
}

// Login checks the password and returns the user and a new token.
// ip keys the lockout together with the user name.
func (s *Store) Login(name, password, ip string) (User, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := name + "|" + ip
	now := s.now()
	if a := s.attempts[key]; a != nil && now.Before(a.until) {
		return User{}, "", ErrLocked
	}
	r, ok := s.users[name]
	if !ok || !verifyPassword(r.Hash, password) {
		if !ok { // same cost as a real check: no user enumeration by timing
			verifyPassword(dummyHash, password)
		}
		a := s.attempts[key]
		if a == nil || now.After(a.until) && !a.until.IsZero() {
			a = &attempt{}
			s.attempts[key] = a
		}
		a.fails++
		if a.fails >= maxFails {
			a.until, a.fails = now.Add(lockoutTime), 0
		}
		return User{}, "", ErrInvalid
	}
	delete(s.attempts, key)
	if r.TOTPSecret != "" { // contraseña bien, falta el código (CompleteTOTP emite el token)
		return r.User, "", ErrNeedTOTP
	}
	r.LastLogin = now.UTC().Format(time.RFC3339)
	_ = s.saveLocked()
	tok := randomToken()
	s.tokens[digest(tok)] = &token{user: name, lastUsed: now}
	s.saveSessionsLocked()
	return r.User, tok, nil
}

// Resume validates a token and returns its user.
func (s *Store) Resume(tok string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tokens[digest(tok)]
	now := s.now()
	if t == nil || now.Sub(t.lastUsed) > TokenIdle {
		delete(s.tokens, digest(tok))
		return User{}, ErrToken
	}
	r, ok := s.users[t.user]
	if !ok {
		return User{}, ErrToken
	}
	t.lastUsed = now
	s.saveSessionsLocked()
	return r.User, nil
}

// Touch refreshes a token's idle timer (each authenticated request).
func (s *Store) Touch(tok string) {
	s.mu.Lock()
	if t := s.tokens[digest(tok)]; t != nil {
		t.lastUsed = s.now()
		// Persist the idle timer at most once a minute.
		if s.now().Sub(s.lastSave) > time.Minute {
			s.saveSessionsLocked()
		}
	}
	s.mu.Unlock()
}

// Logout revokes a token.
func (s *Store) Logout(tok string) {
	s.mu.Lock()
	delete(s.tokens, digest(tok))
	s.saveSessionsLocked()
	s.mu.Unlock()
}

// Get returns a user by name.
func (s *Store) Get(name string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	if !ok {
		return User{}, false
	}
	return r.User, true
}

// List returns all users, sorted by name.
func (s *Store) List() []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, 0, len(s.users))
	for _, r := range s.users {
		out = append(out, r.User)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Public lists the users for the login screen.
func (s *Store) Public() []PublicUser {
	var out []PublicUser
	for _, u := range s.List() {
		out = append(out, PublicUser{Name: u.Name, Full: u.Full, Color: u.Color})
	}
	return out
}

func (s *Store) saveLocked() error {
	recs := make([]*record, 0, len(s.users))
	for _, r := range s.users {
		recs = append(recs, r)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
	b, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.path, b)
}

// ---- passwords and tokens -------------------------------------------------------

var dummyHash = hashPassword("tango-dummy-password")

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

func verifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// randomPassword: 16 chars from an alphabet without look-alikes.
func randomPassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func digest(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
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
	return os.Rename(tmp.Name(), path)
}
