package api

import "testing"

func TestSplitTrashPath(t *testing.T) {
	sh, id, ok := splitTrashPath("/Familia/.tango-trash/6a1b2c3d0011223344ff")
	if !ok || sh != "Familia" || id != "6a1b2c3d0011223344ff" {
		t.Fatalf("%q %q %v", sh, id, ok)
	}
	for _, bad := range []string{"/Familia/x/6a1b2c3d0011223344ff", "/Familia/.tango-trash/../../etc", "/Familia/.tango-trash/zz", "/.tango-trash/6a1b2c3d0011223344ff"} {
		if _, _, ok := splitTrashPath(bad); ok {
			t.Errorf("aceptó %q", bad)
		}
	}
}

func TestKindOf(t *testing.T) {
	cases := map[string]string{"IMG_1.JPG": "image", "peli.mkv": "video", "tema.flac": "audio", "a.pdf": "pdf", "b.xlsx": "sheet", "c.md": "text", "d.go": "code", "e.zip": "archive", "sin": "other"}
	for in, want := range cases {
		if got := KindOf(in); got != want {
			t.Errorf("KindOf(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestLocationPaths(t *testing.T) {
	l := location{share: Share{Name: "Familia"}, root: "/volume1/@shares/Familia", rel: "."}
	if l.vpath(".") != "/Familia" || l.vpath("a/b.txt") != "/Familia/a/b.txt" {
		t.Fatal(l.vpath("."), l.vpath("a/b.txt"))
	}
	if itoa(0) != "0" || itoa(-12) != "-12" || itoa(1234567) != "1234567" {
		t.Fatal("itoa")
	}
}

func TestByteRange(t *testing.T) {
	i := func(v int64) *int64 { return &v }
	cases := []struct {
		size     int64
		off, ln  *int64
		from, to int64
		wantErr  bool
	}{
		{100, nil, nil, 0, 100, false},
		{100, i(10), nil, 10, 100, false},
		{100, i(10), i(20), 10, 30, false},
		{100, i(90), i(50), 90, 100, false}, // se recorta al final
		{100, i(100), nil, 100, 100, false}, // rango vacío al final
		{100, i(101), nil, 0, 0, true},
		{100, i(-1), nil, 0, 0, true},
		{100, nil, i(-5), 0, 0, true},
	}
	for _, c := range cases {
		from, to, err := byteRange(c.size, c.off, c.ln)
		if (err != nil) != c.wantErr || (!c.wantErr && (from != c.from || to != c.to)) {
			t.Errorf("byteRange(%d,%v,%v) = %d,%d,%v", c.size, c.off, c.ln, from, to, err)
		}
	}
}
