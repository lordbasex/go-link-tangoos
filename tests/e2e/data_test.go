//go:build e2e

package e2e

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// smbAs corre smbclient en el NAS con un archivo de credenciales (no por argv).
func smbAs(t *testing.T, user, pw, share, cmds string) (string, error) {
	t.Helper()
	creds := fmt.Sprintf("username=%s\npassword=%s\n", user, pw)
	script := "umask 077; f=$(mktemp); cat > $f <<'EOF'\n" + creds + "EOF\n" +
		"echo hola-e2e > /tmp/e2e-smb.txt; smbclient //localhost/" + share + " -A $f -c '" + cmds + "' 2>&1; rc=$?; rm -f $f /tmp/e2e-smb.txt; exit $rc"
	return sshRun(script)
}

// TestSharesUsersPermissions: carpeta nueva visible por SMB, permisos rw/ro/none
// verificados con smbclient, usuario y cambio de contraseña.
func TestSharesUsersPermissions(t *testing.T) {
	a := admin(t)
	v1 := volume1(t, a)
	pw := ensureTestUser(t, a)
	name := "E2E-Permisos"
	s := ensureShare(t, a, name, v1.ID)
	t.Cleanup(func() { deleteShare(t, a, s) })

	out := onNAS(t, "smbclient -L localhost -N 2>&1 || true")
	if !strings.Contains(out, name) {
		t.Fatalf("la carpeta no aparece en smbclient -L:\n%s", out)
	}
	// rw → put funciona
	setUserAccess(t, a, s.ID, testUser, "rw")
	if out, err := smbAs(t, testUser, pw, name, "put /tmp/e2e-smb.txt e2e.txt; ls"); err != nil || !strings.Contains(out, "e2e.txt") {
		t.Fatalf("rw: put falló: %v\n%s", err, out)
	}
	// ro → ls sí, put no
	setUserAccess(t, a, s.ID, testUser, "ro")
	out, _ = smbAs(t, testUser, pw, name, "ls; put /tmp/e2e-smb.txt e2e-ro.txt")
	if !strings.Contains(out, "e2e.txt") || !strings.Contains(out, "NT_STATUS_ACCESS_DENIED") {
		t.Fatalf("ro: esperaba listar y ACCESS_DENIED al escribir:\n%s", out)
	}
	// none → ni conecta
	setUserAccess(t, a, s.ID, testUser, "none")
	out, _ = smbAs(t, testUser, pw, name, "ls")
	if !strings.Contains(out, "NT_STATUS_ACCESS_DENIED") && !strings.Contains(out, "NT_STATUS_BAD_NETWORK_NAME") {
		t.Fatalf("none: esperaba acceso denegado:\n%s", out)
	}
	// Contraseña débil rechazada; cambio de contraseña aplicado a SMB.
	a.callErr("users.setPassword", map[string]string{"name": testUser, "password": "corta"}, "")
	setUserAccess(t, a, s.ID, testUser, "rw")
	pw2 := randomSecret(8)
	a.call("users.setPassword", map[string]string{"name": testUser, "password": pw2}, nil)
	if out, err := smbAs(t, testUser, pw2, name, "ls"); err != nil {
		t.Fatalf("la contraseña nueva no sirve por SMB: %v\n%s", err, out)
	}
	if out, _ := smbAs(t, testUser, pw, name, "ls"); !strings.Contains(out, "NT_STATUS_LOGON_FAILURE") {
		t.Fatalf("la contraseña vieja sigue sirviendo por SMB:\n%s", out)
	}
	// Nombre de carpeta duplicado / inválido.
	a.callErr("shares.create", map[string]any{"name": name, "volume": v1.ID, "description": "", "smb": false}, "")
	a.callErr("shares.create", map[string]any{"name": "../etc", "volume": v1.ID, "description": "", "smb": false}, "")
	// Borrar con el nombre mal escrito no borra.
	a.callErr("shares.delete", map[string]string{"share": s.ID, "typed_name": "otra-cosa"}, "")
}

// ---- Archivos ---------------------------------------------------------------------------

type fileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
}

func (c *client) ls(p string) map[string]fileEntry {
	c.t.Helper()
	var es []fileEntry
	c.call("files.list", map[string]string{"path": p}, &es)
	m := map[string]fileEntry{}
	for _, e := range es {
		m[e.Name] = e
	}
	return m
}

func writeRandom(t *testing.T, path string, mb int) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(f, h), rand.Reader, int64(mb)<<20); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	_, _ = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

func TestFiles(t *testing.T) {
	a := admin(t)
	v1 := volume1(t, a)
	pw := ensureTestUser(t, a)
	name := "E2E-Archivos"
	s := ensureShare(t, a, name, v1.ID)
	t.Cleanup(func() { deleteShare(t, a, s) })
	root := "/" + name
	tmp := t.TempDir()

	// Subir y bajar un archivo grande con verificación sha256.
	mb := 1024
	if v, err := strconv.Atoi(os.Getenv("TANGO_E2E_BIGFILE_MB")); err == nil && v > 0 {
		mb = v
	}
	big := filepath.Join(tmp, "grande.bin")
	want := writeRandom(t, big, mb)
	st, err := a.Upload(ctxT(t, 20*time.Minute), big, root, false)
	if err != nil {
		t.Fatalf("upload %d MiB: %v", mb, err)
	}
	t.Logf("subida %d MiB: %.1f MB/s", mb, st.MBps())
	if got := a.ls(root)["grande.bin"]; got.Size != int64(mb)<<20 {
		t.Fatalf("tamaño remoto %d", got.Size)
	}
	back := filepath.Join(tmp, "vuelta.bin")
	st, err = a.Download(ctxT(t, 20*time.Minute), root+"/grande.bin", back)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	t.Logf("bajada %d MiB: %.1f MB/s", mb, st.MBps())
	if got := sha256File(t, back); got != want {
		t.Fatalf("sha256 distinto tras ida y vuelta")
	}
	// Mismo nombre sin overwrite → error.
	if _, err := a.Upload(ctxT(t, time.Minute), big, root, false); err == nil {
		t.Fatal("subió encima de un archivo existente sin overwrite")
	}

	// mkdir, rename, move, trash, restore, empty.
	a.call("files.mkdir", map[string]string{"path": root + "/Carpeta"}, nil)
	small := filepath.Join(tmp, "nota.txt")
	_ = os.WriteFile(small, []byte("hola TangoOS\n"), 0o600)
	if _, err := a.Upload(ctxT(t, time.Minute), small, root, false); err != nil {
		t.Fatal(err)
	}
	a.call("files.rename", map[string]string{"path": root + "/nota.txt", "name": "nota-renombrada.txt"}, nil)
	a.call("files.move", map[string]any{"paths": []string{root + "/nota-renombrada.txt"}, "dest": root + "/Carpeta"}, nil)
	if _, ok := a.ls(root + "/Carpeta")["nota-renombrada.txt"]; !ok {
		t.Fatal("move no dejó el archivo en Carpeta")
	}
	a.callErr("files.rename", map[string]string{"path": root + "/Carpeta", "name": "a/b"}, "")
	a.call("files.trash", map[string]any{"paths": []string{root + "/grande.bin"}}, nil)
	if _, ok := a.ls(root)["grande.bin"]; ok {
		t.Fatal("trash no quitó el archivo")
	}
	var trash []struct {
		Path   string `json:"path"`
		Origin string `json:"origin"`
	}
	a.call("files.trash.list", map[string]string{"share": name}, &trash)
	var tpath string
	for _, e := range trash {
		if strings.HasSuffix(e.Origin, "/grande.bin") {
			tpath = e.Path
		}
	}
	if tpath == "" {
		t.Fatalf("grande.bin no está en la papelera: %+v", trash)
	}
	a.call("files.restore", map[string]any{"paths": []string{tpath}}, nil)
	if _, ok := a.ls(root)["grande.bin"]; !ok {
		t.Fatal("restore no devolvió el archivo")
	}
	a.call("files.trash", map[string]any{"paths": []string{root + "/grande.bin"}}, nil)
	a.call("files.trash.empty", map[string]string{"share": name}, nil)

	// Búsqueda.
	var hits []fileEntry
	a.call("files.search", map[string]string{"q": "renombrada"}, &hits)
	if len(hits) == 0 {
		t.Fatal("files.search no encontró el archivo")
	}

	// Miniatura de una imagen generada acá.
	img := image.NewRGBA(image.Rect(0, 0, 320, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 160, 255})
		}
	}
	pf, _ := os.Create(filepath.Join(tmp, "img.png"))
	_ = png.Encode(pf, img)
	pf.Close()
	if _, err := a.Upload(ctxT(t, time.Minute), filepath.Join(tmp, "img.png"), root, false); err != nil {
		t.Fatal(err)
	}
	var th struct {
		Mime string `json:"mime"`
		Data string `json:"data"`
	}
	a.call("files.thumb", map[string]any{"path": root + "/img.png", "size": 128}, &th)
	if th.Mime != "image/jpeg" || len(th.Data) < 100 {
		t.Fatalf("miniatura inválida: %s (%d bytes)", th.Mime, len(th.Data))
	}

	// Seguridad de rutas: traversal y symlinks que escapan.
	for _, p := range []string{root + "/../../etc", root + "/../" + name + "/../../etc/passwd", "/../etc", root + "/.tango-trash"} {
		a.callErr("files.list", map[string]string{"path": p}, "")
	}
	onNAS(t, fmt.Sprintf("ln -sfn /etc %s/escape-abs && ln -sfn ../../../../etc %s/escape-rel", s.Path, s.Path))
	for _, p := range []string{root + "/escape-abs", root + "/escape-rel"} {
		if _, err := a.Call(ctxT(t, 20*time.Second), "files.list", map[string]string{"path": p}); err == nil {
			es := a.ls(p)
			if _, leak := es["passwd"]; leak {
				t.Fatalf("symlink %s permite listar /etc", p)
			}
		}
		if _, err := a.Download(ctxT(t, 20*time.Second), p+"/passwd", filepath.Join(tmp, "leak")); err == nil {
			t.Fatalf("symlink %s permitió bajar /etc/passwd", p)
		}
	}
	onNAS(t, fmt.Sprintf("rm -f %s/escape-abs %s/escape-rel", s.Path, s.Path))

	// Permisos por usuario: sin acceso → la carpeta no se ve; ro → no puede subir.
	u := loginAs(t, testUser, pw)
	setUserAccess(t, a, s.ID, testUser, "none")
	time.Sleep(time.Second)
	if _, ok := u.ls("/")[name]; ok {
		t.Fatal("un usuario sin acceso ve la carpeta en /")
	}
	setUserAccess(t, a, s.ID, testUser, "ro")
	time.Sleep(time.Second)
	if _, ok := u.ls("/")[name]; !ok {
		t.Fatal("un usuario con solo lectura no ve la carpeta")
	}
	if _, err := u.Upload(ctxT(t, time.Minute), small, root, true); err == nil {
		t.Fatal("un usuario de solo lectura pudo subir")
	}
	setUserAccess(t, a, s.ID, testUser, "")
}

// ---- Instantáneas ------------------------------------------------------------------------

func TestSnapshots(t *testing.T) {
	a := admin(t)
	v1 := volume1(t, a)
	name := "E2E-Instantaneas"
	s := ensureShare(t, a, name, v1.ID)
	t.Cleanup(func() { deleteShare(t, a, s) })
	root := "/" + name
	tmp := t.TempDir()
	f := filepath.Join(tmp, "doc.txt")
	_ = os.WriteFile(f, []byte("versión 1\n"), 0o600)
	if _, err := a.Upload(ctxT(t, time.Minute), f, root, false); err != nil {
		t.Fatal(err)
	}
	var r jobRef
	a.call("snapshots.create", map[string]string{"share": s.ID}, &r)
	a.waitJob(r.Job, 2*time.Minute)
	var snaps []struct {
		ID     string `json:"id"`
		Share  string `json:"share"`
		Kind   string `json:"kind"`
		Locked bool   `json:"locked"`
	}
	a.call("snapshots.list", map[string]string{"share": s.ID}, &snaps)
	if len(snaps) == 0 {
		t.Fatal("la instantánea no aparece")
	}
	// Modificar, ver versiones y restaurar.
	_ = os.WriteFile(f, []byte("versión 2\n"), 0o600)
	if _, err := a.Upload(ctxT(t, time.Minute), f, root, true); err != nil {
		t.Fatal(err)
	}
	var vers []struct {
		Snapshot struct {
			ID string `json:"id"`
		} `json:"snapshot"`
	}
	a.call("files.versions", map[string]string{"path": root + "/doc.txt"}, &vers)
	if len(vers) == 0 {
		t.Fatal("files.versions vacío después de una instantánea")
	}
	var restored fileEntry
	a.call("files.restoreVersion", map[string]string{"path": root + "/doc.txt", "snapshot": vers[0].Snapshot.ID}, &restored)
	got := filepath.Join(tmp, "restaurado.txt")
	if _, err := a.Download(ctxT(t, time.Minute), restored.Path, got); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got); string(b) != "versión 1\n" {
		t.Fatalf("la versión restaurada tiene %q", b)
	}
	// La instantánea es de solo lectura en disco.
	out, _ := sshRun(fmt.Sprintf("btrfs property get -ts $(ls -d /volume1/@snapshots/%s/* | head -1) ro 2>/dev/null || true", s.ID))
	if out != "" && !strings.Contains(out, "ro=true") {
		t.Fatalf("instantánea escribible: %s", out)
	}
	// Programación: ida y vuelta + validación.
	sched := map[string]any{"enabled": true, "hourly": map[string]int{"every": 2, "keep": 5}, "daily": map[string]any{"at": "04:15", "keep": 3}, "weekly": map[string]any{"day": 6, "at": "05:00", "keep": 0}}
	a.call("snapshots.schedule.set", map[string]any{"share": s.ID, "schedule": sched}, nil)
	var back struct {
		Hourly struct{ Every, Keep int } `json:"hourly"`
		Daily  struct {
			At string `json:"at"`
		} `json:"daily"`
	}
	a.call("snapshots.schedule.get", map[string]string{"share": s.ID}, &back)
	if back.Hourly.Every != 2 || back.Hourly.Keep != 5 || back.Daily.At != "04:15" {
		t.Fatalf("schedule no persistió: %+v", back)
	}
	a.callErr("snapshots.schedule.set", map[string]any{"share": s.ID, "schedule": map[string]any{"enabled": true, "hourly": map[string]int{"every": 7, "keep": 1}}}, "")
	t.Log("la ejecución programada con intervalo corto se validó en M2.2 con un modo de depuración; no hay perilla en producción (skip documentado)")
	// Borrar una manual.
	a.call("snapshots.delete", map[string]string{"id": snaps[0].ID}, nil)
}
