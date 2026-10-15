package gluster

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fx(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestValidVolumeName(t *testing.T) {
	for _, ok := range []string{"gv0", "gv_tel_locuciones", "a"} {
		if err := ValidVolumeName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Gv0", "0gv", "gv-0", "gv.0", "all", "-x", strings.Repeat("a", 33), "gv 0", "../x"} {
		if ValidVolumeName(bad) == nil {
			t.Errorf("%q debería ser inválido", bad)
		}
	}
}

func TestValidHost(t *testing.T) {
	for _, ok := range []string{"172.17.0.48", "fe80::1", "tg-gluster1", "glusterfsacens1", "nas.local"} {
		if err := ValidHost(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-help", "0.0.0.0", "224.0.0.1", "host name", "a;b", "UPPER", "x..y", "host:/p"} {
		if ValidHost(bad) == nil {
			t.Errorf("%q debería ser inválido", bad)
		}
	}
}

func bricks(vol string, hosts ...string) []Brick {
	var out []Brick
	for _, h := range hosts {
		out = append(out, Brick{Host: h, Path: BrickPath("volume1", vol)})
	}
	return out
}

func TestValidateCreate(t *testing.T) {
	if err := ValidateCreate("gv0", Layout{Replica: 3}, bricks("gv0", "n1", "n2", "n3")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCreate("gv0", Layout{Replica: 3, Arbiter: true}, bricks("gv0", "n1", "n2", "n3")); err != nil {
		t.Fatal(err)
	}
	// Distribuido-replicado: 2 conjuntos de 3 en los mismos 3 nodos con otro volumen de TangoOS.
	b := bricks("gv0", "n1", "n2", "n3")
	for _, h := range []string{"n1", "n2", "n3"} {
		b = append(b, Brick{Host: h, Path: BrickPath("volume2", "gv0")})
	}
	if err := ValidateCreate("gv0", Layout{Replica: 3}, b); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		l Layout
		b []Brick
	}{
		"no múltiplo":      {Layout{Replica: 3}, bricks("gv0", "n1", "n2")},
		"mismo nodo":       {Layout{Replica: 3}, append(bricks("gv0", "n1", "n2"), Brick{Host: "n1", Path: BrickPath("volume2", "gv0")})},
		"repetido":         {Layout{Replica: 2}, bricks("gv0", "n1", "n1")},
		"árbitro sin 3":    {Layout{Replica: 2, Arbiter: true}, bricks("gv0", "n1", "n2")},
		"réplica alta":     {Layout{Replica: 5}, bricks("gv0", "n1", "n2", "n3", "n4", "n5")},
		"ruta de otro vol": {Layout{Replica: 2}, bricks("gv1", "n1", "n2")},
		"ruta arbitraria":  {Layout{Replica: 1}, []Brick{{Host: "n1", Path: "/etc"}}},
		"ruta con ..":      {Layout{Replica: 1}, []Brick{{Host: "n1", Path: "/volume1/@gluster/gv0/../../etc/brick"}}},
		"host como opción": {Layout{Replica: 1}, []Brick{{Host: "-force", Path: BrickPath("volume1", "gv0")}}},
		"sin bricks":       {Layout{Replica: 1}, nil},
		"réplica cero":     {Layout{Replica: 0}, bricks("gv0", "n1")},
	}
	for name, c := range cases {
		if ValidateCreate("gv0", c.l, c.b) == nil {
			t.Errorf("%s: debería fallar", name)
		}
	}
}

func TestOptionsAndPresets(t *testing.T) {
	for _, p := range Presets() {
		if err := ValidOptions(p.Options); err != nil {
			t.Errorf("preset %s: %v", p.ID, err)
		}
	}
	p, ok := PresetByID("production-replica3")
	if !ok || len(p.Options) != 17 || p.Options["cluster.server-quorum-ratio"] != "51%" || p.Options["network.ping-timeout"] != "10" {
		t.Fatalf("preset de producción: %+v", p)
	}
	bad := map[string]string{
		"cluster.quorum-type":         "maybe",
		"network.ping-timeout":        "0",
		"cluster.server-quorum-ratio": "51",
		"performance.read-ahead":      "yes",
		"auth.allow":                  "*",
		"storage.owner-uid":           "0",
		"features.trash":              "on",
	}
	for k, v := range bad {
		if ValidOption(k, v) == nil {
			t.Errorf("%s=%s debería rechazarse", k, v)
		}
	}
	if got := ArgsSet("gv0", "cluster.server-quorum-ratio", "51%"); !reflect.DeepEqual(got, []string{"volume", "set", "all", "cluster.server-quorum-ratio", "51%"}) {
		t.Errorf("ratio global: %v", got)
	}
	for _, o := range Options() {
		if o.Key == "auth.allow" {
			t.Error("auth.allow no se ofrece en la UI")
		}
	}
}

func TestArgs(t *testing.T) {
	got := ArgsVolumeCreate("gv0", Layout{Replica: 3, Arbiter: true}, bricks("gv0", "n1", "n2", "n3"))
	want := []string{"volume", "create", "gv0", "replica", "3", "arbiter", "1", "transport", "tcp",
		"n1:/volume1/@gluster/gv0/brick", "n2:/volume1/@gluster/gv0/brick", "n3:/volume1/@gluster/gv0/brick"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("create: %v", got)
	}
	for _, a := range [][]string{got, ArgsAddBrick("gv0", 0, bricks("gv0", "n4")), ArgsHeal("gv0", true)} {
		for _, s := range a {
			if s == "force" {
				t.Errorf("no se usa force: %v", a)
			}
		}
	}
	if _, err := ArgsVolumeAction("gv0", "rebalance"); err == nil {
		t.Error("acción fuera de la lista")
	}
	if a, err := ArgsRemoveBrick("gv0", 2, bricks("gv0", "n3"), "commit"); err != nil || a[len(a)-1] != "force" {
		t.Errorf("bajar réplica: %v %v", a, err)
	}
	if _, err := ArgsRemoveBrick("gv0", 2, bricks("gv0", "n3"), "start"); err == nil {
		t.Error("bajar réplica solo con commit")
	}
}

func TestParsePool(t *testing.T) {
	peers, err := ParsePool(fx(t, "pool-list-down.xml"), "tg-gluster1")
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 3 || !peers[2].Self || peers[2].Host != "tg-gluster1" || peers[1].Connected || !peers[0].Connected {
		t.Fatalf("%+v", peers)
	}
	alone, err := ParsePool(fx(t, "pool-list-alone.xml"), "tg-gluster1")
	if err != nil || len(alone) != 1 || !alone[0].Self {
		t.Fatalf("%+v %v", alone, err)
	}
}

func TestParseVolumes(t *testing.T) {
	vols, err := ParseVolumes(fx(t, "volume-info.xml"), fx(t, "volume-status.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 2 {
		t.Fatalf("volúmenes: %d", len(vols))
	}
	gv0 := vols[0]
	if gv0.Name != "gv0" || gv0.Type != "Replicate" || gv0.Replica != 3 || gv0.Sets != 1 || gv0.Status != "Started" || gv0.Online != 3 {
		t.Fatalf("gv0: %+v", gv0)
	}
	if gv0.Options["cluster.quorum-type"] != "auto" || len(gv0.OptOrder) != len(gv0.Options) {
		t.Errorf("opciones: %v", gv0.Options)
	}
	if gv0.Bricks[0].Port == 0 || gv0.SizeTotal == 0 || gv0.Bricks[0].FsName != "ext4" {
		t.Errorf("status: %+v", gv0.Bricks[0])
	}
	if gv1 := vols[1]; gv1.Arbiter != 1 || !gv1.Bricks[2].Arbiter {
		t.Errorf("árbitro: %+v", gv1)
	}
	txt := InfoText(gv0)
	// Igual, línea por línea, a la salida real de `gluster volume info gv0`.
	if real := strings.TrimSpace(string(fx(t, "volume-info-gv0.txt"))); strings.TrimSpace(txt) != real {
		t.Errorf("InfoText distinto del CLI:\n%s\n---\n%s", txt, real)
	}
	for _, s := range []string{"Number of Bricks: 1 x 3 = 3", "Brick3: tg-gluster3:/srv/fx/gv0/b", "Options Reconfigured:", "cluster.server-quorum-type: server"} {
		if !strings.Contains(txt, s) {
			t.Errorf("info sin %q:\n%s", s, txt)
		}
	}
	if !strings.Contains(InfoText(vols[1]), "1 x (2 + 1) = 3") {
		t.Errorf("árbitro en info:\n%s", InfoText(vols[1]))
	}

	// Nodo caído: su brick no aparece en el status y queda fuera de línea.
	down, err := ParseVolumes(fx(t, "volume-info.xml"), fx(t, "volume-status-down.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if down[0].Online != 2 || down[0].Bricks[2].Online {
		t.Errorf("caído: %+v", down[0].Bricks)
	}
	// Sin status (glusterd no respondió): todo fuera de línea, sin error.
	if v, err := ParseVolumes(fx(t, "volume-info.xml"), nil); err != nil || v[0].Online != 0 {
		t.Errorf("sin status: %v %v", v, err)
	}
	if v, err := ParseVolumes(fx(t, "volume-info-empty.xml"), nil); err != nil || len(v) != 0 {
		t.Errorf("vacío: %v %v", v, err)
	}
}

func TestParseHeal(t *testing.T) {
	h, err := ParseHeal(fx(t, "heal-summary-pending.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 3 || h[0].Pending != 6 || h[2].Connected || h[2].Pending != -1 || strings.Contains(h[2].Status, "totalNumberOfEntries") {
		t.Fatalf("%+v", h)
	}
	vols, _ := ParseVolumes(fx(t, "volume-info.xml"), fx(t, "volume-status-down.xml"))
	MergeHeal(&vols[0], h)
	if vols[0].HealPending != 12 || vols[0].Bricks[2].HealPending != -1 {
		t.Errorf("merge: %+v", vols[0])
	}
	after, err := ParseHeal(fx(t, "heal-summary-after.xml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range after {
		if !b.Connected || b.Pending != 0 || b.SplitBrain != 0 {
			t.Errorf("después de curar: %+v", b)
		}
	}
}

func TestErrors(t *testing.T) {
	for name, want := range map[string]string{
		"volume-create-error.xml": "Volume gv0 already exists",
		"peer-detach-error.xml":   "hosts one or more bricks",
		"volume-info-missing.xml": "Volume does not exist",
	} {
		err := ParseResult(fx(t, name), 1)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !IsNotFound(ParseResult(fx(t, "volume-info-missing.xml"), 1)) {
		t.Error("IsNotFound")
	}
	for _, ok := range []string{"volume-create.xml", "volume-start.xml", "volume-set.xml", "volume-set-all.xml", "peer-probe.xml", "heal.xml"} {
		if err := ParseResult(fx(t, ok), 0); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	if err := ParseResult([]byte("Connection failed. Please check if gluster daemon is operational.\n"), 1); err == nil || !strings.Contains(err.Error(), "gluster daemon") {
		t.Errorf("texto plano: %v", err)
	}
}

func TestUnits(t *testing.T) {
	u := MountUnit("gv0", []string{"tg-gluster2", "172.17.0.50", "-bad"})
	for _, s := range []string{"Where=/cluster/gv0", "What=localhost:/gv0", "Type=glusterfs", "backup-volfile-servers=tg-gluster2:172.17.0.50", "_netdev"} {
		if !strings.Contains(u, s) {
			t.Errorf("mount sin %q:\n%s", s, u)
		}
	}
	if strings.Contains(u, "-bad") {
		t.Error("host inválido en backup-volfile-servers")
	}
	if MountUnitName("gv_tel") != "cluster-gv_tel.mount" {
		t.Error(MountUnitName("gv_tel"))
	}
	r := FirewallRules([]string{"172.17.0.49", "172.17.0.50", "172.17.0.49", "fe80::2", "nope"})
	for _, s := range []string{"elements = { 172.17.0.49, 172.17.0.50 }", "elements = { fe80::2 }", "tcp dport { 24007, 24008, 49152-60999 } drop", "delete table inet tango_gluster"} {
		if !strings.Contains(r, s) {
			t.Errorf("reglas sin %q:\n%s", s, r)
		}
	}
	if strings.Contains(FirewallRules(nil), "elements") {
		t.Error("set vacío sin elements")
	}
	if strings.Contains(FirewallUnit, "ExecStop") {
		t.Error("detener el firewall no puede abrir los puertos")
	}
}
