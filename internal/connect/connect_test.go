package connect

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

func TestLinkProofVector(t *testing.T) {
	b, err := os.ReadFile("../../protocol/vectors/connect-link-proof.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Vectors []struct{ Token, TokenSha256Hex, Nonce, Proof string }
	}
	if err := json.Unmarshal(b, &f); err != nil || len(f.Vectors) == 0 {
		t.Fatalf("vector: %v", err)
	}
	for _, v := range f.Vectors {
		if hashHex(v.Token) != v.TokenSha256Hex {
			t.Fatal("sha256 del token no coincide con el vector")
		}
		p, ok := LinkProof(v.TokenSha256Hex, v.Nonce)
		if !ok || p != v.Proof {
			t.Fatalf("proof = %q, quiero %q", p, v.Proof)
		}
	}
	if _, ok := LinkProof("zz", "n"); ok {
		t.Fatal("hash inválido aceptado")
	}
}

func TestStoreIdentityAndPersistence(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := st.Get()
	if len(c.DeviceID) != 36 || len(c.DeviceSecret) != 43 || c.SignalURL != DefaultSignalURL || c.PortalURL != DefaultPortalURL || c.Enabled {
		t.Fatalf("config inicial: %+v", c)
	}
	fi, err := os.Stat(filepath.Join(dir, "connect.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("connect.json debe ser 0600: %v %v", fi.Mode(), err)
	}
	st2, _ := OpenStore(dir)
	if st2.Get().DeviceID != c.DeviceID || st2.Get().DeviceSecret != c.DeviceSecret {
		t.Fatal("la identidad cambió al reabrir")
	}
}

func TestManifestSignVerify(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":           {Data: []byte("<html>")},
		"assets/a.js":          {Data: []byte("console.log(1)")},
		".tango-manifest.json": {Data: []byte("ignored")},
	}
	m, err := BuildManifest(fsys, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 2 || m.Files[0].Path != "assets/a.js" {
		t.Fatalf("manifiesto: %+v", m.Files)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	text, sig, err := Sign(m, priv)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(text, sig, pub) {
		t.Fatal("firma válida rechazada")
	}
	if Verify(text+" ", sig, pub) {
		t.Fatal("texto alterado aceptado")
	}
	// Bundle con manifiesto firmado + clave pública: se verifica al cargar.
	signed := fstest.MapFS{
		"index.html":  {Data: []byte("<html>")},
		"assets/a.js": {Data: []byte("console.log(1)")},
		ManifestFile:  {Data: []byte(text)},
		SignatureFile: {Data: []byte(sig + "\n")},
		PublicKeyFile: {Data: []byte(b64(pub) + "\n")},
	}
	b, err := NewBundle(signed, "x")
	if err != nil {
		t.Fatal(err)
	}
	if b.Manifest().KeyID != KeyID(pub) || b.Manifest().Signature == "" {
		t.Fatalf("manifiesto servido: %+v", b.Manifest())
	}
	signed[SignatureFile] = &fstest.MapFile{Data: []byte(sig[:len(sig)-4] + "AAAA")}
	if _, err := NewBundle(signed, "x"); err == nil {
		t.Fatal("firma corrupta aceptada al cargar el bundle")
	}
	// Sin manifiesto: se calcula, sin firma.
	u, err := NewBundle(fsys, "dev")
	if err != nil || u.Manifest().Signature != "" || len(u.Manifest().Files) != 2 {
		t.Fatalf("bundle sin firmar: %+v %v", u.Manifest(), err)
	}
}

func TestShareExpiry(t *testing.T) {
	now := time.Now()
	if (ShareLink{}).Expired(now) {
		t.Fatal("sin límites no vence")
	}
	if !(ShareLink{ExpiresAt: now.Add(-time.Minute).Format(time.RFC3339)}).Expired(now) {
		t.Fatal("vencido por fecha")
	}
	if !(ShareLink{MaxDownloads: 2, Downloads: 2}).Expired(now) {
		t.Fatal("vencido por descargas")
	}
	if (ShareLink{MaxDownloads: 2, Downloads: 1}).Expired(now) {
		t.Fatal("todavía le queda una descarga")
	}
}

func newTestService(t *testing.T) *Service {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(Deps{Store: st})
}

func sess(state string) *rtcrpc.Session {
	s := rtcrpc.NewSession("x", "remote", "remote", nil, nil, nil)
	if state != "" {
		s.Set("connect.state", state)
	}
	return s
}

func TestGateByState(t *testing.T) {
	sv := newTestService(t)
	cases := []struct {
		state, method string
		allow         bool
	}{
		{"", "storage.disks.list", true}, // local: reglas normales
		{"", "connect.resume", false},
		{"", "guest.open", false},
		{"", "bundle.get", false},
		{StatePaired, "connect.link", true},
		{StatePaired, "auth.login", false},
		{StateUnlinked, "connect.challenge", true},
		{StateUnlinked, "connect.resume", true},
		{StateUnlinked, "guest.open", true},
		{StateUnlinked, "auth.login", false},
		{StateUnlinked, "bundle.manifest", false},
		{StateLinked, "auth.login", true},
		{StateLinked, "bundle.get", true},
		{StateLinked, "system.info", true},
		{StateLinked, "storage.disks.list", false}, // sin login
		{StateGuest, "guest.download.begin", true},
		{StateGuest, "guest.open", false},
		{StateGuest, "files.list", false},
		{StateGuest, "auth.login", false},
		{StateGuest, "connect.hello", true},
	}
	for _, c := range cases {
		err := sv.Gate(sess(c.state), c.method)
		if (err == nil) != c.allow {
			t.Errorf("estado %q método %s: permitido=%v, quería %v", c.state, c.method, err == nil, c.allow)
		}
	}
	// Linked + logueado: todo salvo los métodos de vínculo e invitados.
	s := sess(StateLinked)
	s.Authenticate("admin", "tok")
	if sv.Gate(s, "storage.disks.list") != nil || sv.Gate(s, "guest.open") == nil || sv.Gate(s, "connect.resume") == nil {
		t.Fatal("linked + login: reglas incorrectas")
	}
}

func TestLinkChallengeResume(t *testing.T) {
	sv := newTestService(t)
	if _, err := sv.Link(sess(StateUnlinked), "x"); err == nil {
		t.Fatal("solo se vincula quien llegó con el código")
	}
	s := sess(StatePaired)
	lr, err := sv.Link(s, "Mi notebook")
	if err != nil || lr.Token == "" || s.Get("connect.state") != StateLinked {
		t.Fatalf("link: %+v %v", lr, err)
	}
	// Otra conexión (reach): el NAS prueba primero, después el token.
	r := sess(StateUnlinked)
	nonce := "0123456789abcdef0123"
	proof, err := sv.Challenge(r, lr.LinkID, nonce)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := LinkProof(hashHex(lr.Token), nonce)
	if proof != want {
		t.Fatal("la prueba no corresponde al token")
	}
	if err := sv.Resume(r, lr.LinkID, "otro-token"); err == nil {
		t.Fatal("token falso aceptado")
	}
	if err := sv.Resume(r, lr.LinkID, lr.Token); err != nil || r.Get("connect.state") != StateLinked {
		t.Fatalf("resume: %v", err)
	}
	// 5 fallos cortan a esa conexión.
	bad := sess(StateUnlinked)
	for i := 0; i < 5; i++ {
		_ = sv.Resume(bad, lr.LinkID, "x")
	}
	if err := sv.Resume(bad, lr.LinkID, lr.Token); err == nil {
		t.Fatal("después de 5 fallos debería quedar bloqueada")
	}
	if err := sv.RevokeLink(lr.LinkID); err != nil {
		t.Fatal(err)
	}
	if _, err := sv.Challenge(sess(StateUnlinked), lr.LinkID, nonce); err == nil {
		t.Fatal("vínculo revocado sigue respondiendo")
	}
}

func TestShareLinkGuestFlow(t *testing.T) {
	sv := newTestService(t)
	info, err := sv.CreateShare(ShareParams{Owner: "sofia", Path: "/Familia/foto.jpg", Name: "foto.jpg", Kind: "image", Size: 10, Password: "secreta123", MaxDownloads: 1})
	if err != nil {
		t.Fatal(err)
	}
	secret := info.URL[len(info.URL)-24:]
	if info.Invite != sv.d.Store.Get().DeviceID+"."+info.ID || !info.HasPassword {
		t.Fatalf("info: %+v", info)
	}
	g := sess(StateUnlinked)
	if _, err := sv.GuestOpen(g, info.ID, "mal"); err == nil {
		t.Fatal("secreto incorrecto aceptado")
	}
	if _, err := sv.GuestOpen(g, info.ID, secret); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := sv.GuestLink(g); err == nil {
		t.Fatal("con contraseña pendiente no debería poder bajar")
	}
	if err := sv.GuestUnlock(g, "nop"); err == nil {
		t.Fatal("contraseña incorrecta aceptada")
	}
	if err := sv.GuestUnlock(g, "secreta123"); err != nil {
		t.Fatal(err)
	}
	if _, err := sv.GuestLink(g); err != nil {
		t.Fatal(err)
	}
	if err := sv.CountDownload(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sv.GuestLink(g); err == nil {
		t.Fatal("con el límite de descargas alcanzado el enlace debería vencer")
	}
	// Otro usuario no puede revocar un enlace ajeno; un admin sí.
	if err := sv.RevokeShare(info.ID, "martin", false); err == nil {
		t.Fatal("revocó un enlace ajeno")
	}
	if err := sv.RevokeShare(info.ID, "admin", true); err != nil {
		t.Fatal(err)
	}
	if _, err := sv.CreateShare(ShareParams{Owner: "x", Path: "/a", ExpiresAt: "2001-01-01T00:00:00Z"}); err == nil {
		t.Fatal("vencimiento en el pasado aceptado")
	}
}
