//go:build e2e

// Pruebas E2E de los servicios de M4 contra un NAS real (VM de laboratorio).
//
//	TANGO_HOST=172.17.0.44 TANGO_E2E_USER=sofia TANGO_E2E_PASSWORD=… go test -tags e2e ./tests/e2e/ -run SFTP -v
package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/pkg/sftp"

	"github.com/lordbasex/go-link-tangoos/internal/auth"
	"github.com/lordbasex/go-link-tangoos/internal/rtc"
	"golang.org/x/crypto/ssh"
)

func env(t *testing.T, k string) string {
	v := os.Getenv(k)
	if v == "" {
		t.Skipf("falta %s", k)
	}
	return v
}

func TestSFTPPermissions(t *testing.T) {
	host, user, pw := env(t, "TANGO_HOST"), env(t, "TANGO_E2E_USER"), env(t, "TANGO_E2E_PASSWORD")
	cfg := &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.Password(pw)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second} //nolint:gosec // laboratorio
	conn, err := ssh.Dial("tcp", host+":2222", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	c, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	root, err := c.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range root {
		names = append(names, f.Name())
	}
	t.Logf("carpetas visibles: %v", names)

	payload := []byte(fmt.Sprintf("hola por SFTP %s\n", time.Now().Format(time.RFC3339)))
	f, err := c.Create("/Familia/sftp-e2e.txt")
	if err != nil {
		t.Fatalf("crear en Familia: %v", err)
	}
	if _, err := f.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	r, err := c.Open("/Familia/sftp-e2e.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("contenido distinto: %q", got)
	}
	if err := c.Rename("/Familia/sftp-e2e.txt", "/Familia/sftp-e2e-renombrado.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := c.Create("/Fotos/no-deberia.txt"); err == nil {
		t.Fatal("pudo crear en una carpeta de solo lectura")
	} else {
		t.Logf("Fotos (solo lectura): %v", err)
	}
	if _, err := c.ReadDir("/Fotos"); err != nil {
		t.Fatalf("listar Fotos: %v", err)
	}
	if _, err := c.Open("/Familia/../../etc/passwd"); err == nil {
		t.Fatal("traversal")
	}
	if err := c.Remove("/Familia/sftp-e2e-renombrado.txt"); err != nil {
		t.Fatalf("remove (a la papelera): %v", err)
	}
	bad := &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.Password("incorrecta")}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second} //nolint:gosec
	if c2, err := ssh.Dial("tcp", host+":2222", bad); err == nil {
		c2.Close()
		t.Fatal("entró con contraseña incorrecta")
	}
}

// TestSecureResume: el NAS prueba que conoce el token (HMAC) antes de que el cliente lo
// revele, en una segunda conexión WebRTC real.
func TestSecureResume(t *testing.T) {
	host, user, pw := env(t, "TANGO_HOST"), env(t, "TANGO_E2E_USER"), env(t, "TANGO_E2E_PASSWORD")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	url := "wss://" + host + "/signal?v=1"
	c1, err := rtc.Dial(ctx, url, true)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	res, err := c1.Call(ctx, "auth.login", map[string]string{"user": user, "password": pw})
	if err != nil {
		t.Fatal(err)
	}
	var lr struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(res, &lr)

	c2, err := rtc.Dial(ctx, url, true)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	h1 := sha256.Sum256([]byte(lr.Token))
	id := auth.TokenID(hex.EncodeToString(h1[:]))
	nonce := "e2e-nonce-0123456789abcdef"
	res, err = c2.Call(ctx, "auth.challenge", map[string]string{"tokenId": id, "nonce": nonce})
	if err != nil {
		t.Fatal(err)
	}
	var pr struct {
		Proof string `json:"proof"`
	}
	_ = json.Unmarshal(res, &pr)
	if pr.Proof != auth.ResumeProof(h1[:], nonce) {
		t.Fatal("la prueba del NAS no coincide")
	}
	if _, err := c2.Call(ctx, "auth.challenge", map[string]string{"tokenId": "00000000000000000000000000000000", "nonce": nonce}); err == nil {
		t.Fatal("el NAS probó un token inexistente")
	}
	if _, err := c2.Call(ctx, "auth.resume", map[string]string{"token": lr.Token}); err != nil {
		t.Fatal(err)
	}
	_, _ = c1.Call(ctx, "auth.logout", nil)
}
