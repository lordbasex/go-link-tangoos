//go:build e2e

package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TOTP RFC 6238 usa HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
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
)

// ---- Borrar usuarios (§14) --------------------------------------------------------------

func TestUsersDelete(t *testing.T) {
	a := admin(t)
	name := "e2edel"
	pw := randomSecret(8)
	a.call("users.create", map[string]any{"name": name, "full": "Usuario a borrar", "email": "", "password": pw, "admin": false, "groups": []string{}}, nil)
	if out, err := sshRun("id -u " + name); err != nil {
		t.Fatalf("no se creó el usuario Unix: %v %s", err, out)
	}
	a.callErr("users.delete", map[string]any{"name": name, "typed_name": "otro"}, "CONFIRMATION_MISMATCH")
	re := a.callErr("users.delete", map[string]any{"name": "admin", "typed_name": "admin"}, "")
	if re.Code != "CANNOT_DELETE_SELF" && re.Code != "LAST_ADMIN" {
		t.Fatalf("borrar al propio admin → %s", re.Code)
	}
	a.call("users.delete", map[string]any{"name": name, "typed_name": name, "keepFiles": true}, nil)
	a.callErr("users.delete", map[string]any{"name": name, "typed_name": name}, "USER_NOT_FOUND")
	if _, err := sshRun("id -u " + name); err == nil {
		t.Fatal("el usuario Unix sigue existiendo")
	}
	if out, _ := sshRun("pdbedit -L 2>/dev/null | grep -c '^" + name + ":' || true"); strings.TrimSpace(out) != "0" {
		t.Fatalf("la cuenta Samba sigue existiendo: %s", out)
	}
	c := dial(t)
	c.callErr("auth.login", map[string]string{"user": name, "password": pw}, "INVALID_CREDENTIALS")
}

// ---- 2FA (TOTP) y contraseñas de app (§13, §14) ------------------------------------------

func totp(secret string, counter int64) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	h := m.Sum(nil)
	o := h[len(h)-1] & 0x0f
	v := (binary.BigEndian.Uint32(h[o:o+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", v)
}

func TestTOTPAndAppPasswords(t *testing.T) {
	a := admin(t)
	v1 := volume1(t, a)
	pw := ensureTestUser(t, a)
	s := ensureShare(t, a, "E2E-2FA", v1.ID)
	t.Cleanup(func() { deleteShare(t, a, s) })
	setUserAccess(t, a, s.ID, testUser, "rw")
	t.Cleanup(func() { a.call("users.totp.reset", map[string]string{"name": testUser}, nil) })

	u := loginAs(t, testUser, pw)
	var b struct {
		Secret string `json:"secret"`
		URI    string `json:"uri"`
		QR     string `json:"qr"`
	}
	u.call("users.totp.begin", nil, &b)
	if b.Secret == "" || !strings.HasPrefix(b.URI, "otpauth://totp/") || !strings.HasPrefix(b.QR, "data:image/svg+xml") {
		t.Fatalf("totp.begin incompleto: %+v", b)
	}
	now := time.Now().Unix() / 30
	u.callErr("users.totp.confirm", map[string]string{"code": "000000"}, "INVALID_CODE")
	var conf struct {
		Recovery []string `json:"recoveryCodes"`
	}
	u.call("users.totp.confirm", map[string]string{"code": totp(b.Secret, now)}, &conf)
	if len(conf.Recovery) != 10 {
		t.Fatalf("esperaba 10 códigos de recuperación, hay %d", len(conf.Recovery))
	}

	// Login en dos pasos.
	c := dial(t)
	var lr struct {
		Needs string `json:"needs"`
		Token string `json:"token"`
	}
	c.call("auth.login", map[string]string{"user": testUser, "password": pw}, &lr)
	if lr.Needs != "totp" || lr.Token != "" {
		t.Fatalf("con 2FA el login debería pedir código: %+v", lr)
	}
	c.callErr("files.list", map[string]string{"path": "/"}, "UNAUTHENTICATED")
	c.callErr("auth.totp", map[string]string{"code": "123456"}, "INVALID_CODE")
	c.call("auth.totp", map[string]string{"code": totp(b.Secret, now+1)}, &lr) // ventana ±1, sin reuso del código de confirm
	if lr.Token == "" {
		t.Fatal("auth.totp sin token")
	}
	c.call("files.list", map[string]string{"path": "/"}, nil)
	// Código de recuperación (un solo uso).
	c2 := dial(t)
	c2.call("auth.login", map[string]string{"user": testUser, "password": pw}, &lr)
	c2.call("auth.totp", map[string]string{"code": conf.Recovery[0]}, &lr)
	c3 := dial(t)
	c3.call("auth.login", map[string]string{"user": testUser, "password": pw}, &lr)
	c3.callErr("auth.totp", map[string]string{"code": conf.Recovery[0]}, "INVALID_CODE")
	c4 := dial(t)
	c4.callErr("auth.totp", map[string]string{"code": "123456"}, "TOTP_NOT_PENDING")

	// Con 2FA, WebDAV y SFTP solo aceptan contraseñas de app.
	var ap struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	u.call("users.appPasswords.create", map[string]any{"label": "e2e", "smb": false}, &ap)
	if len(strings.ReplaceAll(ap.Password, "-", "")) < 20 {
		t.Fatalf("contraseña de app corta: %d", len(ap.Password))
	}
	dav := func(user, pass string) int {
		req, _ := http.NewRequest("PUT", "https://"+host()+"/dav/"+url.PathEscape(s.Name)+"/2fa.txt", bytes.NewReader([]byte("2fa")))
		req.SetBasicAuth(user, pass)
		resp, err := insecureHTTP(true).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := dav(testUser, pw); code != 401 {
		t.Errorf("WebDAV con la contraseña de la cuenta y 2FA activo → %d, esperaba 401", code)
	}
	if code := dav(testUser, ap.Password); code != 201 && code != 204 {
		t.Errorf("WebDAV con contraseña de app → %d", code)
	}
	t.Run("SFTP-app-password", func(t *testing.T) { sftpRoundTrip(t, host(), testUser, ap.Password, s.Name) })
	var list []struct{ ID string }
	u.call("users.appPasswords.list", map[string]any{}, &list)
	if len(list) == 0 {
		t.Fatal("appPasswords.list vacío")
	}
	u.call("users.appPasswords.revoke", map[string]string{"id": ap.ID}, nil)
	if code := dav(testUser, ap.Password); code != 401 {
		t.Errorf("WebDAV con una contraseña de app revocada → %d", code)
	}
	// Otro usuario no puede ver las contraseñas de app de admin.
	u.callErr("users.appPasswords.list", map[string]string{"name": "admin"}, "PERMISSION_DENIED")
	// Desactivar con la contraseña de la cuenta.
	u.callErr("users.totp.disable", map[string]string{"password": "mala"}, "")
	u.call("users.totp.disable", map[string]string{"password": pw}, nil)
	c5 := dial(t)
	c5.call("auth.login", map[string]string{"user": testUser, "password": pw}, &lr)
	if lr.Token == "" {
		t.Fatal("después de desactivar 2FA el login sigue pidiendo código")
	}
}

// ---- Versiones anteriores de Windows, instantáneas navegables, rangos, watcher -----------

func TestPreviousVersionsBrowseRangesWatcher(t *testing.T) {
	a := admin(t)
	v1 := volume1(t, a)
	pw := ensureTestUser(t, a)
	s := ensureShare(t, a, "E2E-Versiones", v1.ID)
	t.Cleanup(func() { deleteShare(t, a, s) })
	setUserAccess(t, a, s.ID, testUser, "rw")
	root := "/" + s.Name
	tmp := t.TempDir()
	f := filepath.Join(tmp, "datos.bin")
	_ = os.WriteFile(f, bytes.Repeat([]byte("0123456789abcdef"), 1<<16), 0o600) // 1 MiB
	if _, err := a.Upload(ctxT(t, time.Minute), f, root, false); err != nil {
		t.Fatal(err)
	}
	var r jobRef
	a.call("snapshots.create", map[string]string{"share": s.ID}, &r)
	a.waitJob(r.Job, 2*time.Minute)
	var snaps []struct {
		ID string `json:"id"`
	}
	a.call("snapshots.list", map[string]string{"share": s.ID}, &snaps)
	if len(snaps) == 0 {
		t.Fatal("sin instantáneas")
	}
	snap := snaps[0].ID
	// Navegar la instantánea y stat.
	var es []fileEntry
	a.call("files.list", map[string]string{"path": root, "snapshot": snap}, &es)
	found := false
	for _, e := range es {
		found = found || e.Name == "datos.bin"
	}
	if !found {
		t.Fatalf("la instantánea %s no muestra datos.bin: %+v", snap, es)
	}
	var st fileEntry
	a.call("files.stat", map[string]string{"path": root + "/datos.bin", "snapshot": snap}, &st)
	if st.Size != 1<<20 {
		t.Fatalf("stat en la instantánea: %+v", st)
	}
	a.callErr("files.mkdir", map[string]any{"path": root + "/x", "snapshot": snap}, "") // solo lectura
	// Rango desde la instantánea y desde el archivo actual.
	rng := filepath.Join(tmp, "rango.bin")
	if _, err := a.DownloadRange(ctxT(t, time.Minute), root+"/datos.bin", snap, 1000, 4096, rng); err != nil {
		t.Fatalf("download con rango: %v", err)
	}
	got, _ := os.ReadFile(rng)
	want, _ := os.ReadFile(f)
	if len(got) < 1000+4096 || !bytes.Equal(got[1000:1000+4096], want[1000:1000+4096]) {
		t.Fatalf("los bytes del rango no coinciden (len %d)", len(got))
	}
	a.callErr("files.download.begin", map[string]any{"path": root + "/datos.bin", "offset": 2 << 20, "length": 10}, "")

	// Versiones anteriores de Windows por SMB: allinfo lista @GMT-…
	out, err := smbAs(t, testUser, pw, s.Name, "allinfo datos.bin")
	if err != nil || !strings.Contains(out, "@GMT-") {
		t.Fatalf("SMB no expone versiones anteriores (@GMT):\n%s %v", out, err)
	}

	// Watcher: con la carpeta abierta, un cambio por SMB emite files.changed.
	a.ls(root)
	for len(a.Events) > 0 {
		<-a.Events
	}
	if out, err := smbAs(t, testUser, pw, s.Name, "put /tmp/e2e-smb.txt desde-smb.txt"); err != nil {
		t.Fatalf("put por SMB: %v\n%s", err, out)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-a.Events:
			var e struct {
				Topic string `json:"topic"`
				Data  struct {
					Dir string `json:"dir"`
				} `json:"data"`
			}
			if json.Unmarshal(ev, &e) == nil && e.Topic == "files.changed" && e.Data.Dir == root {
				return
			}
		case <-deadline:
			t.Fatal("no llegó files.changed después de escribir por SMB")
		}
	}
}

// ---- Time Machine, WS-Discovery, UPS, cookie de apps ---------------------------------------

func TestTimeMachineShare(t *testing.T) {
	a := admin(t)
	var ss []struct {
		Name        string `json:"name"`
		TimeMachine bool   `json:"timeMachine"`
	}
	a.call("shares.list", nil, &ss)
	ok := false
	for _, x := range ss {
		ok = ok || (x.Name == "TimeMachine" && x.TimeMachine)
	}
	if !ok {
		t.Fatalf("no existe la carpeta TimeMachine con timeMachine=true: %+v", ss)
	}
	conf := onNAS(t, "testparm -s 2>/dev/null | awk '/^\\[TimeMachine\\]/{f=1;next} /^\\[/{f=0} f'")
	if !strings.Contains(conf, "fruit:time machine = Yes") && !strings.Contains(conf, "fruit:time machine = yes") {
		t.Fatalf("Samba no marca TimeMachine como destino de Time Machine:\n%s", conf)
	}
	avahi := onNAS(t, "cat /etc/avahi/services/tangoos-smb.service")
	if !strings.Contains(avahi, "_adisk._tcp") || !strings.Contains(avahi, "adVN=TimeMachine") {
		t.Fatalf("Bonjour no anuncia el disco de Time Machine:\n%s", avahi)
	}
	a.callErr("shares.update", map[string]any{"share": "timemachine-no-existe", "timeMachine": true}, "")
}

func TestWSDiscovery(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tangoctl")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/tangoctl").CombinedOutput(); err != nil {
		t.Fatalf("compilar tangoctl: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "wsd-probe", "4").CombinedOutput()
	if err != nil || !strings.Contains(string(out), host()) {
		// La Mac puede no recibir multicast de la VM: se prueba desde el propio NAS.
		lin := filepath.Join(t.TempDir(), "tangoctl-linux")
		build := exec.Command("go", "build", "-o", lin, "../../cmd/tangoctl")
		build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		if o, err := build.CombinedOutput(); err != nil {
			t.Fatalf("compilar tangoctl linux: %v\n%s", err, o)
		}
		scp := exec.Command("scp", "-q", "-i", sshKey(), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", lin, "root@"+host()+":/tmp/e2e-tangoctl")
		if o, err := scp.CombinedOutput(); err != nil {
			t.Fatalf("scp: %v %s", err, o)
		}
		nas := onNAS(t, "/tmp/e2e-tangoctl wsd-probe 4 2>&1; rm -f /tmp/e2e-tangoctl")
		t.Logf("probe desde la Mac sin respuesta (%v); desde el NAS:\n%s", err, nas)
		if !strings.Contains(strings.ToLower(nas), "computer") && !strings.Contains(nas, host()) {
			t.Fatalf("WS-Discovery no respondió:\n%s", nas)
		}
		return
	}
	t.Logf("WS-Discovery desde la Mac:\n%s", out)
}

func TestUPSFake(t *testing.T) {
	a := admin(t)
	bin := filepath.Join(t.TempDir(), "fakeupsd")
	build := exec.Command("go", "build", "-o", bin, "../../internal/ups/upstest/fakeupsd")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("no compila fakeupsd: %v %s", err, out)
	}
	scp := exec.Command("scp", "-q", "-i", sshKey(), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", bin, "root@"+host()+":/tmp/e2e-fakeupsd")
	if out, err := scp.CombinedOutput(); err != nil {
		t.Fatalf("scp: %v %s", err, out)
	}
	onNAS(t, "pkill -x e2e-fakeupsd; (setsid /tmp/e2e-fakeupsd -listen 127.0.0.1:13493 -name ups -status OL -charge 87 >/dev/null 2>&1 &); sleep 1")
	var prev json.RawMessage
	res, err := a.Call(ctxT(t, 20*time.Second), "ups.config.get", nil)
	if err != nil {
		t.Fatal(err)
	}
	prev = res
	t.Cleanup(func() {
		var p map[string]any
		_ = json.Unmarshal(prev, &p)
		if r, err := a.Call(ctxT(t, 30*time.Second), "ups.config.set", p); err == nil {
			var j jobRef
			_ = json.Unmarshal(r, &j)
			if j.Job != "" {
				a.waitJobAny(j.Job, 2*time.Minute)
			}
		}
		_, _ = sshRun("pkill -x e2e-fakeupsd; rm -f /tmp/e2e-fakeupsd")
	})
	var j jobRef
	a.call("ups.config.set", map[string]any{"mode": "remote", "host": "127.0.0.1", "port": 13493, "upsName": "ups", "netServer": false,
		"policy": map[string]any{"action": "notify", "afterMinutes": 0, "belowPercent": 0}}, &j)
	if j.Job != "" {
		a.waitJob(j.Job, 2*time.Minute)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		var st struct {
			Online     bool     `json:"online"`
			Status     string   `json:"status"`
			BatteryPct *float64 `json:"batteryPct"`
		}
		a.call("ups.status", nil, &st)
		if st.Online && strings.HasPrefix(st.Status, "OL") && st.BatteryPct != nil && *st.BatteryPct == 87 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ups.status no refleja el UPS falso: %+v", st)
		}
		time.Sleep(2 * time.Second)
	}
}

func TestAppsCookieRevokedOnLogout(t *testing.T) {
	a := dial(t)
	a.t = t
	a.call("auth.login", map[string]string{"user": "admin", "password": adminPassword(t)}, nil)
	var apps []struct {
		ID  string  `json:"id"`
		URL *string `json:"url"`
	}
	a.call("apps.list", nil, &apps)
	var id, base string
	for _, x := range apps {
		if x.URL != nil {
			id, base = x.ID, *x.URL
			break
		}
	}
	if id == "" {
		t.Skip("no hay apps instaladas con web")
	}
	var tk struct {
		URL string `json:"url"`
	}
	a.call("apps.ticket", map[string]string{"id": id}, &tk)
	cl := insecureHTTP(false)
	resp, err := cl.Get(tk.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cookies := resp.Cookies()
	get := func() int {
		req, _ := http.NewRequest("GET", base, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		r, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
		return r.StatusCode
	}
	if code := get(); code == 401 {
		t.Fatal("la cookie no sirve recién emitida")
	}
	a.call("auth.logout", nil, nil)
	time.Sleep(time.Second)
	if code := get(); code != 401 {
		t.Fatalf("después de cerrar sesión la cookie de la app sigue sirviendo (%d)", code)
	}
}
