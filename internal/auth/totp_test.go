package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Vectores de RFC 6238 (apéndice B, SHA-1, secreto "12345678901234567890"), 6 dígitos.
func TestTOTPRFCVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037", 20000000000: "353130"}
	for unix, want := range cases {
		if got := totpCode(secret, unix/totpStep); got != want {
			t.Errorf("t=%d: %s, want %s", unix, got, want)
		}
	}
}

func codeAt(secretB32 string, at time.Time) string {
	sec, _ := b32.DecodeString(secretB32)
	return totpCode(sec, at.Unix()/totpStep)
}

func TestTOTPFlow(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	if err := s.Add(User{Name: "ana", Full: "Ana"}, "una-clave-larga-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TOTPConfirm("ana", "123456"); !errors.Is(err, ErrNoPending) {
		t.Fatalf("confirm sin begin: %v", err)
	}
	secret, uri, err := s.TOTPBegin("ana")
	if err != nil || !strings.HasPrefix(uri, "otpauth://totp/TangoOS:ana?") || !strings.Contains(uri, "secret="+secret) {
		t.Fatalf("begin: %q %v", uri, err)
	}
	if _, err := s.TOTPConfirm("ana", "000000"); !errors.Is(err, ErrBadCode) {
		t.Fatalf("código malo aceptado: %v", err)
	}
	codes, err := s.TOTPConfirm("ana", codeAt(secret, now))
	if err != nil || len(codes) != recoveryN {
		t.Fatalf("confirm: %v %d", err, len(codes))
	}
	if u, _ := s.Get("ana"); !u.TwoFA {
		t.Fatal("twofa no quedó activa")
	}

	// Login: la contraseña sola no da token.
	if _, tok, err := s.Login("ana", "una-clave-larga-1", "1.2.3.4"); !errors.Is(err, ErrNeedTOTP) || tok != "" {
		t.Fatalf("login con 2FA: %v %q", err, tok)
	}
	// El mismo código del alta no se puede reusar (mismo contador).
	if _, _, err := s.CompleteTOTP("ana", codeAt(secret, now)); !errors.Is(err, ErrBadCode) {
		t.Fatalf("reuso aceptado: %v", err)
	}
	now = now.Add(30 * time.Second)
	_, tok, err := s.CompleteTOTP("ana", codeAt(secret, now))
	if err != nil || tok == "" {
		t.Fatalf("complete: %v", err)
	}
	if _, err := s.Resume(tok); err != nil {
		t.Fatal(err)
	}
	// Código de recuperación: sirve una vez, con o sin guion y en mayúsculas.
	rc := strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))
	if _, _, err := s.CompleteTOTP("ana", rc); err != nil {
		t.Fatalf("recuperación: %v", err)
	}
	if _, _, err := s.CompleteTOTP("ana", codes[0]); !errors.Is(err, ErrBadCode) {
		t.Fatal("código de recuperación reusado")
	}
	if s.RecoveryLeft("ana") != recoveryN-1 {
		t.Fatalf("quedan %d", s.RecoveryLeft("ana"))
	}
	// Persistencia: otro Store lee la 2FA del archivo.
	s2, _ := Open(dir)
	if u, _ := s2.Get("ana"); !u.TwoFA {
		t.Fatal("2FA no persistida")
	}
	// Desactivar pide la contraseña.
	if err := s.TOTPDisable("ana", "mal"); err == nil {
		t.Fatal("disable sin contraseña")
	}
	if err := s.TOTPDisable("ana", "una-clave-larga-1"); err != nil {
		t.Fatal(err)
	}
	if _, tok, err := s.Login("ana", "una-clave-larga-1", "1.2.3.4"); err != nil || tok == "" {
		t.Fatalf("login sin 2FA: %v", err)
	}
}

func TestTOTPWindow(t *testing.T) {
	secret := newTOTPSecret()
	now := time.Unix(1_900_000_000, 0)
	if checkTOTP(secret, codeAt(secret, now.Add(-30*time.Second)), now, 0) == 0 {
		t.Fatal("paso anterior rechazado")
	}
	if checkTOTP(secret, codeAt(secret, now.Add(90*time.Second)), now, 0) != 0 {
		t.Fatal("código de 3 pasos adelante aceptado")
	}
}
