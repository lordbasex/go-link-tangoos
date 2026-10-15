//go:build e2e

package e2e

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	xssh "golang.org/x/crypto/ssh"
)

func insecureHTTP(follow bool) *http.Client {
	c := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // laboratorio autofirmado
	if !follow {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return c
}

// ---- Centro de apps: ciclo de vida completo con una app liviana ------------------------

func TestAppsLifecycle(t *testing.T) {
	a := admin(t)
	v1 := volume1(t, a)
	var st struct {
		Installed bool `json:"installed"`
		Running   bool `json:"running"`
	}
	a.call("apps.docker.status", nil, &st)
	if !st.Installed || !st.Running {
		t.Fatalf("Docker no está listo: %+v", st)
	}
	var cat []struct {
		ID string `json:"id"`
	}
	a.call("apps.catalog", nil, &cat)
	if len(cat) < 8 {
		t.Fatalf("catálogo con %d apps", len(cat))
	}
	const id = "syncthing"
	type inst struct {
		ID    string  `json:"id"`
		State string  `json:"state"`
		URL   *string `json:"url"`
	}
	find := func() (inst, bool) {
		var l []inst
		a.call("apps.list", nil, &l)
		for _, x := range l {
			if x.ID == id {
				return x, true
			}
		}
		return inst{}, false
	}
	if _, ok := find(); ok {
		t.Skipf("%s ya estaba instalada: no la toco", id)
	}
	var r jobRef
	a.call("apps.install", map[string]any{"id": id, "volume": v1.ID, "options": map[string]string{}}, &r)
	a.waitJob(r.Job, 15*time.Minute)
	t.Cleanup(func() {
		if _, ok := find(); ok {
			var u jobRef
			a.call("apps.uninstall", map[string]any{"id": id, "keepData": false}, &u)
			a.waitJobAny(u.Job, 5*time.Minute)
		}
	})
	app, ok := find()
	if !ok || app.State != "running" {
		t.Fatalf("después de instalar: %+v", app)
	}
	// Proxy con compuerta de sesión: sin cookie 401; ticket → cookie → 200.
	if app.URL != nil {
		cl := insecureHTTP(false)
		resp, err := cl.Get(*app.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("proxy sin cookie → %d, esperaba 401", resp.StatusCode)
		}
		var tk struct {
			URL string `json:"url"`
		}
		a.call("apps.ticket", map[string]string{"id": id}, &tk)
		resp, err = cl.Get(tk.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		cookies := resp.Cookies()
		if len(cookies) == 0 {
			t.Fatalf("el ticket no dejó cookie (status %d)", resp.StatusCode)
		}
		req, _ := http.NewRequest("GET", *app.URL, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		deadline := time.Now().Add(2 * time.Minute) // la app puede tardar en escuchar
		for {
			resp, err = cl.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 500 && resp.StatusCode != 401 {
					if resp.Header.Get("X-Frame-Options") != "" {
						t.Errorf("el proxy no quitó X-Frame-Options")
					}
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("la app no respondió por el proxy con cookie (último %v %v)", resp, err)
			}
			time.Sleep(3 * time.Second)
		}
		resp, err = cl.Get(tk.URL) // ticket de un solo uso
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("ticket reusado → %d, esperaba 401", resp.StatusCode)
			}
		}
	}
	for _, act := range []string{"stop", "start", "restart", "update"} {
		var j jobRef
		a.call("apps."+act, map[string]string{"id": id}, &j)
		a.waitJob(j.Job, 10*time.Minute)
		time.Sleep(2 * time.Second)
		cur, _ := find()
		want := "running"
		if act == "stop" {
			want = "stopped"
		}
		if cur.State != want {
			t.Fatalf("después de %s: estado %s", act, cur.State)
		}
	}
	var logs struct {
		Lines []string `json:"lines"`
	}
	a.call("apps.logs", map[string]any{"id": id, "tail": 50}, &logs)
	if len(logs.Lines) == 0 {
		t.Fatal("apps.logs vacío")
	}
	var u jobRef
	a.call("apps.uninstall", map[string]any{"id": id, "keepData": false}, &u)
	a.waitJob(u.Job, 5*time.Minute)
	if _, ok := find(); ok {
		t.Fatal("la app sigue en la lista después de desinstalar")
	}
	if out, _ := sshRun("test -e " + v1.Mount + "/@apps/" + id + " && echo QUEDA || echo ok"); strings.Contains(out, "QUEDA") {
		t.Fatal("uninstall keepData=false dejó los datos")
	}
	a.callErr("apps.install", map[string]any{"id": "../etc", "volume": v1.ID, "options": map[string]string{}}, "")
}

// ---- Servicios de archivos ---------------------------------------------------------------

func TestServicesWebDAVSFTPNFS(t *testing.T) {
	a := admin(t)
	v1 := volume1(t, a)
	pw := ensureTestUser(t, a)
	name := "E2E-Servicios"
	s := ensureShare(t, a, name, v1.ID)
	t.Cleanup(func() { deleteShare(t, a, s) })
	setUserAccess(t, a, s.ID, testUser, "rw")

	var svcs []struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
		Running bool   `json:"running"`
	}
	a.call("services.list", nil, &svcs)
	en := map[string]bool{}
	for _, x := range svcs {
		en[x.ID] = x.Enabled && x.Running
	}
	for _, id := range []string{"smb", "nfs", "sftp", "webdav", "iscsi"} {
		if !en[id] {
			t.Logf("aviso: el servicio %s no está activo (%v)", id, en)
		}
	}

	// WebDAV
	if en["webdav"] {
		cl := insecureHTTP(true)
		body := []byte("hola WebDAV " + time.Now().Format(time.RFC3339))
		put, _ := http.NewRequest("PUT", "https://"+host()+"/dav/"+url.PathEscape(name)+"/dav.txt", bytes.NewReader(body))
		put.SetBasicAuth(testUser, pw)
		resp, err := cl.Do(put)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 201 && resp.StatusCode != 204 {
			t.Fatalf("WebDAV PUT → %d", resp.StatusCode)
		}
		get, _ := http.NewRequest("GET", "https://"+host()+"/dav/"+url.PathEscape(name)+"/dav.txt", nil)
		get.SetBasicAuth(testUser, pw)
		resp, err = cl.Do(get)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !bytes.Equal(got, body) {
			t.Fatalf("WebDAV GET devolvió %q", got)
		}
		bad, _ := http.NewRequest("GET", "https://"+host()+"/dav/"+url.PathEscape(name)+"/dav.txt", nil)
		bad.SetBasicAuth(testUser, "mala")
		resp, _ = cl.Do(bad)
		if resp != nil {
			resp.Body.Close()
			if resp.StatusCode != 401 {
				t.Errorf("WebDAV con contraseña mala → %d", resp.StatusCode)
			}
		}
	}

	// SFTP (puerto 2222, servidor propio en Go sobre el file-worker).
	if en["sftp"] {
		t.Setenv("TANGO_HOST", host())
		t.Setenv("TANGO_E2E_USER", testUser)
		t.Setenv("TANGO_E2E_PASSWORD", pw)
		t.Run("SFTP", func(t *testing.T) { sftpRoundTrip(t, host(), testUser, pw, name) })
	}

	// NFS: export a loopback y montaje en el propio NAS.
	if en["nfs"] {
		a.call("nfs.exports.set", map[string]any{"share": s.ID, "clients": []map[string]string{{"host": "127.0.0.1", "access": "rw"}}}, nil)
		t.Cleanup(func() { a.call("nfs.exports.set", map[string]any{"share": s.ID, "clients": []any{}}, nil) })
		time.Sleep(2 * time.Second)
		out := onNAS(t, fmt.Sprintf(`set -e; d=$(mktemp -d); mount -t nfs -o vers=4.2,soft,timeo=50 127.0.0.1:%s $d; runuser -u %s -- ls $d; umount $d; rmdir $d`, s.Path, testUser))
		if !strings.Contains(out, "dav.txt") && en["webdav"] {
			t.Fatalf("el montaje NFS no muestra los archivos de la carpeta:\n%s", out)
		}
		a.callErr("nfs.exports.set", map[string]any{"share": s.ID, "clients": []map[string]string{{"host": "*; rm -rf /", "access": "rw"}}}, "")
	}
}

// ---- iSCSI: LUN con CHAP y login real con open-iscsi en el propio NAS --------------------

func TestServicesISCSI(t *testing.T) {
	a := admin(t)
	v1 := volume1(t, a)
	onNAS(t, "command -v iscsiadm >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get install -y -q open-iscsi >/dev/null")
	name := "e2e-lun"
	secret := "e2eChapSecret16x"
	a.callErr("iscsi.targets.create", map[string]any{"name": name, "volume": v1.ID, "sizeBytes": 256 << 20, "chap": map[string]string{"user": "e2e", "secret": "corto"}}, "")
	var r jobRef
	a.call("iscsi.targets.create", map[string]any{"name": name, "volume": v1.ID, "sizeBytes": 256 << 20, "chap": map[string]string{"user": "e2e", "secret": secret}}, &r)
	if r.Job != "" {
		a.waitJob(r.Job, 2*time.Minute)
	}
	t.Cleanup(func() {
		_, _ = sshRun("iscsiadm -m node -u >/dev/null 2>&1; iscsiadm -m node -o delete >/dev/null 2>&1; true")
		var d jobRef
		if _, err := a.Call(ctxT(t, time.Minute), "iscsi.targets.delete", map[string]string{"name": name, "typed": name}); err == nil {
			_ = d
		}
	})
	var targets []struct {
		Name string `json:"name"`
		IQN  string `json:"iqn"`
	}
	a.call("iscsi.targets.list", nil, &targets)
	var iqn string
	for _, x := range targets {
		if x.Name == name {
			iqn = x.IQN
		}
	}
	if iqn == "" {
		t.Fatalf("el destino no aparece: %+v", targets)
	}
	script := fmt.Sprintf(`set -e
iscsiadm -m discovery -t st -p 127.0.0.1 >/dev/null
iscsiadm -m node -T %[1]s -p 127.0.0.1 --op update -n node.session.auth.authmethod -v CHAP
iscsiadm -m node -T %[1]s -p 127.0.0.1 --op update -n node.session.auth.username -v e2e
iscsiadm -m node -T %[1]s -p 127.0.0.1 --op update -n node.session.auth.password -v %[2]s
iscsiadm -m node -T %[1]s -p 127.0.0.1 -l >/dev/null
sleep 2; lsblk -S -o NAME,VENDOR,MODEL | grep -i LIO
iscsiadm -m node -T %[1]s -p 127.0.0.1 -u >/dev/null`, iqn, secret)
	out := onNAS(t, script)
	if !strings.Contains(out, "LIO") {
		t.Fatalf("no apareció el disco iSCSI:\n%s", out)
	}
	// CHAP malo no entra.
	bad := fmt.Sprintf(`iscsiadm -m node -T %[1]s -p 127.0.0.1 --op update -n node.session.auth.password -v otraClaveMala123 && iscsiadm -m node -T %[1]s -p 127.0.0.1 -l 2>&1; echo rc=$?`, iqn)
	if out, _ := sshRun(bad); strings.Contains(out, "rc=0") {
		t.Fatalf("login iSCSI con CHAP malo funcionó:\n%s", out)
	}
	a.callErr("iscsi.targets.delete", map[string]string{"name": name, "typed": "otro"}, "")
}

// ---- M4: auditoría, logs, terminal, certificado, notificaciones, actualizaciones ---------

func TestM4AuditLogsTerminal(t *testing.T) {
	a := admin(t)
	pw := ensureTestUser(t, a)
	bad := dial(t)
	bad.callErr("auth.login", map[string]string{"user": testUser, "password": "incorrecta-e2e"}, "")
	time.Sleep(time.Second)
	var evs []struct {
		Actor, Method, Result, Target string
	}
	a.call("audit.list", map[string]any{"limit": 200}, &evs)
	var sawFail, sawSetPw bool
	for _, e := range evs {
		if e.Method == "auth.login" && e.Result != "ok" {
			sawFail = true
		}
		if e.Method == "users.setPassword" && e.Actor == "admin" {
			sawSetPw = true
		}
	}
	if !sawFail || !sawSetPw {
		t.Fatalf("auditoría incompleta: login fallido=%v setPassword=%v", sawFail, sawSetPw)
	}
	if out, _ := sshRun("grep -cF -- '" + pw + "' /var/lib/tango/audit.jsonl || true"); strings.TrimSpace(out) != "0" {
		t.Fatalf("la contraseña aparece en audit.jsonl")
	}
	var lines []map[string]any
	a.call("logs.query", map[string]any{"unit": "tangod", "limit": 20}, &lines)
	if len(lines) == 0 {
		t.Fatal("logs.query vacío")
	}
	out, err := a.TermExec(ctxT(t, 30*time.Second), "id -u; uname -s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "0") || !strings.Contains(out, "Linux") {
		t.Fatalf("terminal: %q", out)
	}
	var setup struct {
		State string `json:"state"`
	}
	a.call("setup.state", nil, &setup)
	if setup.State != "done" {
		t.Fatalf("setup.state = %s", setup.State)
	}
}

func TestM4CertAndUpdates(t *testing.T) {
	a := admin(t)
	var ci struct {
		Mode        string   `json:"mode"`
		SANs        []string `json:"sans"`
		NotAfter    string   `json:"notAfter"`
		Fingerprint string   `json:"fingerprint"`
	}
	a.call("cert.info", nil, &ci)
	if ci.Fingerprint == "" || len(ci.SANs) == 0 {
		t.Fatalf("cert.info incompleto: %+v", ci)
	}
	t.Logf("certificado %s (la CA local se prueba en la VM 109 para no cambiar el certificado que el usuario ya aceptó)", ci.Mode)
	var upd struct {
		Debian    []map[string]any `json:"debian"`
		CheckedAt string           `json:"checkedAt"`
	}
	res, err := a.Call(ctxT(t, 5*time.Minute), "system.updates.check", nil)
	if err != nil {
		t.Fatalf("system.updates.check: %v", err)
	}
	_ = json.Unmarshal(res, &upd)
	if upd.CheckedAt == "" {
		t.Fatalf("updates.check sin checkedAt: %s", truncate(string(res), 200))
	}
	if _, err := a.Call(ctxT(t, time.Minute), "system.rollback.list", nil); err != nil {
		t.Logf("rollback.list en la VM 108 (raíz ext4 de imagen cloud): %v (esperado; se prueba en la VM 109)", err)
	}
	a.callErr("system.reboot", map[string]string{"typed": "reiniciar"}, "")
	a.callErr("system.rollback.apply", map[string]string{"id": "no-existe", "typed": "VOLVER"}, "")
}

func TestM4Notifications(t *testing.T) {
	a := admin(t)
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "e2e-sink")
	build := exec.Command("go", "build", "-o", bin, "./sink")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compilar sink: %v\n%s", err, out)
	}
	scp := exec.Command("scp", "-q", "-i", sshKey(), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", bin, "root@"+host()+":/tmp/e2e-sink")
	if out, err := scp.CombinedOutput(); err != nil {
		t.Fatalf("scp: %v %s", err, out)
	}
	onNAS(t, "pkill -x e2e-sink; rm -f /tmp/e2e-sink.log; (setsid /tmp/e2e-sink >/dev/null 2>&1 &); sleep 1")
	t.Cleanup(func() { _, _ = sshRun("pkill -x e2e-sink; rm -f /tmp/e2e-sink /tmp/e2e-sink.log") })

	var prev json.RawMessage
	res, err := a.Call(ctxT(t, 20*time.Second), "notify.channels.get", nil)
	if err != nil {
		t.Fatal(err)
	}
	prev = res
	t.Cleanup(func() {
		var p map[string]any
		_ = json.Unmarshal(prev, &p)
		a.call("notify.channels.set", p, nil)
	})
	cfg := map[string]any{
		"email":    map[string]any{"enabled": true, "host": "127.0.0.1", "port": 18025, "user": "", "password": "", "from": "nas@tango.lab", "to": "admin@tango.lab", "tls": "none"},
		"ntfy":     map[string]any{"enabled": true, "server": "http://127.0.0.1:18081", "topic": "tango-e2e", "token": "tk_e2e_secreto"},
		"telegram": map[string]any{"enabled": false, "botToken": "", "chatId": ""},
		"levels":   []string{"crit", "warn"},
	}
	a.call("notify.channels.set", cfg, nil)
	var got struct {
		Ntfy struct {
			Token string `json:"token"`
		} `json:"ntfy"`
	}
	a.call("notify.channels.get", nil, &got)
	if strings.Contains(got.Ntfy.Token, "tk_e2e_secreto") {
		t.Fatal("notify.channels.get devuelve el token en claro")
	}
	a.call("notify.test", map[string]string{"channel": "ntfy"}, nil)
	a.call("notify.test", map[string]string{"channel": "email"}, nil)
	time.Sleep(2 * time.Second)
	log := onNAS(t, "cat /tmp/e2e-sink.log || true")
	if !strings.Contains(log, `"kind":"http"`) || !strings.Contains(log, "/tango-e2e") || !strings.Contains(log, "Bearer tk_e2e_secreto") {
		t.Fatalf("ntfy no llegó al receptor:\n%s", log)
	}
	if !strings.Contains(log, `"kind":"smtp"`) {
		t.Fatalf("el correo no llegó al receptor:\n%s", log)
	}
	if out, _ := sshRun("grep -c tk_e2e_secreto /var/lib/tango/notify.json; stat -c %a /var/lib/tango/notify.json"); !strings.Contains(out, "600") {
		t.Fatalf("notify.json sin permisos 0600: %s", out)
	}
}

// sftpRoundTrip: escribir, leer, renombrar y borrar por SFTP; traversal y clave mala fallan.
func sftpRoundTrip(t *testing.T, h, user, pw, share string) {
	cfg := &xssh.ClientConfig{User: user, Auth: []xssh.AuthMethod{xssh.Password(pw)}, HostKeyCallback: xssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second} //nolint:gosec // laboratorio
	conn, err := xssh.Dial("tcp", h+":2222", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	c, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	payload := []byte("hola SFTP " + time.Now().Format(time.RFC3339Nano))
	f, err := c.Create("/" + share + "/sftp.txt")
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	_, _ = f.Write(payload)
	if err := f.Close(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	r, err := c.Open("/" + share + "/sftp.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("contenido %q", got)
	}
	if err := c.Rename("/"+share+"/sftp.txt", "/"+share+"/sftp-2.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Open("/" + share + "/../../etc/passwd"); err == nil {
		t.Fatal("traversal por SFTP")
	}
	if err := c.Remove("/" + share + "/sftp-2.txt"); err != nil {
		t.Fatal(err)
	}
	bad := &xssh.ClientConfig{User: user, Auth: []xssh.AuthMethod{xssh.Password("incorrecta")}, HostKeyCallback: xssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second} //nolint:gosec
	if c2, err := xssh.Dial("tcp", h+":2222", bad); err == nil {
		c2.Close()
		t.Fatal("SFTP aceptó una contraseña mala")
	}
}
