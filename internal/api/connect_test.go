package api

import "testing"

func TestGuestScope(t *testing.T) {
	ok := map[[2]string]string{
		{"/Familia/Viaje", ""}:            "/Familia/Viaje",
		{"/Familia/Viaje", "/"}:           "/Familia/Viaje",
		{"/Familia/Viaje", "fotos/a.jpg"}: "/Familia/Viaje/fotos/a.jpg",
		{"/Familia/Viaje", "/fotos/./b"}:  "/Familia/Viaje/fotos/b",
		{"/Familia/Viaje", "x/../y"}:      "/Familia/Viaje/y",
	}
	for in, want := range ok {
		got, err := guestScope(in[0], in[1])
		if err != nil || got != want {
			t.Errorf("guestScope(%q,%q) = %q, %v; quiero %q", in[0], in[1], got, err, want)
		}
	}
	for _, rel := range []string{"..", "../Otra", "../Viaje2/x", "fotos/../../..", "a\x00b"} {
		if got, err := guestScope("/Familia/Viaje", rel); err == nil {
			t.Errorf("guestScope escapó con %q → %q", rel, got)
		}
	}
	// Un hermano con el mismo prefijo no es parte del elemento compartido.
	if _, err := guestScope("/Familia/Viaje", "../Viajes"); err == nil {
		t.Error("prefijo hermano aceptado")
	}
}
