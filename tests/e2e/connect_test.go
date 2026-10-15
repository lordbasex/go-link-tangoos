//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/connect"
	"github.com/lordbasex/go-link-tangoos/internal/rtc"
)

// TangoConnect (protocol/README.md §15) por un signalhub de desarrollo:
//
//	TANGO_E2E_SIGNAL  URL del signalhub (por defecto ws://172.17.0.5:8090/ws?v=1, el de la Mac)
//
// Nunca usa el de producción (signal.go-link.org rechaza app=tangoos hasta que se
// habilite en ALLOWED_APPS). Al terminar deja TangoConnect apagado y con la URL por defecto.

func devSignal() string {
	if v := os.Getenv("TANGO_E2E_SIGNAL"); v != "" {
		return v
	}
	return "ws://172.17.0.5:8090/ws?v=1"
}

type remoteClient struct {
	*rtc.Client
	t *testing.T
}

func (r *remoteClient) call(method string, params any, out any) {
	r.t.Helper()
	res, err := r.Call(ctxT(r.t, 60*time.Second), method, params)
	if err != nil {
		r.t.Fatalf("remoto %s: %v", method, err)
	}
	if out != nil {
		if err := json.Unmarshal(res, out); err != nil {
			r.t.Fatalf("remoto %s: %v", method, err)
		}
	}
}

func (r *remoteClient) callErr(method string, params any, code string) {
	r.t.Helper()
	res, err := r.Call(ctxT(r.t, 30*time.Second), method, params)
	if err == nil {
		r.t.Fatalf("remoto %s: esperaba %s y respondió %s", method, code, truncate(string(res), 200))
	}
	var re *rtc.RPCError
	if !errors.As(err, &re) {
		r.t.Fatalf("remoto %s: error no RPC: %v", method, err)
	}
	if code != "" && re.Code != code {
		r.t.Fatalf("remoto %s: %s (%s), esperaba %s", method, re.Code, re.Message, code)
	}
}

func dialRemote(t *testing.T, m rtc.HubMode) *remoteClient {
	t.Helper()
	m.App = connect.App
	var c *rtc.Client
	var err error
	for i := 0; i < 4; i++ {
		c, err = rtc.DialSignalhub(ctxT(t, 40*time.Second), devSignal(), m)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "device not connected") {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		t.Fatalf("signalhub: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &remoteClient{Client: c, t: t}
}

func TestConnect(t *testing.T) {
	a := admin(t)
	var st connect.Status
	a.call("connect.enable", map[string]any{"enabled": true, "signalURL": devSignal()}, &st)
	t.Cleanup(func() {
		// Apagado y de vuelta a la URL por defecto (producción, que todavía rechaza tangoos).
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = a.Call(ctx, "connect.enable", map[string]any{"enabled": false, "signalURL": connect.DefaultSignalURL})
	})
	deadline := time.Now().Add(20 * time.Second)
	for !st.Connected && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		a.call("connect.status", nil, &st)
	}
	if !st.Connected {
		t.Fatalf("el NAS no se conectó a %s: %+v", devSignal(), st)
	}
	t.Logf("NAS conectado a signalhub: device %s, fingerprint %s", st.DeviceID, st.Fingerprint)

	var token, linkID string
	t.Run("vincular con código y operar remoto", func(t *testing.T) {
		var pr struct{ Code, ExpiresAt string }
		a.call("connect.pair.start", nil, &pr)
		code := strings.ReplaceAll(pr.Code, " ", "")
		if len(code) != 9 {
			t.Fatalf("código %q", pr.Code)
		}
		r := dialRemote(t, rtc.HubMode{Code: code})
		if fp := r.RemoteFingerprint(); !strings.EqualFold(fp, st.Fingerprint) {
			t.Fatalf("fingerprint DTLS %q ≠ %q (pinning)", fp, st.Fingerprint)
		}
		var h struct {
			Remote bool
			State  string
		}
		r.call("connect.hello", nil, &h)
		if !h.Remote || h.State != connect.StatePaired {
			t.Fatalf("hello remoto: %+v", h)
		}
		r.callErr("auth.login", map[string]string{"user": "admin", "password": "x"}, connect.CodeForbiddenState)
		var lr connect.LinkResult
		r.call("connect.link", map[string]string{"label": "e2e portal"}, &lr)
		token, linkID = lr.Token, lr.LinkID
		if lr.DeviceID != st.DeviceID {
			t.Fatalf("deviceId %q ≠ %q", lr.DeviceID, st.DeviceID)
		}
		r.callErr("storage.disks.list", nil, "UNAUTHENTICATED")
		r.call("auth.login", map[string]string{"user": "admin", "password": adminPassword(t)}, nil)
		var disks []map[string]any
		r.call("storage.disks.list", nil, &disks)
		if len(disks) == 0 {
			t.Fatal("sin discos por la ruta remota")
		}
		t.Logf("remoto (claim): %d discos por WebRTC vía signalhub", len(disks))
		// El escritorio, servido por el NAS y verificado.
		var man connect.ManifestResult
		r.call("bundle.manifest", nil, &man)
		if len(man.Files) == 0 {
			t.Fatal("manifiesto vacío")
		}
		if man.Signature == "" {
			t.Log("aviso: el bundle no está firmado (build sin make web)")
		}
		idx, err := r.BundleFile(ctxT(t, 60*time.Second), "index.html")
		if err != nil || !strings.Contains(string(idx), "<") {
			t.Fatalf("bundle index.html: %v", err)
		}
		var big connect.ManifestEntry
		for _, f := range man.Files {
			if f.Size > big.Size {
				big = f
			}
		}
		start := time.Now()
		if _, err := r.BundleFile(ctxT(t, 120*time.Second), big.Path); err != nil {
			t.Fatalf("bundle %s: %v", big.Path, err)
		}
		t.Logf("bundle: %d archivos, firma=%v, %s (%d B) en %v", len(man.Files), man.Signature != "", big.Path, big.Size, time.Since(start).Round(time.Millisecond))
	})

	t.Run("volver con reach: el NAS prueba primero", func(t *testing.T) {
		if token == "" {
			t.Skip("sin vínculo")
		}
		r := dialRemote(t, rtc.HubMode{DeviceID: st.DeviceID})
		r.callErr("auth.login", map[string]string{"user": "admin", "password": "x"}, connect.CodeForbiddenState)
		r.callErr("bundle.manifest", nil, connect.CodeForbiddenState)
		nonce := randomSecret(12)
		var pr struct{ Proof string }
		r.call("connect.challenge", map[string]string{"linkId": linkID, "nonce": nonce}, &pr)
		h := sha256.Sum256([]byte(token))
		want, _ := connect.LinkProof(hex.EncodeToString(h[:]), nonce)
		if pr.Proof != want {
			t.Fatal("la prueba del NAS no corresponde al token guardado")
		}
		r.callErr("connect.resume", map[string]string{"linkId": linkID, "token": "falso"}, connect.CodeLinkInvalid)
		r.call("connect.resume", map[string]string{"linkId": linkID, "token": token}, nil)
		r.call("auth.login", map[string]string{"user": "admin", "password": adminPassword(t)}, nil)
		var info map[string]any
		r.call("system.info", nil, &info)
		var vols []map[string]any
		r.call("storage.volumes.list", nil, &vols)
		t.Logf("remoto (reach): %v, %d volúmenes", info["hostname"], len(vols))
		// Revocar el vínculo corta esa conexión.
		a.call("connect.links.revoke", map[string]string{"id": linkID}, nil)
		time.Sleep(time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := r.Call(ctx, "system.info", nil); err == nil {
			t.Fatal("la conexión de un vínculo revocado sigue respondiendo")
		}
	})

	t.Run("enlace público con contraseña y límite", func(t *testing.T) {
		var entries []struct {
			Name, Path, Kind string
			Size             int64
		}
		a.call("files.list", map[string]string{"path": "/Familia"}, &entries)
		var target string
		var size int64
		for _, e := range entries {
			if e.Kind != "dir" && e.Size > 0 && (target == "" || e.Size > size) {
				target, size = e.Path, e.Size
			}
		}
		if target == "" {
			t.Skip("Familia no tiene archivos")
		}
		dir := t.TempDir()
		ref := filepath.Join(dir, "ref")
		refSt, err := a.Download(ctxT(t, 120*time.Second), target, ref)
		if err != nil {
			t.Fatalf("descarga local de referencia: %v", err)
		}
		var link connect.ShareInfo
		a.call("shares.links.create", map[string]any{"path": target, "password": "clave-del-enlace", "maxDownloads": 1}, &link)
		t.Cleanup(func() { _, _ = a.Call(context.Background(), "shares.links.revoke", map[string]string{"id": link.ID}) })
		if !strings.HasPrefix(link.URL, "https://tangoos.go-link.org/s/"+st.DeviceID+".") {
			t.Fatalf("URL del enlace: %s", link.URL)
		}
		secret := link.URL[strings.Index(link.URL, "#")+1:]

		g := dialRemote(t, rtc.HubMode{DeviceID: st.DeviceID})
		g.callErr("guest.open", map[string]string{"link": link.ID, "secret": "otro"}, connect.CodeShareNotFound)
		var gi struct {
			Name          string
			NeedsPassword bool
			Unlocked      bool
		}
		g.call("guest.open", map[string]string{"link": link.ID, "secret": secret}, &gi)
		if !gi.NeedsPassword || gi.Unlocked {
			t.Fatalf("guest.open: %+v", gi)
		}
		g.callErr("storage.disks.list", nil, connect.CodeForbiddenState)
		g.callErr("files.list", map[string]string{"path": "/"}, connect.CodeForbiddenState)
		g.callErr("guest.download.begin", map[string]string{"path": ""}, connect.CodeShareLocked)
		g.callErr("guest.unlock", map[string]string{"password": "mal"}, "INVALID_CREDENTIALS")
		g.call("guest.unlock", map[string]string{"password": "clave-del-enlace"}, nil)
		g.callErr("guest.download.begin", map[string]string{"path": "../Fotos"}, "")
		gst, err := g.GuestDownload(ctxT(t, 180*time.Second), "", filepath.Join(dir, "guest"))
		if err != nil {
			t.Fatalf("descarga como invitado: %v", err)
		}
		if gst.SHA256 != refSt.SHA256 || gst.Bytes != size {
			t.Fatalf("sha256 invitado %s (%d B) ≠ %s (%d B)", gst.SHA256, gst.Bytes, refSt.SHA256, size)
		}
		mbps := float64(gst.Bytes) / gst.Elapsed.Seconds() / 1e6
		t.Logf("invitado bajó %s: %d B en %v (%.1f MB/s), sha256 ok", target, gst.Bytes, gst.Elapsed.Round(time.Millisecond), mbps)
		g.callErr("guest.download.begin", map[string]string{"path": ""}, connect.CodeShareExpired)
	})

	t.Run("rendimiento por la ruta remota", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "connect-64m.bin")
		buf := make([]byte, 64<<20)
		_, _ = rand.Read(buf)
		if err := os.WriteFile(src, buf, 0o600); err != nil {
			t.Fatal(err)
		}
		up, err := a.Upload(ctxT(t, 180*time.Second), src, "/Familia", true)
		if err != nil {
			t.Fatalf("subida LAN: %v", err)
		}
		remotePath := "/Familia/connect-64m.bin"
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = a.Call(ctx, "files.trash", map[string]any{"paths": []string{remotePath}})
			_, _ = a.Call(ctx, "files.trash.empty", map[string]string{"share": "Familia"})
		})
		var link connect.ShareInfo
		a.call("shares.links.create", map[string]any{"path": remotePath}, &link)
		t.Cleanup(func() { _, _ = a.Call(context.Background(), "shares.links.revoke", map[string]string{"id": link.ID}) })
		g := dialRemote(t, rtc.HubMode{DeviceID: st.DeviceID})
		g.call("guest.open", map[string]string{"link": link.ID, "secret": link.URL[strings.Index(link.URL, "#")+1:]}, nil)
		gst, err := g.GuestDownload(ctxT(t, 300*time.Second), "", filepath.Join(dir, "down"))
		if err != nil {
			t.Fatalf("descarga remota: %v", err)
		}
		if gst.SHA256 != up.SHA256 {
			t.Fatal("sha256 distinto")
		}
		t.Logf("64 MiB por enlace público (signalhub → WebRTC directo): %.1f MB/s (subida LAN %.1f MB/s), sha256 ok",
			float64(gst.Bytes)/gst.Elapsed.Seconds()/1e6, float64(up.Bytes)/up.Elapsed.Seconds()/1e6)
	})

	t.Run("enlace vencido y revocado", func(t *testing.T) {
		var entries []struct{ Path, Kind string }
		a.call("files.list", map[string]string{"path": "/Familia"}, &entries)
		var target string
		for _, e := range entries {
			if e.Kind != "dir" {
				target = e.Path
				break
			}
		}
		if target == "" {
			t.Skip("Familia no tiene archivos")
		}
		var exp connect.ShareInfo
		a.call("shares.links.create", map[string]any{"path": target, "expiresAt": time.Now().Add(3 * time.Second).UTC().Format(time.RFC3339)}, &exp)
		time.Sleep(4 * time.Second)
		g := dialRemote(t, rtc.HubMode{DeviceID: st.DeviceID})
		g.callErr("guest.open", map[string]string{"link": exp.ID, "secret": exp.URL[strings.Index(exp.URL, "#")+1:]}, connect.CodeShareExpired)
		a.call("shares.links.revoke", map[string]string{"id": exp.ID}, nil)

		var rv connect.ShareInfo
		a.call("shares.links.create", map[string]any{"path": "/Familia"}, &rv)
		g2 := dialRemote(t, rtc.HubMode{DeviceID: st.DeviceID})
		g2.call("guest.open", map[string]string{"link": rv.ID, "secret": rv.URL[strings.Index(rv.URL, "#")+1:]}, nil)
		var list []map[string]any
		g2.call("guest.list", map[string]string{"path": ""}, &list)
		for _, e := range list {
			if p, _ := e["path"].(string); strings.HasPrefix(p, "/") {
				t.Fatalf("el invitado ve rutas absolutas: %v", p)
			}
		}
		a.call("shares.links.revoke", map[string]string{"id": rv.ID}, nil)
		time.Sleep(time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := g2.Call(ctx, "guest.info", nil); err == nil {
			t.Fatal("el invitado de un enlace revocado sigue conectado")
		}
		t.Logf("carpeta compartida por enlace: %d elementos; vencido y revocado ok", len(list))
	})
}
