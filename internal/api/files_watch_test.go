package api

import (
	"testing"
	"time"
)

// Regresión (e2e 2026-10-15): borrar un volumen dejaba vigilantes con fds adentro.
func TestForgetUnder(t *testing.T) {
	fa := &filesAPI{recent: map[string]map[string]*watchDir{
		"admin": {
			"a": {loc: location{root: "/e2em1/@shares/M-e2em1", rel: "."}, last: time.Now()},
			"b": {loc: location{root: "/volume1/@shares/Familia", rel: "Fotos"}, last: time.Now()},
		},
		"sofia": {"c": {loc: location{root: "/e2em1/@shares/Otra", rel: "."}, last: time.Now()}},
	}, watching: map[string]bool{}}
	fa.ForgetUnder("/e2em1/")
	if len(fa.recent["admin"]) != 1 || fa.recent["admin"]["b"] == nil || len(fa.recent["sofia"]) != 0 {
		t.Fatalf("quedaron carpetas del volumen borrado: %+v %+v", fa.recent["admin"], fa.recent["sofia"])
	}
	fa.ForgetUnder("/volume1/@shares/Familia/")
	if len(fa.recent["admin"]) != 0 {
		t.Fatal("ForgetUnder de una carpeta no olvidó su raíz")
	}
}
