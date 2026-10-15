//go:build e2e

package e2e

import (
	"crypto/tls"
	"net/http"
	"strings"
	"testing"
	"time"
)

// publicMethods son los únicos que deben responder sin sesión (protocol/README.md §3, §12).
var publicMethods = map[string]bool{
	"auth.users": true, "auth.login": true, "auth.resume": true, "auth.challenge": true,
	"system.info": true, "setup.state": true, "setup.complete": true,
	"auth.totp": true, // segundo factor del login (2FA)
	"connect.hello": true, // TangoConnect: reemplaza al hello en conexiones remotas (§15)
}

// remoteOnly: TangoConnect (§15). En una conexión local el gate los rechaza con FORBIDDEN antes
// de mirar la sesión; también es un rechazo válido.
var remoteOnly = map[string]bool{
	"connect.link": true, "connect.challenge": true, "connect.resume": true,
	"bundle.manifest": true, "bundle.get": true,
	"guest.open": true, "guest.info": true, "guest.unlock": true, "guest.list": true, "guest.download.begin": true,
}

// TestSecurityUnauthenticated: todo método no público rechaza sin sesión.
func TestSecurityUnauthenticated(t *testing.T) {
	c := dial(t)
	for _, m := range c.Hello.Methods {
		if publicMethods[m] {
			continue
		}
		// params vacíos: el rechazo tiene que llegar antes de validar.
		re := c.callErr(m, map[string]any{}, "")
		// Ya hay más métodos que la ráfaga por sesión (100): el limitador corta y se espera.
		for i := 0; re.Code == "RATE_LIMITED" && i < 20; i++ {
			time.Sleep(100 * time.Millisecond)
			re = c.callErr(m, map[string]any{}, "")
		}
		if re.Code != "UNAUTHENTICATED" && !(remoteOnly[m] && re.Code == "FORBIDDEN") {
			t.Errorf("%s sin sesión respondió %s (%s), esperaba UNAUTHENTICATED", m, re.Code, re.Message)
		}
	}
	// setup.complete con el setup ya hecho no debe crear nada.
	re := c.callErr("setup.complete", map[string]any{"hostname": "x", "admin": map[string]string{"name": "x", "full": "x", "password": "Xx-123456789"}, "timezone": "UTC", "certificate": "selfsigned"}, "")
	t.Logf("setup.complete con setup hecho → %s", re.Code)
}

// TestSecurityNonAdmin: un usuario común no puede administrar. Los params son inválidos a
// propósito: si falta el chequeo de rol, el error es otro (y nada se ejecuta).
func TestSecurityNonAdmin(t *testing.T) {
	a := admin(t)
	pw := ensureTestUser(t, a)
	u := loginAs(t, testUser, pw)
	cases := []struct {
		method string
		params any
	}{
		{"storage.volumes.plan", map[string]any{"name": "e2e-noadmin", "profile": "raid1", "disks": []string{}}},
		{"storage.volumes.create", map[string]any{"plan_id": "p_x", "confirm_token": "x", "typed_name": "x"}},
		{"storage.volumes.deletePlan", map[string]any{"volume": "no-existe"}},
		{"storage.volumes.delete", map[string]any{"plan_id": "p_x", "confirm_token": "x", "typed_name": "x"}},
		{"storage.volumes.expandPlan", map[string]any{"volume": "no-existe", "add": []string{}}},
		{"storage.disks.replacePlan", map[string]any{"volume": "no-existe", "replacement": "x"}},
		{"storage.scrub.start", map[string]any{"volume": "no-existe"}},
		{"shares.create", map[string]any{"name": "", "volume": "no-existe", "description": "", "smb": false}},
		{"shares.delete", map[string]any{"share": "no-existe", "typed_name": "x"}},
		{"shares.permissions.set", map[string]any{"share": "no-existe", "entries": []any{}}},
		{"snapshots.create", map[string]any{"share": "no-existe"}},
		{"snapshots.delete", map[string]any{"id": "no-existe"}},
		{"snapshots.schedule.set", map[string]any{"share": "no-existe", "schedule": map[string]any{}}},
		{"users.create", map[string]any{"name": "e2e-no", "full": "x", "email": "", "password": "x", "admin": true, "groups": []string{}}},
		{"users.setPassword", map[string]any{"name": "admin", "password": "x"}},
		{"apps.install", map[string]any{"id": "no-existe", "volume": "no-existe", "options": map[string]string{}}},
		{"apps.uninstall", map[string]any{"id": "no-existe", "keepData": true}},
		{"apps.stop", map[string]any{"id": "no-existe"}},
		{"apps.docker.install", map[string]any{}},
		{"services.set", map[string]any{"id": "no-existe", "enabled": false}},
		{"nfs.exports.set", map[string]any{"share": "no-existe", "clients": []any{}}},
		{"iscsi.targets.create", map[string]any{"name": "", "volume": "no-existe", "sizeBytes": 0}},
		{"iscsi.targets.delete", map[string]any{"name": "no-existe", "typed": "x"}},
		{"notify.test", map[string]any{"channel": "no-existe"}},
		{"audit.list", map[string]any{"limit": 1}},
		{"logs.query", map[string]any{"limit": 1}},
		{"term.open", map[string]any{"cols": 80, "rows": 24}},
		{"system.reboot", map[string]any{"typed": "NO"}},
		{"system.shutdown", map[string]any{"typed": "NO"}},
		{"system.updates.apply", map[string]any{"scope": "no-existe"}},
		{"system.rollback.apply", map[string]any{"id": "no-existe", "typed": "NO"}},
	}
	for _, tc := range cases {
		re := u.callErr(tc.method, tc.params, "")
		if re.Code != "PERMISSION_DENIED" {
			t.Errorf("%s como usuario común → %s (%s), esperaba PERMISSION_DENIED", tc.method, re.Code, re.Message)
		}
	}
}

// TestSecurityRouterValidation: método desconocido y campos de más.
func TestSecurityRouterValidation(t *testing.T) {
	a := admin(t)
	a.callErr("no.existe", nil, "METHOD_NOT_FOUND")
	a.callErr("files.list", map[string]any{"path": "/", "inyectado": true}, "INVALID_PARAMS")
	a.callErr("storage.volumes.plan", map[string]any{"name": "x", "profile": "raid1", "disks": []string{}, "force": true}, "INVALID_PARAMS")
}

// TestSecurityAgentSocket: el socket del agente rechaza peers no autorizados y ops desconocidas.
func TestSecurityAgentSocket(t *testing.T) {
	py := `import socket,json,sys
s=socket.socket(socket.AF_UNIX); s.settimeout(5)
try:
    s.connect("/run/tango/agent.sock")
except Exception as e:
    print("CONNECT_ERR", type(e).__name__); sys.exit(0)
for msg in sys.argv[1:]:
    s.sendall((msg+"\n").encode())
    try:
        print(s.recv(65536).decode().strip() or "EOF")
    except Exception as e:
        print("RECV_ERR", type(e).__name__)
`
	onNAS(t, "cat > /tmp/e2e-agent.py <<'EOF'\n"+py+"EOF")
	// Usuario sin privilegios: no puede ni conectar (permisos del socket) o SO_PEERCRED corta.
	out := onNAS(t, `runuser -u nobody -- python3 /tmp/e2e-agent.py '{"v":1,"id":1,"op":"agent.version","args":{}}' 2>&1 || true`)
	if !strings.Contains(out, "CONNECT_ERR") && !strings.Contains(out, "EOF") && !strings.Contains(out, "RECV_ERR") {
		t.Fatalf("nobody habló con el agente: %s", out)
	}
	// root: op desconocida y campos de más se rechazan.
	out = onNAS(t, `python3 /tmp/e2e-agent.py '{"v":1,"id":1,"op":"shell.exec","args":{"cmd":"id"}}' '{"v":1,"id":2,"op":"agent.version","args":{},"extra":1}' '{"v":9,"id":3,"op":"agent.version","args":{}}'`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 1 || !strings.Contains(lines[0], `"ok":false`) {
		t.Fatalf("op desconocida no rechazada: %s", out)
	}
	for _, l := range lines {
		if strings.Contains(l, `"ok":true`) {
			t.Fatalf("payload inválido aceptado: %s", l)
		}
	}
	onNAS(t, "rm -f /tmp/e2e-agent.py")
}

// TestSecurityHTTPHeaders: cabeceras del sitio HTTPS y redirección de :80.
func TestSecurityHTTPHeaders(t *testing.T) {
	cl := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // laboratorio autofirmado
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := cl.Get("https://" + host() + "/")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Strict-Transport-Security", "Referrer-Policy"} {
		if r.Header.Get(h) == "" {
			t.Errorf("falta la cabecera %s", h)
		}
	}
	if csp := r.Header.Get("Content-Security-Policy"); strings.Contains(csp, "'unsafe-eval'") || !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP débil: %s", csp)
	}
	r2, err := cl.Get("http://" + host() + "/algo?x=1")
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode/100 != 3 || !strings.HasPrefix(r2.Header.Get("Location"), "https://") {
		t.Errorf(":80 no redirige a HTTPS: %d %s", r2.StatusCode, r2.Header.Get("Location"))
	}
	// Un asset inexistente da 404, no la SPA.
	r3, err := cl.Get("https://" + host() + "/assets/no-existe.js")
	if err != nil {
		t.Fatal(err)
	}
	r3.Body.Close()
	if r3.StatusCode != 404 {
		t.Errorf("asset inexistente → %d", r3.StatusCode)
	}
}

// TestSecuritySecretsNotLogged corre al final de la suite de seguridad: ninguna contraseña
// usada en esta corrida aparece en el journal ni en la auditoría.
func TestSecuritySecretsNotLogged(t *testing.T) {
	a := admin(t)
	pw := ensureTestUser(t, a)
	_ = loginAs(t, testUser, pw)
	time.Sleep(2 * time.Second)
	for name, secret := range map[string]string{"admin": adminPassword(t), testUser: pw} {
		out, _ := sshRun("journalctl --since '-2h' --no-pager -o cat | grep -cF -- '" + secret + "' ; grep -cF -- '" + secret + "' /var/lib/tango/audit.jsonl* 2>/dev/null | awk -F: '{s+=$NF} END {print s+0}'")
		for _, n := range strings.Fields(out) {
			if n != "0" {
				t.Errorf("la contraseña de %s aparece en logs/auditoría (%s)", name, strings.TrimSpace(out))
				break
			}
		}
	}
}
