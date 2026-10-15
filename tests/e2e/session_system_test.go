//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ---- Sesión (docs/14: login ok/err, lockout, resume tras reinicio, logout, gating) ------

func TestSessionLoginAndGating(t *testing.T) {
	c := dial(t)
	if len(c.Hello.Methods) < 50 {
		t.Fatalf("hello.methods sospechosamente corto: %d", len(c.Hello.Methods))
	}
	// Público sin sesión.
	var users []map[string]any
	c.call("auth.users", nil, &users)
	if len(users) == 0 {
		t.Fatal("auth.users vacío")
	}
	var info map[string]any
	c.call("system.info", nil, &info)
	// Gated sin sesión.
	c.callErr("storage.volumes.list", nil, "UNAUTHENTICATED")
	c.callErr("files.list", map[string]string{"path": "/"}, "UNAUTHENTICATED")
	// Credenciales malas.
	c.callErr("auth.login", map[string]string{"user": "admin", "password": "esta-no-es"}, "INVALID_CREDENTIALS")
	// Credenciales buenas.
	var lr struct {
		Token string         `json:"token"`
		User  map[string]any `json:"user"`
	}
	c.call("auth.login", map[string]string{"user": "admin", "password": adminPassword(t)}, &lr)
	if lr.Token == "" || lr.User["name"] != "admin" {
		t.Fatalf("login sin token/usuario: %+v", lr.User)
	}
	var vols []volume
	c.call("storage.volumes.list", nil, &vols)
	c.call("auth.logout", nil, nil)
	c.callErr("storage.volumes.list", nil, "UNAUTHENTICATED")
	// El token del logout ya no sirve.
	c2 := dial(t)
	c2.callErr("auth.resume", map[string]string{"token": lr.Token}, "")
}

func TestSessionLockout(t *testing.T) {
	c := dial(t)
	user := "e2e-lockout-" + time.Now().Format("150405") // nunca bloquear usuarios reales
	var lastCode string
	for i := 0; i < 7; i++ {
		re := c.callErr("auth.login", map[string]string{"user": user, "password": "mal"}, "")
		lastCode = re.Code
		if re.Code == "RATE_LIMITED" {
			t.Logf("bloqueado después de %d intentos", i)
			return
		}
	}
	t.Fatalf("7 intentos fallidos sin bloqueo (último código %s)", lastCode)
}

// TestSessionResumeAfterRestart reinicia tangod y verifica que el token sigue sirviendo.
func TestSessionResumeAfterRestart(t *testing.T) {
	c := dial(t)
	var lr struct {
		Token string `json:"token"`
	}
	c.call("auth.login", map[string]string{"user": "admin", "password": adminPassword(t)}, &lr)
	onNAS(t, "systemctl restart tangod")
	time.Sleep(3 * time.Second)
	c2 := dial(t)
	var rr struct {
		User map[string]any `json:"user"`
	}
	c2.call("auth.resume", map[string]string{"token": lr.Token}, &rr)
	if rr.User["name"] != "admin" {
		t.Fatalf("resume devolvió %v", rr.User)
	}
	c2.call("auth.logout", nil, nil)
}

// ---- Sistema ---------------------------------------------------------------------------

func TestSystemInfoMetricsRTC(t *testing.T) {
	a := admin(t)
	var info struct {
		Hostname string `json:"hostname"`
		Version  string `json:"version"`
		Debian   string `json:"debian"`
		Kernel   string `json:"kernel"`
		Cores    int    `json:"cores"`
		MemTotal uint64 `json:"memTotal"`
	}
	a.call("system.info", nil, &info)
	if info.Hostname == "" || info.Version == "" || !strings.Contains(info.Debian, "13") || info.Cores < 1 || info.MemTotal == 0 {
		t.Fatalf("system.info incompleto: %+v", info)
	}
	// Métricas a 4 Hz por el canal unordered.
	deadline := time.After(3 * time.Second)
	n := 0
loop:
	for {
		select {
		case m := <-a.Metrics:
			var f struct {
				T   int64   `json:"t"`
				CPU float64 `json:"cpu"`
			}
			if err := json.Unmarshal(m, &f); err != nil || f.T == 0 {
				t.Fatalf("frame de métricas inválido: %s", m)
			}
			n++
		case <-deadline:
			break loop
		}
	}
	if n < 8 { // 3 s × 4 Hz = 12, con margen
		t.Fatalf("solo %d frames de métricas en 3 s (esperaba ~12)", n)
	}
	var st struct {
		ICE      string `json:"ice"`
		Channels []struct {
			Label string `json:"label"`
		} `json:"channels"`
	}
	a.call("rtc.stats", nil, &st)
	if st.ICE != "connected" && st.ICE != "completed" {
		t.Fatalf("ICE %q", st.ICE)
	}
	labels := map[string]bool{}
	for _, ch := range st.Channels {
		labels[ch.Label] = true
	}
	for _, l := range []string{"rpc", "events", "metrics", "files.0"} {
		if !labels[l] {
			t.Fatalf("falta el canal %s en rtc.stats (%v)", l, labels)
		}
	}
	var sess []map[string]any
	a.call("sessions.list", nil, &sess)
	if len(sess) == 0 {
		t.Fatal("sessions.list vacío")
	}
	var disks []disk
	a.call("storage.disks.list", nil, &disks)
	sys := 0
	for _, d := range disks {
		if d.Use == "system" {
			sys++
		}
	}
	if sys != 1 {
		t.Fatalf("esperaba exactamente 1 disco de sistema, hay %d", sys)
	}
}
