package diskops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShadowCopySections(t *testing.T) {
	if got := ShadowSnapdir("/volume1/@shares/Familia"); got != "/volume1/@snapshots/Familia" {
		t.Fatalf("snapdir: %q", got)
	}
	for _, p := range []string{"/srv/x", "/volume1/@shares/", "/volume1/@shares/a/b"} {
		if ShadowSnapdir(p) != "" {
			t.Fatalf("%s must not get shadow copies", p)
		}
	}
	for _, sec := range []string{
		SambaShare("Familia", "/volume1/@shares/Familia", "casa"),
		SambaShareWithPerms("Familia", "/volume1/@shares/Familia", "casa", ShareOpts{TimeMachine: true}),
	} {
		for _, want := range []string{
			"vfs objects = fruit streams_xattr shadow_copy2\n",
			"shadow:mountpoint = /volume1/@shares/Familia\n",
			"shadow:snapdir = /volume1/@snapshots/Familia/.gmt\n",
			"shadow:format = %Y-%m-%dT%H-%M-%SZ\n",
			"shadow:localtime = no\n",
			"shadow:sort = desc\n",
		} {
			if !strings.Contains(sec, want) {
				t.Fatalf("missing %q in:\n%s", want, sec)
			}
		}
	}
	if s := SambaShare("X", "/srv/x", ""); strings.Contains(s, "shadow") {
		t.Fatalf("non-TangoOS path got shadow lines:\n%s", s)
	}
	// El formato coincide con SnapshotName (Samba ignora el sufijo de nivel).
	name := SnapshotName(time.Date(2026, 10, 15, 1, 16, 55, 0, time.UTC))
	if name != "2026-10-15T01-16-55Z" {
		t.Fatalf("snapshot name format changed: %s (update SnapshotFormat)", name)
	}
}

func TestSyncShadowLinks(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"2026-10-15T02-00-04Z.hourly", "2026-10-15T02-00-04Z.daily", "2026-10-14T10-00-00Z", "basura"} {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Un enlace viejo (instantánea ya borrada) y un archivo que no es enlace.
	_ = os.MkdirAll(filepath.Join(dir, ShadowLinkDir), 0o755)
	_ = os.Symlink("../2026-01-01T00-00-00Z", filepath.Join(dir, ShadowLinkDir, "2026-01-01T00-00-00Z"))
	_ = os.WriteFile(filepath.Join(dir, ShadowLinkDir, "nota"), []byte("x"), 0o644)
	if err := SyncShadowLinks(dir); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	ents, _ := os.ReadDir(filepath.Join(dir, ShadowLinkDir))
	for _, e := range ents {
		target, _ := os.Readlink(filepath.Join(dir, ShadowLinkDir, e.Name()))
		got[e.Name()] = target
	}
	want := map[string]string{
		"2026-10-15T02-00-04Z": "../2026-10-15T02-00-04Z.daily",
		"2026-10-14T10-00-00Z": "../2026-10-14T10-00-00Z",
		"nota":                 "",
	}
	if len(got) != len(want) {
		t.Fatalf("links: %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s → %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	// Idempotente y sigue el borrado de una instantánea.
	_ = os.RemoveAll(filepath.Join(dir, "2026-10-14T10-00-00Z"))
	if err := SyncShadowLinks(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ShadowLinkDir, "2026-10-14T10-00-00Z")); !os.IsNotExist(err) {
		t.Fatalf("stale link kept: %v", err)
	}
}
