package auth

import (
	"os"
	"path/filepath"
	"testing"
)

// Regresión (e2e 2026-10-14): cambiar la contraseña de otro usuario borraba el archivo
// con la contraseña inicial de admin.
func TestSetPasswordKeepsInitialAdminFile(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAdmin(dir); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "initial-admin-password")
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("EnsureAdmin no escribió el archivo: %v", err)
	}
	if err := s.Add(User{Name: "otro", Full: "Otro"}, "Una-clave-larga-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword("otro", "Otra-clave-larga-2", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("cambiar la contraseña de otro usuario borró la contraseña inicial de admin")
	}
	if err := s.SetPassword("admin", "Admin-clave-nueva-3", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("cambiar la contraseña de admin debería invalidar el archivo inicial")
	}
}
