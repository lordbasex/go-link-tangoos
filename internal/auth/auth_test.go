package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFirstRunAdminLoginResumeLogout(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	pw, err := s.EnsureAdmin(dir)
	if err != nil || len(pw) != 16 {
		t.Fatalf("EnsureAdmin: %q %v", pw, err)
	}
	for _, f := range []string{"users.json", "initial-admin-password"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, st, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "users.json"))
	if strings.Contains(string(b), pw) || !strings.Contains(string(b), "$argon2id$") {
		t.Fatal("users.json must hold only the argon2id hash")
	}
	if again, _ := s.EnsureAdmin(dir); again != "" {
		t.Fatal("second EnsureAdmin must not recreate admin")
	}

	// reload from disk
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	u, tok, err := s.Login("admin", pw, "192.0.2.1")
	if err != nil || !u.Admin || tok == "" || u.LastLogin == "" {
		t.Fatalf("login: %+v %q %v", u, tok, err)
	}
	if _, err := s.Resume(tok); err != nil {
		t.Fatal(err)
	}
	s.Logout(tok)
	if _, err := s.Resume(tok); !errors.Is(err, ErrToken) {
		t.Fatalf("resume after logout: %v", err)
	}
}

func TestLockoutAndIdleExpiry(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	if err := s.Add(User{Name: "sofia", Full: "Sofía"}, "correct horse"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFails; i++ {
		if _, _, err := s.Login("sofia", "nope", "ip1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, _, err := s.Login("sofia", "correct horse", "ip1"); !errors.Is(err, ErrLocked) {
		t.Fatalf("want locked, got %v", err)
	}
	if _, _, err := s.Login("sofia", "correct horse", "ip2"); err != nil {
		t.Fatalf("other IP must not be locked: %v", err)
	}
	now = now.Add(lockoutTime + time.Second)
	_, tok, err := s.Login("sofia", "correct horse", "ip1")
	if err != nil {
		t.Fatalf("after lockout: %v", err)
	}
	now = now.Add(TokenIdle + time.Second)
	if _, err := s.Resume(tok); !errors.Is(err, ErrToken) {
		t.Fatalf("idle token must expire: %v", err)
	}
	if _, _, err := s.Login("nobody", "x", "ip1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown user: %v", err)
	}
	if p := s.Public(); len(p) != 1 || p[0].Name != "sofia" {
		t.Fatalf("public: %+v", p)
	}
}

func TestSessionsSurviveRestartAndPasswordChange(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(User{Name: "sofia", Full: "Sofía"}, "primera-contraseña"); err != nil {
		t.Fatal(err)
	}
	_, a, err := s.Login("sofia", "primera-contraseña", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	_, b, _ := s.Login("sofia", "primera-contraseña", "10.0.0.2")

	// "Reinicio": un Store nuevo sobre el mismo directorio.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Resume(a); err != nil {
		t.Fatalf("el token no sobrevivió al reinicio: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "sessions.json"))
	if strings.Contains(string(raw), a) || strings.Contains(string(raw), b) {
		t.Fatal("sessions.json guarda tokens en claro")
	}
	if err := s2.SetPassword("sofia", "segunda-contraseña", a); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Resume(a); err != nil {
		t.Errorf("la sesión de quien cambió la contraseña tiene que seguir: %v", err)
	}
	if _, err := s2.Resume(b); err == nil {
		t.Error("las otras sesiones tienen que cerrarse")
	}
	if _, _, err := s2.Login("sofia", "primera-contraseña", "10.0.0.1"); err == nil {
		t.Error("la contraseña vieja sigue sirviendo")
	}
}
