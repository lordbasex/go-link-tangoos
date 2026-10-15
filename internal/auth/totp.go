package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Verificación en dos pasos (protocol/README.md §13): TOTP RFC 6238 con SHA-1,
// 6 dígitos y pasos de 30 s (lo que entienden todas las apps de autenticación),
// ventana ±1 paso y sin reuso de un código ya aceptado.

var (
	// ErrNeedTOTP: la contraseña es correcta pero falta el código.
	ErrNeedTOTP = errors.New("falta el código de verificación")
	// ErrBadCode: código incorrecto o vencido.
	ErrBadCode = errors.New("código incorrecto")
	// ErrNoPending: no hay un alta de 2FA en curso.
	ErrNoPending = errors.New("primero generá el código QR")
)

const (
	totpStep    = 30
	totpDigits  = 6
	totpSkew    = 1
	totpIssuer  = "TangoOS"
	recoveryN   = 10
	recoveryLen = 10 // "xxxxx-xxxxx"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// totpCode calcula el código de un contador (RFC 4226 + RFC 6238).
func totpCode(secret []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}

// checkTOTP busca el código en la ventana y devuelve su contador (0 = no coincide).
// Solo acepta contadores mayores que last (sin reuso).
func checkTOTP(secretB32, code string, now time.Time, last int64) int64 {
	secret, err := b32.DecodeString(strings.ToUpper(secretB32))
	if err != nil || len(code) != totpDigits {
		return 0
	}
	cur := now.Unix() / totpStep
	for d := -totpSkew; d <= totpSkew; d++ {
		c := cur + int64(d)
		if c <= last {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, c)), []byte(code)) == 1 {
			return c
		}
	}
	return 0
}

// otpauthURI es el URI que leen las apps de autenticación (y que va en el QR).
func otpauthURI(user, secret string) string {
	label := url.PathEscape(totpIssuer + ":" + user)
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", totpIssuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	return "otpauth://totp/" + label + "?" + v.Encode()
}

func newTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

// newRecoveryCodes genera códigos "xxxxx-xxxxx" (alfabeto sin caracteres parecidos).
func newRecoveryCodes() []string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	out := make([]string, recoveryN)
	for i := range out {
		b := make([]byte, recoveryLen)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		for j := range b {
			b[j] = alphabet[int(b[j])%len(alphabet)]
		}
		out[i] = string(b[:5]) + "-" + string(b[5:])
	}
	return out
}

func recoveryHash(code string) string {
	code = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	if len(code) == recoveryLen && !strings.Contains(code, "-") {
		code = code[:5] + "-" + code[5:]
	}
	h := sha256.Sum256([]byte("tango recovery\n" + code))
	return hex.EncodeToString(h[:])
}

// TOTPBegin genera un secreto nuevo (pendiente hasta confirmar) y su URI otpauth.
func (s *Store) TOTPBegin(name string) (secret, uri string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	if !ok {
		return "", "", ErrInvalid
	}
	r.TOTPPending = newTOTPSecret()
	if err := s.saveLocked(); err != nil {
		return "", "", err
	}
	return r.TOTPPending, otpauthURI(name, r.TOTPPending), nil
}

// TOTPConfirm activa la 2FA si el código coincide con el secreto pendiente y
// devuelve los códigos de recuperación (en claro, una sola vez).
func (s *Store) TOTPConfirm(name, code string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	if !ok {
		return nil, ErrInvalid
	}
	if r.TOTPPending == "" {
		return nil, ErrNoPending
	}
	c := checkTOTP(r.TOTPPending, strings.TrimSpace(code), s.now(), 0)
	if c == 0 {
		return nil, ErrBadCode
	}
	codes := newRecoveryCodes()
	r.TOTPSecret, r.TOTPPending, r.TOTPLast = r.TOTPPending, "", c
	r.Recovery = make([]string, len(codes))
	for i, rc := range codes {
		r.Recovery[i] = recoveryHash(rc)
	}
	r.TwoFA = true
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return codes, nil
}

// TOTPDisable desactiva la 2FA (pide la contraseña).
func (s *Store) TOTPDisable(name, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	if !ok || !verifyPassword(r.Hash, password) {
		return ErrInvalid
	}
	s.clearTOTPLocked(r)
	return s.saveLocked()
}

// TOTPReset desactiva la 2FA de otro usuario (lo hace un administrador).
func (s *Store) TOTPReset(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	if !ok {
		return fmt.Errorf("auth: no existe el usuario %q", name)
	}
	s.clearTOTPLocked(r)
	return s.saveLocked()
}

func (s *Store) clearTOTPLocked(r *record) {
	r.TOTPSecret, r.TOTPPending, r.TOTPLast, r.Recovery = "", "", 0, nil
	r.TwoFA = false
}

// CompleteTOTP termina un login que pidió código: acepta un código TOTP o uno de
// recuperación (que se consume) y emite el token de sesión.
func (s *Store) CompleteTOTP(name, code string) (User, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.users[name]
	if !ok || r.TOTPSecret == "" {
		return User{}, "", ErrInvalid
	}
	code = strings.TrimSpace(code)
	now := s.now()
	if c := checkTOTP(r.TOTPSecret, code, now, r.TOTPLast); c != 0 {
		r.TOTPLast = c
	} else if i := s.recoveryIndexLocked(r, code); i >= 0 {
		r.Recovery = append(r.Recovery[:i], r.Recovery[i+1:]...)
	} else {
		return User{}, "", ErrBadCode
	}
	r.LastLogin = now.UTC().Format(time.RFC3339)
	_ = s.saveLocked()
	tok := randomToken()
	s.tokens[digest(tok)] = &token{user: name, lastUsed: now}
	s.saveSessionsLocked()
	return r.User, tok, nil
}

func (s *Store) recoveryIndexLocked(r *record, code string) int {
	if len(code) < recoveryLen {
		return -1
	}
	h := recoveryHash(code)
	for i, rc := range r.Recovery {
		if subtle.ConstantTimeCompare([]byte(rc), []byte(h)) == 1 {
			return i
		}
	}
	return -1
}

// RecoveryLeft dice cuántos códigos de recuperación le quedan a un usuario.
func (s *Store) RecoveryLeft(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.users[name]; ok {
		return len(r.Recovery)
	}
	return 0
}
