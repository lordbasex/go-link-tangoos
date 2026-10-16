package zfs

import (
	"strings"
	"testing"
)

func TestKeySeal(t *testing.T) {
	pub, priv, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	v, err := Seal(pub, "una frase larga")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "v1.") || strings.Contains(v, "frase") || len(v) > 8000 {
		t.Fatalf("sello %q", v)
	}
	got, err := Open(v, priv)
	if err != nil || got != "una frase larga" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	p2, err := SealPub(v)
	if err != nil || *p2 != *pub || *PubOf(priv) != *pub {
		t.Fatal("clave pública distinta")
	}
	// Otra clave no abre; un sello roto tampoco.
	_, other, _ := NewKeyPair()
	if _, err := Open(v, other); err == nil {
		t.Fatal("abrió con otra clave")
	}
	if _, err := Open(v[:len(v)-4]+"AAAA", priv); err == nil {
		t.Fatal("abrió un sello alterado")
	}
	for _, bad := range []string{"", "v2.abc", "v1.!!", "v1.AAAA"} {
		if _, err := SealPub(bad); err == nil {
			t.Fatalf("SealPub(%q) aceptó", bad)
		}
	}
	// Volver a sellar otra frase con la misma pública (cambio de frase sin el pendrive).
	v2, _ := Seal(pub, "otra frase nueva")
	if got, _ := Open(v2, priv); got != "otra frase nueva" {
		t.Fatal("re-sellado")
	}
	if n := KeyFileName(pub); !strings.HasPrefix(n, "zfs-") || len(n) != len("zfs-")+16+len(".key") {
		t.Fatalf("nombre %q", n)
	}
}
