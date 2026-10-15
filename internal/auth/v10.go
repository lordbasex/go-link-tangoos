package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Cierre v1.0 (protocol/README.md §14): borrar usuarios y contraseñas de app.

var (
	ErrNotFound      = errors.New("auth: no existe el usuario")
	ErrTooManyApp    = errors.New("auth: hay demasiadas contraseñas de app (máximo 20)")
	ErrAppPwNotFound = errors.New("auth: no existe esa contraseña de app")
)

// MaxAppPasswords es el tope de contraseñas de app por usuario.
const MaxAppPasswords = 20

type appPassword struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Hash     string     `json:"hash"` // SHA-256 hex de la contraseña normalizada
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"last_used,omitempty"`
	SMB      bool       `json:"smb,omitempty"`
}

// AppPasswordInfo es lo que se muestra de una contraseña de app (nunca el secreto).
type AppPasswordInfo struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Created  string  `json:"created"`
	LastUsed *string `json:"lastUsed"`
	SMB      bool    `json:"smb"`
}

func (p appPassword) info() AppPasswordInfo {
	out := AppPasswordInfo{ID: p.ID, Label: p.Label, Created: p.Created.UTC().Format(time.RFC3339), SMB: p.SMB}
	if p.LastUsed != nil {
		t := p.LastUsed.UTC().Format(time.RFC3339)
		out.LastUsed = &t
	}
	return out
}

// normAppPassword ignora guiones, espacios y mayúsculas al escribirla.
func normAppPassword(pw string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(pw)))
}

func appPasswordHash(pw string) string {
	h := sha256.Sum256([]byte("tango app password\n" + normAppPassword(pw)))
	return hex.EncodeToString(h[:])
}

// newAppPassword devuelve 24 caracteres base32 en grupos de 4 ("ABCD-EFGH-…").
func newAppPassword() string {
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	raw := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b) // 24 caracteres
	var g []string
	for i := 0; i < len(raw); i += 4 {
		g = append(g, raw[i:i+4])
	}
	return strings.Join(g, "-")
}

func newAppID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "ap_" + hex.EncodeToString(b)
}

// Delete borra un usuario con su 2FA, contraseñas de app, sesiones e intentos.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[name]; !ok {
		return ErrNotFound
	}
	delete(s.users, name)
	if err := s.saveLocked(); err != nil {
		return err
	}
	for h, t := range s.tokens {
		if t.user == name {
			delete(s.tokens, h)
		}
	}
	for k := range s.attempts {
		if strings.HasPrefix(k, name+"|") {
			delete(s.attempts, k)
		}
	}
	s.saveSessionsLocked()
	return nil
}

// AdminCount cuenta los administradores.
func (s *Store) AdminCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.users {
		if r.Admin {
			n++
		}
	}
	return n
}

// AppPasswords lista las contraseñas de app de un usuario.
func (s *Store) AppPasswords(name string) ([]AppPasswordInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]AppPasswordInfo, 0, len(r.AppPasswords))
	for _, p := range r.AppPasswords {
		out = append(out, p.info())
	}
	return out, nil
}

// CreateAppPassword crea una contraseña de app y la devuelve en claro (única vez).
// Con smb, pasa a ser la contraseña SMB: las demás pierden esa marca.
func (s *Store) CreateAppPassword(name, label string, smb bool) (AppPasswordInfo, string, error) {
	label = strings.TrimSpace(label)
	if label == "" || len([]rune(label)) > 64 || strings.ContainsAny(label, "\n\r") {
		return AppPasswordInfo{}, "", fmt.Errorf("auth: el nombre de la contraseña de app es obligatorio (máx. 64)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	if !ok {
		return AppPasswordInfo{}, "", ErrNotFound
	}
	if len(r.AppPasswords) >= MaxAppPasswords {
		return AppPasswordInfo{}, "", ErrTooManyApp
	}
	pw := newAppPassword()
	if smb {
		for i := range r.AppPasswords {
			r.AppPasswords[i].SMB = false
		}
	}
	p := appPassword{ID: newAppID(), Label: label, Hash: appPasswordHash(pw), Created: s.now(), SMB: smb}
	r.AppPasswords = append(r.AppPasswords, p)
	if err := s.saveLocked(); err != nil {
		return AppPasswordInfo{}, "", err
	}
	return p.info(), pw, nil
}

// RevokeAppPassword borra una contraseña de app; dice si era la de SMB.
func (s *Store) RevokeAppPassword(name, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	if !ok {
		return false, ErrNotFound
	}
	for i, p := range r.AppPasswords {
		if p.ID == id {
			r.AppPasswords = append(r.AppPasswords[:i], r.AppPasswords[i+1:]...)
			return p.SMB, s.saveLocked()
		}
	}
	return false, ErrAppPwNotFound
}

// HasTOTP dice si el usuario tiene la verificación en dos pasos activa.
func (s *Store) HasTOTP(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	return ok && r.TOTPSecret != ""
}

// VerifyService verifica una contraseña para WebDAV/SFTP (sin token). Con 2FA activa
// solo valen las contraseñas de app; sin 2FA, la de la cuenta o una de app. Comparte el
// bloqueo por intentos con el login.
func (s *Store) VerifyService(name, password, ip string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := name + "|" + ip
	now := s.now()
	if a := s.attempts[key]; a != nil && now.Before(a.until) {
		return User{}, ErrLocked
	}
	r, ok := s.users[name]
	good := false
	if ok {
		h := appPasswordHash(password)
		for i := range r.AppPasswords {
			if subtle.ConstantTimeCompare([]byte(r.AppPasswords[i].Hash), []byte(h)) == 1 {
				good = true
				if r.AppPasswords[i].LastUsed == nil || now.Sub(*r.AppPasswords[i].LastUsed) > time.Minute {
					t := now
					r.AppPasswords[i].LastUsed = &t
					_ = s.saveLocked()
				}
				break
			}
		}
		if !good && r.TOTPSecret == "" {
			good = verifyPassword(r.Hash, password)
		}
	} else {
		verifyPassword(dummyHash, password)
	}
	if !good {
		a := s.attempts[key]
		if a == nil || now.After(a.until) && !a.until.IsZero() {
			a = &attempt{}
			s.attempts[key] = a
		}
		a.fails++
		if a.fails >= maxFails {
			a.until, a.fails = now.Add(lockoutTime), 0
		}
		return User{}, ErrInvalid
	}
	delete(s.attempts, key)
	return r.User, nil
}

// RandomSecret es una contraseña aleatoria que nadie conoce (para cerrar SMB).
func RandomSecret() string { return newAppPassword() + "-" + newAppPassword() }
