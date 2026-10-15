//go:build linux

package fileworker

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// share arma una carpeta de prueba con un archivo afuera y symlinks que intentan
// escapar. Devuelve el fd O_PATH de la raíz.
func share(t *testing.T) (int, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "share")
	outside := filepath.Join(base, "secreto.txt")
	must(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	must(t, os.WriteFile(outside, []byte("secreto"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("hola"), 0o644))
	must(t, os.Symlink(outside, filepath.Join(root, "abs-link")))
	must(t, os.Symlink("../secreto.txt", filepath.Join(root, "rel-escape")))
	must(t, os.Symlink("sub/a.txt", filepath.Join(root, "ok-link")))
	must(t, os.Symlink("/proc/self/root/etc/passwd", filepath.Join(root, "magic")))
	fd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY, 0)
	must(t, err)
	t.Cleanup(func() { unix.Close(fd) })
	return fd, root, outside
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestEscapesAreRejected(t *testing.T) {
	root, _, _ := share(t)
	for _, rel := range []string{"../secreto.txt", "abs-link", "rel-escape", "magic", "sub/../../secreto.txt", "/etc/passwd"} {
		f, _, e := openRead(root, rel)
		if e == nil {
			b, _ := io.ReadAll(f)
			f.Close()
			t.Errorf("openRead(%q) escapó: leyó %q", rel, b)
		}
	}
	f, _, e := openRead(root, "ok-link")
	if e != nil {
		t.Fatalf("symlink interno: %v", e)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "hola" {
		t.Fatalf("ok-link leyó %q", b)
	}
}

func TestListHidesInternalsAndEscapes(t *testing.T) {
	root, dir, _ := share(t)
	must(t, os.Mkdir(filepath.Join(dir, TrashDir), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".x.ab.tango-part"), nil, 0o644))
	res, e := list(root, ".", 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	names := map[string]bool{}
	for _, en := range res.Entries {
		names[en.Name] = true
	}
	for _, want := range []string{"sub", "ok-link"} {
		if !names[want] {
			t.Errorf("falta %s en %v", want, names)
		}
	}
	for _, no := range []string{TrashDir, ".x.ab.tango-part", "abs-link", "rel-escape", "magic"} {
		if names[no] {
			t.Errorf("se listó %s", no)
		}
	}
}

func TestUploadCommitTrashRestore(t *testing.T) {
	root, dir, _ := share(t)
	f, part, e := openWrite(root, "sub", "nuevo.bin")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.Write([]byte("datos"))
	f.Close()
	if _, e := commit(root, "sub", part, "nuevo.bin", false); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "sub", "nuevo.bin")); string(b) != "datos" {
		t.Fatalf("contenido %q", b)
	}
	// Sin overwrite no pisa.
	f2, part2, _ := openWrite(root, "sub", "nuevo.bin")
	f2.Close()
	if _, e := commit(root, "sub", part2, "nuevo.bin", false); e == nil || e.Code != CodeExists {
		t.Fatalf("commit pisó un archivo: %v", e)
	}
	must(t, abortErr(abort(root, "sub", part2)))
	// Papelera y vuelta.
	it, e := trash(root, "sub/nuevo.bin")
	if e != nil {
		t.Fatal(e)
	}
	items, _ := trashList(root)
	if len(items) != 1 || items[0].Origin != "sub/nuevo.bin" {
		t.Fatalf("papelera %+v", items)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "nuevo.bin")); err == nil {
		t.Fatal("sigue en su lugar")
	}
	if _, e := restore(root, it.ID); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "sub", "nuevo.bin")); string(b) != "datos" {
		t.Fatal("no se restauró")
	}
	// Commit con un parcial inventado fuera de la carpeta: rechazado.
	if _, e := commit(root, "sub", "../../x.tango-part", "y", false); e == nil {
		t.Fatal("aceptó un parcial con /")
	}
}

func abortErr(e *Error) error {
	if e == nil {
		return nil
	}
	return e
}

func TestMoveAndRename(t *testing.T) {
	root, dir, _ := share(t)
	if _, e := mkdir(root, ".", "destino"); e != nil {
		t.Fatal(e)
	}
	if _, e := rename(root, "sub/a.txt", "b.txt"); e != nil {
		t.Fatal(e)
	}
	if _, e := move(root, "sub/b.txt", root, "destino"); e != nil {
		t.Fatal(e)
	}
	if _, err := os.Stat(filepath.Join(dir, "destino", "b.txt")); err != nil {
		t.Fatal(err)
	}
	if _, e := rename(root, "destino/b.txt", "../escape.txt"); e == nil {
		t.Fatal("rename con / aceptado")
	}
}

// Con openat2 bloqueado (RestrictSUIDSGID=yes en la unidad del agente) la resolución
// manual tiene que dar exactamente las mismas garantías.
func TestFallbackResolver(t *testing.T) {
	noOpenat2.Store(true)
	defer noOpenat2.Store(false)
	TestEscapesAreRejected(t)
	TestListHidesInternalsAndEscapes(t)
	TestUploadCommitTrashRestore(t)
	TestMoveAndRename(t)
	root, dir, _ := share(t)
	must(t, os.Symlink("../sub", filepath.Join(dir, "sub", "loop-up"))) // sub/loop-up → ../sub (adentro)
	if _, e := statRel(root, "sub/loop-up/a.txt"); e != nil {
		t.Fatalf("symlink relativo interno: %v", e)
	}
	must(t, os.Symlink("../../..", filepath.Join(dir, "sub", "up3")))
	if _, e := statRel(root, "sub/up3/etc/passwd"); e == nil {
		t.Fatal("escapó con ../../..")
	}
	must(t, os.Symlink("self", filepath.Join(dir, "self")))
	if _, e := statRel(root, "self"); e == nil {
		t.Fatal("bucle de symlinks aceptado")
	}
}

func TestWatchFDs(t *testing.T) {
	root, dir, _ := share(t)
	sub, e := openBeneath(root, "sub", unix.O_PATH|unix.O_DIRECTORY, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Close(sub)
	// Sin cambios: vence el tiempo y no informa nada.
	r, e := watchFDs(map[int]int{3: sub}, 300*time.Millisecond)
	if e != nil || len(r.Changed) != 0 {
		t.Fatalf("sin cambios: %v %v", r, e)
	}
	// Un archivo nuevo en sub (como lo dejaría Samba) se informa con su índice.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(dir, "sub", "por-smb.txt"), []byte("x"), 0o644)
	}()
	start := time.Now()
	r, e = watchFDs(map[int]int{3: sub}, 5*time.Second)
	if e != nil || len(r.Changed) != 1 || r.Changed[0] != 3 {
		t.Fatalf("cambio: %v %v", r, e)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("no respondió al cambio")
	}
	// Los parciales de TangoOS no cuentan.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(dir, "sub", ".x.1.tango-part"), []byte("x"), 0o644)
	}()
	r, _ = watchFDs(map[int]int{3: sub}, 600*time.Millisecond)
	if len(r.Changed) != 0 {
		t.Fatalf("parcial informado: %v", r)
	}
}
