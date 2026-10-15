package monitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func kinds(ev []Event) map[string]string {
	out := map[string]string{}
	for _, e := range ev {
		out[e.Alert.ID] = e.Kind
	}
	return out
}

func TestDegradedRebuildResolved(t *testing.T) {
	now := time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)
	vol := VolumeObs{ID: "u1", Name: "volume2", Profile: "raid5", State: "healthy", Size: 100, Used: 10}

	// Sano: nada.
	act, ev := Evaluate(map[string]Alert{}, Observation{Volumes: []VolumeObs{vol}}, now)
	if len(act) != 0 || len(ev) != 0 {
		t.Fatalf("sano: %v %v", act, ev)
	}
	// Un RAID recién creado se construye sin alerta.
	vol.State = "building"
	if act, ev = Evaluate(act, Observation{Volumes: []VolumeObs{vol}}, now); len(act) != 0 || len(ev) != 0 {
		t.Fatalf("construyendo sin degradar: %v", act)
	}
	// Falla un disco.
	vol.State = "degraded"
	act, ev = Evaluate(act, Observation{Volumes: []VolumeObs{vol}}, now)
	if k := kinds(ev); k["md:u1:degraded"] != "raised" || act["md:u1:degraded"].Level != "crit" {
		t.Fatalf("degradado: %v %v", act, ev)
	}
	// Otra pasada igual: sin eventos nuevos (no se repite el aviso).
	if _, ev2 := Evaluate(act, Observation{Volumes: []VolumeObs{vol}}, now); len(ev2) != 0 {
		t.Fatalf("repetido: %v", ev2)
	}
	// Reemplazo en curso: la alerta sigue abierta, baja a warn con el progreso.
	p := 0.4
	vol.State, vol.Progress = "building", &p
	act, ev = Evaluate(act, Observation{Volumes: []VolumeObs{vol}}, now)
	if a := act["md:u1:degraded"]; a.Resolved || a.Level != "warn" || kinds(ev)["md:u1:degraded"] != "changed" {
		t.Fatalf("reconstruyendo: %+v %v", a, ev)
	}
	// Sano de nuevo: se resuelve.
	vol.State, vol.Progress = "healthy", nil
	act, ev = Evaluate(act, Observation{Volumes: []VolumeObs{vol}}, now.Add(time.Hour))
	if a := act["md:u1:degraded"]; !a.Resolved || a.ResolvedAt == "" || kinds(ev)["md:u1:degraded"] != "resolved" {
		t.Fatalf("resuelta: %+v %v", a, ev)
	}
	title, _ := ResolvedNotice(act["md:u1:degraded"], func(string) string { return "volume2" })
	if title != "volume2 volvió a tener redundancia" {
		t.Fatal(title)
	}
	// Vuelve a degradarse: alerta nueva.
	vol.State = "degraded"
	if _, ev = Evaluate(act, Observation{Volumes: []VolumeObs{vol}}, now); kinds(ev)["md:u1:degraded"] != "raised" {
		t.Fatalf("re-alerta: %v", ev)
	}
}

func TestSpaceHysteresisAndSmart(t *testing.T) {
	now := time.Now()
	v := VolumeObs{ID: "u1", Name: "volume1", State: "healthy", Size: 1000, Used: 920} // 8 % libre
	act, ev := Evaluate(nil, Observation{Volumes: []VolumeObs{v}}, now)
	if act["space:u1:low"].Level != "warn" || len(ev) != 1 {
		t.Fatalf("poco espacio: %v", act)
	}
	v.Used = 960 // 4 %: crítico
	act, _ = Evaluate(act, Observation{Volumes: []VolumeObs{v}}, now)
	if act["space:u1:low"].Level != "crit" {
		t.Fatal("crítico")
	}
	v.Used = 890 // 11 %: sigue abierta por histéresis
	act, _ = Evaluate(act, Observation{Volumes: []VolumeObs{v}}, now)
	if a := act["space:u1:low"]; a.Resolved || a.Level != "warn" {
		t.Fatalf("histéresis: %+v", a)
	}
	v.Used = 850 // 15 %: se resuelve
	act, _ = Evaluate(act, Observation{Volumes: []VolumeObs{v}}, now)
	if !act["space:u1:low"].Resolved {
		t.Fatal("resuelta")
	}
	d := DiskObs{ID: "wwn-1", Label: "El disco de la bahía 3", Smart: "crit"}
	act, ev = Evaluate(act, Observation{Volumes: []VolumeObs{v}, Disks: []DiskObs{d}}, now)
	if act["smart:wwn-1"].Level != "crit" || kinds(ev)["smart:wwn-1"] != "raised" {
		t.Fatalf("smart: %v", act)
	}
}

func TestStoreAndBtrfsErrors(t *testing.T) {
	dir := t.TempDir()
	s := OpenStore(filepath.Join(dir, "alerts.json"))
	m := map[string]Alert{}
	for i := range 60 {
		id := string(rune('a'+i%26)) + string(rune('a'+i/26))
		m[id] = Alert{ID: id, Resolved: true, ResolvedAt: time.Unix(int64(i), 0).UTC().Format(time.RFC3339)}
	}
	m["open"] = Alert{ID: "open", At: "2026-10-14T00:00:00Z"}
	s.Replace(m)
	list := OpenStore(filepath.Join(dir, "alerts.json")).List()
	if len(list) != 51 || list[0].ID != "open" {
		t.Fatalf("poda: %d %v", len(list), list[0])
	}

	uuid := "0b8f4c2e-1111-4222-8333-444455556666"
	p := filepath.Join(dir, "fs/btrfs", uuid, "devinfo/1/error_stats")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("write_errs 0\nread_errs 2\nflush_errs 0\ncorruption_errs 1\ngeneration_errs 0\n"), 0o644)
	if n := BtrfsErrors(dir, uuid); n != 3 {
		t.Fatalf("errores: %d", n)
	}
	if BtrfsErrors(dir, "../etc") != 0 || BtrfsErrors(dir, "md-volume2") != 0 {
		t.Fatal("solo UUIDs")
	}
}
