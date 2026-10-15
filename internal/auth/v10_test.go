package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newV10Store(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAdmin(dir); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(User{Name: "sofia", Full: "Sofía"}, "una-clave-larga"); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAppPasswordsWithoutAndWithTOTP(t *testing.T) {
	s := newV10Store(t)
	info, pw, err := s.CreateAppPassword("sofia", "Notebook WebDAV", false)
	if err != nil || len(pw) != 29 || strings.Count(pw, "-") != 5 || info.LastUsed != nil {
		t.Fatalf("create: %+v %q %v", info, pw, err)
	}
	b, _ := os.ReadFile(s.path)
	if strings.Contains(string(b), pw) || strings.Contains(string(b), normAppPassword(pw)) {
		t.Fatal("users.json must not hold the app password")
	}
	// Sin 2FA: vale la de la cuenta y la de app (sin importar guiones ni mayúsculas).
	if _, err := s.VerifyService("sofia", "una-clave-larga", "ip"); err != nil {
		t.Fatalf("account password: %v", err)
	}
	if _, err := s.VerifyService("sofia", strings.ToLower(strings.ReplaceAll(pw, "-", "")), "ip"); err != nil {
		t.Fatalf("app password normalized: %v", err)
	}
	list, _ := s.AppPasswords("sofia")
	if len(list) != 1 || list[0].LastUsed == nil {
		t.Fatalf("lastUsed must be set: %+v", list)
	}
	// Con 2FA: solo la de app.
	s.mu.Lock()
	s.users["sofia"].TOTPSecret = "JBSWY3DPEHPK3PXP"
	s.mu.Unlock()
	if _, err := s.VerifyService("sofia", "una-clave-larga", "ip2"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("account password must fail with TOTP: %v", err)
	}
	if _, err := s.VerifyService("sofia", pw, "ip2"); err != nil {
		t.Fatalf("app password with TOTP: %v", err)
	}
	// Revocar.
	wasSMB, err := s.RevokeAppPassword("sofia", info.ID)
	if err != nil || wasSMB {
		t.Fatalf("revoke: %v %v", wasSMB, err)
	}
	if _, err := s.VerifyService("sofia", pw, "ip3"); !errors.Is(err, ErrInvalid) {
		t.Fatal("revoked app password must fail")
	}
	if _, err := s.RevokeAppPassword("sofia", info.ID); !errors.Is(err, ErrAppPwNotFound) {
		t.Fatalf("second revoke: %v", err)
	}
}

func TestAppPasswordSMBFlagIsExclusiveAndCapped(t *testing.T) {
	s := newV10Store(t)
	a, _, _ := s.CreateAppPassword("sofia", "Mac SMB", true)
	b, _, _ := s.CreateAppPassword("sofia", "PC SMB", true)
	list, _ := s.AppPasswords("sofia")
	for _, p := range list {
		if (p.ID == a.ID && p.SMB) || (p.ID == b.ID && !p.SMB) {
			t.Fatalf("only the newest may be SMB: %+v", list)
		}
	}
	if was, _ := s.RevokeAppPassword("sofia", b.ID); !was {
		t.Fatal("revoking the SMB one must report it")
	}
	for i := len(list) - 1; i < MaxAppPasswords; i++ {
		if _, _, err := s.CreateAppPassword("sofia", "x", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.CreateAppPassword("sofia", "una más", false); !errors.Is(err, ErrTooManyApp) {
		t.Fatalf("cap: %v", err)
	}
	if _, _, err := s.CreateAppPassword("sofia", "  ", false); err == nil {
		t.Fatal("empty label must fail")
	}
}

func TestDeleteUserRevokesSessions(t *testing.T) {
	s := newV10Store(t)
	_, tok, err := s.Login("sofia", "una-clave-larga", "ip")
	if err != nil {
		t.Fatal(err)
	}
	if s.AdminCount() != 1 {
		t.Fatalf("admins: %d", s.AdminCount())
	}
	if err := s.Delete("sofia"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(tok); err == nil {
		t.Fatal("token of a deleted user must not resume")
	}
	if _, ok := s.Get("sofia"); ok {
		t.Fatal("user still there")
	}
	if err := s.Delete("sofia"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	// Persistido.
	s2, err := Open(filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get("sofia"); ok {
		t.Fatal("deleted user came back from disk")
	}
}
