package gluster

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBrickForms(t *testing.T) {
	cases := []struct {
		path, kind string
		ok         bool
	}{
		{"/volume1/@gluster/gv0", "dir", true},
		{"/srv/gluster/gv0/brick", "lv", true},
		{"/srv/gluster/gv0", "", false},
		{"/srv/gluster/gv0/brick/../x", "", false},
		{"/srv/gluster/otro/brick", "lv", false}, // otro volumen
	}
	for _, c := range cases {
		if k := BrickKind(c.path); k != c.kind {
			t.Errorf("%s: kind %q, esperaba %q", c.path, k, c.kind)
		}
		err := ValidBrick(Brick{Host: "10.0.0.1", Path: c.path}, "gv0")
		if (err == nil) != c.ok {
			t.Errorf("%s: ValidBrick %v", c.path, err)
		}
	}
	if LVBrickPath("gv0") != "/srv/gluster/gv0/brick" || LVName("gv0") != "gb_gv0" || LVMountUnitName("gv_a") != "srv-gluster-gv_a.mount" {
		t.Fatal("rutas del LV")
	}
	// mezcla de formas en un mismo volumen
	if err := ValidateCreate("gv0", Layout{Replica: 2}, []Brick{{"a", "/volume1/@gluster/gv0"}, {"b", "/srv/gluster/gv0/brick"}}); err != nil {
		t.Fatal(err)
	}
}

func TestTLSManaged(t *testing.T) {
	o := TLSOptions(true, []string{"n1", "n2"})
	if o["auth.ssl-allow"] != "n1,n2" || o["client.ssl"] != "on" || o["server.ssl"] != "on" {
		t.Fatalf("%v", o)
	}
	if o := TLSOptions(false, nil); o["client.ssl"] != "off" || o["auth.ssl-allow"] != "" {
		t.Fatalf("%v", o)
	}
	if _, err := ArgsSetManaged("gv0", "auth.ssl-allow", "n1,-x"); err == nil {
		t.Fatal("aceptó un CN inválido")
	}
	if _, err := ArgsSetManaged("gv0", "auth.allow", "*"); err == nil {
		t.Fatal("aceptó una opción no administrada")
	}
	// las opciones TLS no se pueden tocar como opciones de usuario
	for _, k := range []string{"client.ssl", "server.ssl", "auth.ssl-allow"} {
		if ValidOption(k, "on") == nil {
			t.Errorf("%s pasó la allow-list de usuario", k)
		}
	}
}

func TestQuota(t *testing.T) {
	for _, p := range []string{"/Documentos", "/Fotos 2026", "/a.b_c-d"} {
		if err := ValidQuotaPath(p); err != nil {
			t.Errorf("rechazó %q: %v", p, err)
		}
	}
	// solo primer nivel: un namespace adentro de otro saca su uso del de arriba
	for _, p := range []string{"/", "Docs", "/../etc", "/a/b", "/-x", "/a//b", "/.."} {
		if ValidQuotaPath(p) == nil {
			t.Errorf("aceptó %q", p)
		}
	}
	if ValidQuotaLimit(100) == nil || ValidQuotaLimit(1<<20) != nil {
		t.Error("límites")
	}
	on := ArgsQuotaOn("gv0", true)
	if len(on) != 2 || !slices.Equal(on[1], []string{"volume", "set", "gv0", "feature.simple-quota-pass-through", "false"}) {
		t.Fatalf("%v", on)
	}
	if !QuotaOn(map[string]string{"feature.simple-quota-pass-through": "false", "features.tag-namespaces": "on"}) ||
		QuotaOn(map[string]string{"features.quota": "on"}) {
		t.Error("QuotaOn")
	}
	if v, err := ParseXattrInt([]byte("20971520\x00")); err != nil || v != 20971520 {
		t.Fatalf("%d %v", v, err)
	}
	m := ParseQuotaOption("/Fotos 2026=2097152/Docs=1048576/bad=1/..=1048576/")
	if len(m) != 2 || m["/Docs"] != 1<<20 || m["/Fotos 2026"] != 2<<20 {
		t.Fatalf("%v", m)
	}
	if FormatQuotaOption(m) != "/Docs=1048576/Fotos 2026=2097152" || FormatQuotaOption(nil) != "" {
		t.Fatalf("%q", FormatQuotaOption(m))
	}
	q := NewQuota("/docs", 100<<20, 85<<20)
	if !q.SoftOver || q.HardOver || q.Avail != 15<<20 {
		t.Fatalf("%+v", q)
	}
	if q := NewQuota("/docs", 20971520, 20914176); !q.HardOver { // frenó apenas debajo del límite
		t.Fatalf("%+v", q)
	}
	if q := NewQuota("/docs", 100<<20, 120<<20); !q.HardOver || q.Avail != 0 {
		t.Fatalf("%+v", q)
	}
	if NodeUUID([]byte("UUID=e58cfe8d-156e\noperating-version=110000\n")) != "e58cfe8d-156e" {
		t.Error("NodeUUID")
	}
}

func TestTasks(t *testing.T) {
	r, err := ParseTask(fixture(t, "rebalance-status.xml"), "tg-gluster1")
	if err != nil || !r.Done || r.Failed || r.Total.Lookups != 30 || r.Nodes[0].Node != "tg-gluster1" || r.ID == "" {
		t.Fatalf("%+v %v", r, err)
	}
	rb, err := ParseTask(fixture(t, "remove-brick-status.xml"), "")
	if err != nil || !rb.Done || rb.Total.Lookups != 40 || rb.Nodes[0].Node != "localhost" {
		t.Fatalf("%+v %v", rb, err)
	}
	if _, err := ParseTask(fixture(t, "rebalance-not-distribute.xml"), ""); err == nil || !strings.Contains(err.Error(), "not a distribute") {
		t.Fatalf("esperaba el error de GlusterFS: %v", err)
	}
	if _, err := ArgsRebalance("gv0", "fix-layout"); err == nil {
		t.Fatal("aceptó una acción desconocida")
	}
}

func TestSnapshots(t *testing.T) {
	s, err := ParseSnapshots(fixture(t, "snapshot-info.xml"))
	if err != nil || len(s) != 1 || s[0].Name != "s1" || s[0].Desc != "prueba uno" || s[0].Status != "Stopped" || !strings.HasPrefix(s[0].Created, "2026-10-15") {
		t.Fatalf("%+v %v", s, err)
	}
	a, err := ArgsSnapshot("create", "antes-de-migrar", "gv0", "Antes de migrar")
	if err != nil || !slices.Equal(a, []string{"snapshot", "create", "antes-de-migrar", "gv0", "no-timestamp", "description", "Antes de migrar"}) {
		t.Fatalf("%v %v", a, err)
	}
	for _, bad := range []struct{ op, snap, desc string }{{"create", "-x", ""}, {"create", "ok", `con "comillas"`}, {"create", "all", ""}, {"clone", "ok", ""}} {
		if _, err := ArgsSnapshot(bad.op, bad.snap, "gv0", bad.desc); err == nil {
			t.Errorf("aceptó %+v", bad)
		}
	}
	if a, _ := ArgsSnapshot("info", "", "gv0", ""); !slices.Equal(a, []string{"snapshot", "info", "volume", "gv0"}) {
		t.Fatalf("%v", a)
	}
}
