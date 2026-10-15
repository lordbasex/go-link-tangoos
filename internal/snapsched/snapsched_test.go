package snapsched

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var ba = time.FixedZone("ART", -3*3600)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, ba)
	if err != nil {
		panic(err)
	}
	return t
}

func TestPrevSlot(t *testing.T) {
	s := Default() // cada 1 h · diario 03:00 · semanal domingo 03:30
	s.Hourly.Every = 6
	cases := []struct {
		level, now, want string
	}{
		{"hourly", "2026-10-14 13:20", "2026-10-14 12:00"},
		{"hourly", "2026-10-14 05:59", "2026-10-14 00:00"},
		{"hourly", "2026-10-14 18:00", "2026-10-14 18:00"},
		{"daily", "2026-10-14 13:20", "2026-10-14 03:00"},
		{"daily", "2026-10-14 02:59", "2026-10-13 03:00"},
		// 2026-10-14 es miércoles: el domingo anterior es el 11.
		{"weekly", "2026-10-14 13:20", "2026-10-11 03:30"},
		{"weekly", "2026-10-11 03:29", "2026-10-04 03:30"},
		{"weekly", "2026-10-11 03:30", "2026-10-11 03:30"},
	}
	for _, c := range cases {
		if got := PrevSlot(s, c.level, at(c.now), time.Hour); !got.Equal(at(c.want)) {
			t.Errorf("%s @ %s = %s, want %s", c.level, c.now, got, c.want)
		}
	}
	// Modo prueba: una "hora" de un minuto.
	s.Hourly.Every = 1
	if got := PrevSlot(s, "hourly", at("2026-10-14 13:20").Add(30*time.Second), time.Minute); !got.Equal(at("2026-10-14 13:20")) {
		t.Errorf("modo minuto: %s", got)
	}
}

func TestDueAndCatchUp(t *testing.T) {
	s := Default()
	now := at("2026-10-14 13:20")
	if !Due(s, "hourly", time.Time{}, now, time.Hour) {
		t.Fatal("sin ejecución previa corresponde una")
	}
	if Due(s, "hourly", at("2026-10-14 13:00"), now, time.Hour) {
		t.Fatal("ya se hizo la de las 13")
	}
	// tangod estuvo apagado 3 días: corresponde una sola (la última), no tres.
	last := at("2026-10-11 03:00")
	if !Due(s, "daily", last, now, time.Hour) {
		t.Fatal("catch-up diario")
	}
	if Due(s, "daily", at("2026-10-14 03:00"), now, time.Hour) {
		t.Fatal("después del catch-up no corresponde otra")
	}
	off := s
	off.Enabled = false
	if Due(off, "hourly", time.Time{}, now, time.Hour) {
		t.Fatal("política apagada")
	}
	none := s
	none.Weekly.Keep = 0
	if Due(none, "weekly", time.Time{}, now, time.Hour) {
		t.Fatal("nivel con keep 0 está apagado")
	}
}

func TestPrune(t *testing.T) {
	names := []string{
		"2026-10-14T15-00-00Z.hourly", "2026-10-14T13-00-00Z.hourly",
		"2026-10-14T16-00-00Z.hourly", "2026-10-14T14-00-00Z.hourly",
	}
	got := Prune(names, 3)
	if !reflect.DeepEqual(got, []string{"2026-10-14T13-00-00Z.hourly"}) {
		t.Fatalf("prune: %v", got)
	}
	if Prune(names, 10) != nil || len(Prune(names, 0)) != 4 {
		t.Fatal("bordes")
	}
}

func TestValidateAndStore(t *testing.T) {
	bad := Default()
	bad.Hourly.Every = 5
	if bad.Validate() == nil {
		t.Fatal("every 5")
	}
	bad = Default()
	bad.Daily.At = "25:00"
	if bad.Validate() == nil {
		t.Fatal("hora inválida")
	}
	p := filepath.Join(t.TempDir(), "s.json")
	st := Open(p)
	if !reflect.DeepEqual(st.Get("fotos"), Default()) {
		t.Fatal("default")
	}
	c := Default()
	c.Hourly = Hourly{Every: 1, Keep: 3}
	if err := st.Set("fotos", c); err != nil {
		t.Fatal(err)
	}
	st.MarkRun("fotos", "hourly", at("2026-10-14 13:00"))
	st2 := Open(p)
	if st2.Get("fotos").Hourly.Keep != 3 || !st2.Last("fotos", "hourly").Equal(at("2026-10-14 13:00")) {
		t.Fatalf("persistencia: %+v", st2.Get("fotos"))
	}
	st2.Forget("fotos")
	if Open(p).Get("fotos").Hourly.Keep != 24 {
		t.Fatal("forget")
	}
}
