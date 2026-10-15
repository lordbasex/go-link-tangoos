package fileworker

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestCleanRel(t *testing.T) {
	ok := map[string]string{"": ".", "/": ".", "a/b": "a/b", "/a//b/": "a/b", "a/./b": "a/b", "a/../b": "b"}
	for in, want := range ok {
		got, e := CleanRel(in)
		if e != nil || got != want {
			t.Errorf("CleanRel(%q) = %q, %v; want %q", in, got, e, want)
		}
	}
	for _, bad := range []string{"..", "../etc", "a/../../etc", "/../x", "x\x00y", ".tango-trash/x", "a/.tango-trash", "a/x.tango-part"} {
		if _, e := CleanRel(bad); e == nil {
			t.Errorf("CleanRel(%q) aceptó una ruta inválida", bad)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, good := range []string{"foto.jpg", "Mis cosas", "ñandú (2).pdf", ".bashrc"} {
		if e := ValidName(good); e != nil {
			t.Errorf("ValidName(%q) = %v", good, e)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", "x\x00", ".tango-trash", "a.tango-part", " espacio", string(make([]byte, 256))} {
		if e := ValidName(bad); e == nil {
			t.Errorf("ValidName(%q) aceptó un nombre inválido", bad)
		}
	}
}

func TestValidRoot(t *testing.T) {
	for _, good := range []string{"/volume1/@shares/Familia", "/volume2/@shares/Mis Fotos", "/volume1/@snapshots/Familia/2026-10-14T21.42.00Z"} {
		if e := ValidRoot(good); e != nil {
			t.Errorf("ValidRoot(%q) = %v", good, e)
		}
	}
	for _, bad := range []string{"/", "/etc", "/volume1", "/volume1/@shares", "/volume1/@shares/../../etc", "/volume1/@shares/..", "/volume1/@apps/x", "volume1/@shares/x", "/volume1/@shares/a/b", "/volume1/@snapshots/Familia"} {
		if e := ValidRoot(bad); e == nil {
			t.Errorf("ValidRoot(%q) aceptó una raíz inválida", bad)
		}
	}
}

func TestFrameRoundTrip(t *testing.T) {
	data := []byte("hola mundo")
	b := EncodeFrame(nil, 0xDEADBEEF, 1<<40+7, data)
	if len(b) != FrameHeader+len(data) {
		t.Fatalf("largo %d", len(b))
	}
	id, off, got, err := DecodeFrame(b)
	if err != nil || id != 0xDEADBEEF || off != 1<<40+7 || !bytes.Equal(got, data) {
		t.Fatalf("decode = %x %d %q %v", id, off, got, err)
	}
	if _, _, _, err := DecodeFrame(b[:11]); err == nil {
		t.Fatal("aceptó un frame corto")
	}
	// Reusa el buffer sin realocar.
	buf := make([]byte, 0, 100)
	if out := EncodeFrame(buf, 1, 2, data); &out[0] != &buf[:1][0] {
		t.Fatal("no reusó el buffer")
	}
}

func TestMakeThumb(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 1000; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	out, err := MakeThumb(&buf, 128)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || format != "jpeg" || cfg.Width != 128 || cfg.Height != 64 {
		t.Fatalf("miniatura %dx%d %s %v", cfg.Width, cfg.Height, format, err)
	}
	if _, err := MakeThumb(bytes.NewReader([]byte("no es una imagen")), 128); err == nil {
		t.Fatal("aceptó algo que no es imagen")
	}
}

func TestHidden(t *testing.T) {
	for _, h := range []string{".tango-trash", "#recycle", ".tango-x", ".foto.jpg.ab12.tango-part"} {
		if !Hidden(h) {
			t.Errorf("Hidden(%q) = false", h)
		}
	}
	if Hidden(".bashrc") || Hidden("foto.jpg") {
		t.Error("ocultó un nombre normal")
	}
}
