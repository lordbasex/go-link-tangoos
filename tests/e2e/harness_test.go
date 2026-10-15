//go:build e2e

// Harness E2E de TangoOS contra un NAS real (docs/14-plan-de-pruebas.md).
//
// Variables:
//
//	TANGO_HOST               host del NAS (por defecto 172.17.0.44)
//	TANGO_SSH_KEY            clave SSH root del laboratorio (por defecto ~/.ssh/id_ed25519)
//	TANGO_E2E_DESTRUCTIVE=1  habilita la matriz de almacenamiento y el reinicio de la VM
//	TANGO_E2E_REBOOT=1       habilita la fila 9 (reinicio de la VM)
//	TANGO_E2E_BIGFILE_MB     tamaño del archivo grande (por defecto 1024)
//
// La contraseña de admin se lee por SSH de /var/lib/tango/initial-admin-password y nunca
// se imprime.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/rtc"
)

// ---- configuración ------------------------------------------------------------------

func host() string {
	if h := os.Getenv("TANGO_HOST"); h != "" {
		return h
	}
	return "172.17.0.44"
}

func signalURL() string { return "wss://" + host() + "/signal?v=1" }

func sshKey() string {
	if k := os.Getenv("TANGO_SSH_KEY"); k != "" {
		return k
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh/id_ed25519")
}

func destructive(t *testing.T) {
	t.Helper()
	if os.Getenv("TANGO_E2E_DESTRUCTIVE") != "1" {
		t.Skip("prueba destructiva: exportá TANGO_E2E_DESTRUCTIVE=1")
	}
}

// ---- SSH al NAS (setup/verificación del laboratorio, nunca como sustituto de la API) ---

func sshRun(cmd string) (string, error) {
	args := []string{"-i", sshKey(), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", "-o", "ConnectTimeout=10", "-o", "BatchMode=yes", "root@" + host(), cmd}
	var out, errb bytes.Buffer
	c := exec.Command("ssh", args...)
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	if err != nil {
		return out.String(), fmt.Errorf("ssh %q: %w: %s", cmd, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func onNAS(t *testing.T, cmd string) string {
	t.Helper()
	out, err := sshRun(cmd)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ---- credenciales -------------------------------------------------------------------

var (
	adminOnce sync.Once
	adminPW   string
	adminErr  error
)

func adminPassword(t *testing.T) string {
	t.Helper()
	adminOnce.Do(func() {
		if v := os.Getenv("TANGO_E2E_ADMIN_PASSWORD"); v != "" {
			adminPW = v
			return
		}
		out, err := sshRun("cat /var/lib/tango/initial-admin-password")
		adminPW, adminErr = strings.TrimSpace(out), err
	})
	if adminErr != nil || adminPW == "" {
		t.Skipf("no pude leer la contraseña de admin: %v", adminErr)
	}
	return adminPW
}

func randomSecret(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return "E2e-" + hex.EncodeToString(b)
}

// ---- cliente --------------------------------------------------------------------------

type client struct {
	*rtc.Client
	t    *testing.T
	user string
}

func ctxT(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// dial abre una conexión WebRTC real sin iniciar sesión.
func dial(t *testing.T) *client {
	t.Helper()
	var c *rtc.Client
	var err error
	for i := 0; i < 5; i++ { // el NAS puede estar reiniciando tangod
		c, err = rtc.Dial(ctxT(t, 30*time.Second), signalURL(), true)
		if err == nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &client{Client: c, t: t}
}

func loginAs(t *testing.T, user, pw string) *client {
	t.Helper()
	c := dial(t)
	if _, err := c.Call(ctxT(t, 20*time.Second), "auth.login", map[string]string{"user": user, "password": pw}); err != nil {
		t.Fatalf("login %s: %v", user, err)
	}
	c.user = user
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = c.Call(ctx, "auth.logout", nil)
	})
	return c
}

func admin(t *testing.T) *client { return loginAs(t, "admin", adminPassword(t)) }

// call llama y decodifica; falla el test ante cualquier error.
func (c *client) call(method string, params any, out any) {
	c.t.Helper()
	res, err := c.Call(ctxT(c.t, 60*time.Second), method, params)
	if err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	if out != nil {
		if err := json.Unmarshal(res, out); err != nil {
			c.t.Fatalf("%s: decodificar %s: %v", method, res, err)
		}
	}
}

// callErr espera un error RPC con ese código ("" = cualquiera).
func (c *client) callErr(method string, params any, code string) *rtc.RPCError {
	c.t.Helper()
	res, err := c.Call(ctxT(c.t, 60*time.Second), method, params)
	if err == nil {
		c.t.Fatalf("%s: esperaba error %s y respondió %s", method, code, truncate(string(res), 200))
	}
	var re *rtc.RPCError
	if !errors.As(err, &re) {
		c.t.Fatalf("%s: error no RPC: %v", method, err)
	}
	if code != "" && re.Code != code {
		c.t.Fatalf("%s: código %s (%s), esperaba %s", method, re.Code, re.Message, code)
	}
	return re
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ---- jobs ------------------------------------------------------------------------------

type job struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Title    string  `json:"title"`
	Detail   string  `json:"detail"`
	Progress float64 `json:"progress"`
	State    string  `json:"state"`
}

// waitJob sondea jobs.list hasta que el job termina; falla si no es succeeded.
func (c *client) waitJob(id string, timeout time.Duration) job {
	c.t.Helper()
	j := c.waitJobAny(id, timeout)
	if j.State != "succeeded" {
		c.t.Fatalf("job %s (%s) terminó %s: %s", id, j.Title, j.State, j.Detail)
	}
	return j
}

func (c *client) waitJobAny(id string, timeout time.Duration) job {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	var last job
	for time.Now().Before(deadline) {
		var jobs []job
		c.call("jobs.list", nil, &jobs)
		for _, j := range jobs {
			if j.ID == id {
				last = j
				switch j.State {
				case "succeeded", "failed", "cancelled", "interrupted":
					return j
				}
			}
		}
		time.Sleep(time.Second)
	}
	c.t.Fatalf("job %s no terminó en %s (último: %+v)", id, timeout, last)
	return last
}

type jobRef struct {
	Job string `json:"job"`
}

// ---- almacenamiento ---------------------------------------------------------------------

type disk struct {
	ID     string `json:"id"`
	Dev    string `json:"dev"`
	Bay    *int   `json:"bay"`
	Model  string `json:"model"`
	Serial string `json:"serial"`
	Size   uint64 `json:"size"`
	Use    string `json:"use"`
	Volume string `json:"volume"`
}

type volume struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Mount   string   `json:"mount"`
	Profile string   `json:"profile"`
	Size    uint64   `json:"size"`
	Used    uint64   `json:"used"`
	State   string   `json:"state"`
	Disks   []string `json:"disks"`
	Task    *string  `json:"task"`
}

func (c *client) disks() map[string]disk {
	c.t.Helper()
	var ds []disk
	c.call("storage.disks.list", nil, &ds)
	m := map[string]disk{}
	for _, d := range ds {
		m[d.Serial] = d
	}
	return m
}

func (c *client) volumes() map[string]volume {
	c.t.Helper()
	var vs []volume
	c.call("storage.volumes.list", nil, &vs)
	m := map[string]volume{}
	for _, v := range vs {
		m[v.Name] = v
	}
	return m
}

func (c *client) diskIDs(serials ...string) []string {
	c.t.Helper()
	ds := c.disks()
	out := make([]string, 0, len(serials))
	for _, s := range serials {
		d, ok := ds[s]
		if !ok {
			c.t.Fatalf("no existe el disco %s", s)
		}
		out = append(out, d.ID)
	}
	return out
}

// waitVolume espera a que el volumen esté en el estado pedido (o desaparezca si state == "").
func (c *client) waitVolume(name, state string, timeout time.Duration) volume {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	var v volume
	var ok bool
	for time.Now().Before(deadline) {
		v, ok = c.volumes()[name]
		if state == "" && !ok {
			return v
		}
		if ok && v.State == state && (v.Task == nil || *v.Task == "") {
			return v
		}
		time.Sleep(2 * time.Second)
	}
	c.t.Fatalf("volumen %s: no llegó a %q en %s (ahora %+v, existe=%v)", name, state, timeout, v, ok)
	return v
}

// ---- carpetas y usuario de pruebas -----------------------------------------------------

const testUser = "e2etester"

// ensureTestUser crea (o resetea) el usuario de pruebas con una contraseña nueva por corrida.
func ensureTestUser(t *testing.T, a *client) string {
	t.Helper()
	pw := randomSecret(8)
	_, err := a.Call(ctxT(t, 60*time.Second), "users.create", map[string]any{
		"name": testUser, "full": "Usuario E2E", "email": "e2e@tango.lab", "password": pw, "admin": false, "groups": []string{},
	})
	if err != nil {
		var re *rtc.RPCError
		if errors.As(err, &re) && re.Code == "USER_EXISTS" {
			a.call("users.setPassword", map[string]string{"name": testUser, "password": pw}, nil)
		} else {
			t.Fatalf("users.create: %v", err)
		}
	}
	return pw
}

type share struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Volume string `json:"volume"`
	Path   string `json:"path"`
	SMB    bool   `json:"smb"`
}

func (c *client) shares() map[string]share {
	c.t.Helper()
	var ss []share
	c.call("shares.list", nil, &ss)
	m := map[string]share{}
	for _, s := range ss {
		m[s.Name] = s
	}
	return m
}

// ensureShare crea la carpeta en el volumen si no existe y la devuelve.
func ensureShare(t *testing.T, a *client, name, volumeID string) share {
	t.Helper()
	if s, ok := a.shares()[name]; ok {
		return s
	}
	var r jobRef
	a.call("shares.create", map[string]any{"name": name, "volume": volumeID, "description": "Carpeta de pruebas E2E", "smb": true}, &r)
	a.waitJob(r.Job, 2*time.Minute)
	s, ok := a.shares()[name]
	if !ok {
		t.Fatalf("la carpeta %s no aparece después de crearla", name)
	}
	return s
}

func deleteShare(t *testing.T, a *client, s share) {
	t.Helper()
	var r jobRef
	a.call("shares.delete", map[string]string{"share": s.ID, "typed_name": s.Name}, &r)
	a.waitJob(r.Job, 2*time.Minute)
}

type permEntry struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Access string `json:"access"`
}

// setUserAccess cambia solo la entrada de un usuario y devuelve una función que restaura.
func setUserAccess(t *testing.T, a *client, shareID, user, access string) {
	t.Helper()
	var cur struct {
		Entries []permEntry `json:"entries"`
	}
	a.call("shares.permissions.get", map[string]string{"share": shareID}, &cur)
	next := []permEntry{}
	for _, e := range cur.Entries {
		if !(e.Kind == "user" && e.Name == user) {
			next = append(next, e)
		}
	}
	if access != "" {
		next = append(next, permEntry{Kind: "user", Name: user, Access: access})
	}
	a.call("shares.permissions.set", map[string]any{"share": shareID, "entries": next}, nil)
}

// volume1 es el fixture permanente (RAID 1); las pruebas no destructivas trabajan ahí.
func volume1(t *testing.T, a *client) volume {
	t.Helper()
	v, ok := a.volumes()["volume1"]
	if !ok {
		t.Skip("no existe volume1 (fixture del laboratorio)")
	}
	return v
}
