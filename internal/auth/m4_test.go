package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestProveTokenHostFirst(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(User{Name: "sofia", Full: "Sofía"}, "unaClaveLarga2026"); err != nil {
		t.Fatal(err)
	}
	_, tok, err := s.Login("sofia", "unaClaveLarga2026", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(tok))
	id := TokenID(hex.EncodeToString(h[:]))
	nonce := "0123456789abcdef0123"
	proof, err := s.ProveToken(id, nonce)
	if err != nil || proof != ResumeProof(h[:], nonce) {
		t.Fatalf("proof: %v", err)
	}
	if _, err := s.ProveToken("0000000000000000000000000000000f", nonce); err == nil {
		t.Fatal("unknown token proved")
	}
	if _, err := s.ProveToken(id, "corto"); err == nil {
		t.Fatal("short nonce accepted")
	}
	s.Logout(tok)
	if _, err := s.ProveToken(id, nonce); err == nil {
		t.Fatal("revoked token proved")
	}
}

func TestVerifyAndSetup(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	pw, err := s.EnsureAdmin(dir)
	if err != nil || pw == "" {
		t.Fatal(err)
	}
	if s.SetupDone(dir) {
		t.Fatal("fresh install must be pending")
	}
	if _, err := s.Verify("admin", pw, "ip"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFails; i++ {
		_, _ = s.Verify("admin", "mal", "ip2")
	}
	if _, err := s.Verify("admin", pw, "ip2"); err != ErrLocked {
		t.Fatalf("lockout: %v", err)
	}
	if err := s.ReplaceInitialAdmin(dir, User{Name: "federico", Full: "Federico"}, "frase-larga-de-prueba"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("admin"); ok {
		t.Fatal("unused initial admin must be removed")
	}
	if u, ok := s.Get("federico"); !ok || !u.Admin {
		t.Fatal("new admin missing")
	}
	if err := MarkSetupDone(dir); err != nil || !s.SetupDone(dir) {
		t.Fatal("setup done flag")
	}
	tok, err := s.IssueToken("federico")
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.Resume(tok); err != nil || u.Name != "federico" {
		t.Fatal("issued token must resume")
	}
	for _, w := range []string{"corta", "tango-2026-segura", "federico-123456"} {
		if !WeakPassword(w, "federico") {
			t.Errorf("%q should be weak", w)
		}
	}
	if WeakPassword("Bandoneón-Río-1906", "federico") {
		t.Fatal("strong password rejected")
	}
}
