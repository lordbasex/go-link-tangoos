package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Verify checks a password without creating a token (WebDAV/SFTP basic auth). It
// shares the login lockout, keyed by user and remote address.
func (s *Store) Verify(name, password, ip string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := name + "|" + ip
	now := s.now()
	if a := s.attempts[key]; a != nil && now.Before(a.until) {
		return User{}, ErrLocked
	}
	r, ok := s.users[name]
	if !ok || !verifyPassword(r.Hash, password) {
		if !ok {
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
		return User{}, ErrInvalid
	}
	delete(s.attempts, key)
	return r.User, nil
}

// ResumeProofLabel is the HMAC message prefix of the "host proves first" resume
// (protocol/README.md §12, docs/07 §2).
const ResumeProofLabel = "tango resume\n"

// TokenID is how a client names its token without revealing it:
// hex(SHA-256(SHA-256(token)))[:32]. The server derives the same value from the
// digest it stores.
func TokenID(digestHex string) string {
	raw, err := hex.DecodeString(digestHex)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])[:32]
}

// ResumeProof is HMAC-SHA256(key = SHA-256(token), "tango resume\n" + nonce), hex.
func ResumeProof(digestRaw []byte, nonce string) string {
	m := hmac.New(sha256.New, digestRaw)
	m.Write([]byte(ResumeProofLabel + nonce))
	return hex.EncodeToString(m.Sum(nil))
}

// ErrUnknownToken: no live token has that id.
var ErrUnknownToken = errors.New("sesión desconocida o vencida")

// ProveToken lets the NAS prove it knows the client's token before the client
// reveals it: it looks the token up by id and returns the HMAC of the nonce.
func (s *Store) ProveToken(tokenID, nonce string) (string, error) {
	if len(nonce) < 16 || len(nonce) > 128 || len(tokenID) != 32 {
		return "", ErrUnknownToken
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for d, t := range s.tokens {
		if now.Sub(t.lastUsed) > TokenIdle {
			continue
		}
		if hmac.Equal([]byte(TokenID(d)), []byte(tokenID)) {
			raw, _ := hex.DecodeString(d)
			return ResumeProof(raw, nonce), nil
		}
	}
	return "", ErrUnknownToken
}

// IssueToken creates a session token for user (setup.complete logs the new admin in).
func (s *Store) IssueToken(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[name]; !ok {
		return "", ErrInvalid
	}
	tok := randomToken()
	s.tokens[digest(tok)] = &token{user: name, lastUsed: s.now()}
	s.saveSessionsLocked()
	return tok, nil
}

// ---- Primer arranque ----------------------------------------------------------------

const setupDoneFile = "setup-done"

// SetupDone reports whether first-boot setup has finished. An existing install
// whose admin was created before M4 counts as done: someone already logged in, or
// the admin was created with a generated password long ago.
func (s *Store) SetupDone(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, setupDoneFile)); err == nil {
		return true
	}
	if os.Getenv("TANGOD_SETUP_PENDING") == "1" { // e2e: forzar el asistente en un estado nuevo
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.users {
		if r.LastLogin != "" || len(s.users) > 1 {
			return true
		}
	}
	return false
}

// MarkSetupDone records that setup finished (idempotent).
func MarkSetupDone(dir string) error {
	return writeAtomic(filepath.Join(dir, setupDoneFile), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"))
}

// ReplaceInitialAdmin swaps the generated first-run admin for the one chosen in the
// setup assistant: the initial account is removed if it was never used, and its
// password file is deleted.
func (s *Store) ReplaceInitialAdmin(dir string, u User, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.users["admin"]; ok && u.Name != "admin" && old.LastLogin == "" {
		delete(s.users, "admin")
	}
	if r, ok := s.users[u.Name]; ok {
		r.User.Full = u.Full
		r.User.Admin = true
		r.Hash = hashPassword(password)
	} else {
		u.Admin = true
		if err := s.addLocked(u, password); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(dir, "initial-admin-password"))
	return s.saveLocked()
}

// WeakPassword is the minimal server-side policy (the UI shows a richer meter).
func WeakPassword(pw string, context ...string) bool {
	if len(pw) < 10 {
		return true
	}
	lower := strings.ToLower(pw)
	for _, c := range []string{"123456", "password", "contraseña", "qwerty", "admin", "tango", "111111"} {
		if strings.Contains(lower, c) {
			return true
		}
	}
	for _, c := range context {
		if len(c) >= 3 && strings.Contains(lower, strings.ToLower(c)) {
			return true
		}
	}
	return false
}

// SessionID es el identificador público de un token (el mismo TokenID del resume
// seguro): sirve para atar otras credenciales a la sesión sin guardar el token.
func SessionID(tok string) string {
	if tok == "" {
		return ""
	}
	return TokenID(digest(tok))
}

// SessionAlive dice si la sesión con ese id sigue viva (no cerró ni venció).
func (s *Store) SessionAlive(id string) bool {
	if len(id) != 32 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for d, t := range s.tokens {
		if now.Sub(t.lastUsed) <= TokenIdle && hmac.Equal([]byte(TokenID(d)), []byte(id)) {
			return true
		}
	}
	return false
}
