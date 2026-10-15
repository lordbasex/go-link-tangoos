package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func TestValidatePools(t *testing.T) {
	fp := strings.Repeat("a", 64)
	d := func(id string) agentproto.VolumeDisk { return agentproto.VolumeDisk{DevID: id, Fingerprint: fp} }

	pc := agentproto.PoolCreateArgs{OpID: "j_1", Name: "pool1", Profile: "hybrid1", Disks: []agentproto.VolumeDisk{d("a"), d("b"), d("c")}}
	if e := validatePoolCreate(&pc); e != nil {
		t.Fatal(e)
	}
	for name, mut := range map[string]func(a *agentproto.PoolCreateArgs){
		"nombre con guion":        func(a *agentproto.PoolCreateArgs) { a.Name = "pool-1" },
		"híbrido-2 con 3":         func(a *agentproto.PoolCreateArgs) { a.Profile = "hybrid2" },
		"perfil desconocido":      func(a *agentproto.PoolCreateArgs) { a.Profile = "zfs" },
		"disco repetido":          func(a *agentproto.PoolCreateArgs) { a.Disks[2] = d("a") },
		"huella corta":            func(a *agentproto.PoolCreateArgs) { a.Disks[0].Fingerprint = "x" },
		"op_id":                   func(a *agentproto.PoolCreateArgs) { a.OpID = "../x" },
		"dev_id con barra":        func(a *agentproto.PoolCreateArgs) { a.Disks[0].DevID = "../sda" },
		"nombre de sistema (etc)": func(a *agentproto.PoolCreateArgs) { a.Name = "etc" },
	} {
		b := pc
		b.Disks = append([]agentproto.VolumeDisk{}, pc.Disks...)
		mut(&b)
		if validatePoolCreate(&b) == nil {
			t.Errorf("%s: aceptado", name)
		}
	}

	lv := agentproto.LVCreateArgs{OpID: "j_2", Pool: "pool1", Name: "volume3", Size: 1 << 30, FS: "btrfs", Passphrase: "una frase muy larga", KeyDevice: "ABCD-1234"}
	if e := validateLVCreate(&lv); e != nil {
		t.Fatal(e)
	}
	for name, mut := range map[string]func(a *agentproto.LVCreateArgs){
		"fs":                      func(a *agentproto.LVCreateArgs) { a.FS = "zfs" },
		"chico":                   func(a *agentproto.LVCreateArgs) { a.Size = 1 << 20 },
		"frase corta":             func(a *agentproto.LVCreateArgs) { a.Passphrase = "corta" },
		"frase con salto":         func(a *agentproto.LVCreateArgs) { a.Passphrase = "una frase\nmuy larga" },
		"llave sin cifrado":       func(a *agentproto.LVCreateArgs) { a.Passphrase = "" },
		"llave con UUID inválido": func(a *agentproto.LVCreateArgs) { a.KeyDevice = "../../etc" },
		"nombre":                  func(a *agentproto.LVCreateArgs) { a.Name = "Volume3" },
	} {
		b := lv
		mut(&b)
		if validateLVCreate(&b) == nil {
			t.Errorf("lv %s: aceptado", name)
		}
	}
	lv.Size = 0 // todo el espacio libre
	if validateLVCreate(&lv) != nil {
		t.Error("tamaño 0 = todo el libre")
	}

	if validateLUKSUnlock(&agentproto.LUKSUnlockArgs{Name: "volume3"}) == nil {
		t.Error("desbloquear sin frase ni llave")
	}
	if validateLUKSUnlock(&agentproto.LUKSUnlockArgs{Name: "volume3", Passphrase: "x", UseKeyDevice: true}) == nil {
		t.Error("frase y llave a la vez")
	}
	if validateLUKSPassphrase(&agentproto.LUKSPassphraseArgs{Name: "volume3", Old: "una frase muy larga", New: "una frase muy larga"}) == nil {
		t.Error("frase nueva igual a la vieja")
	}

	cc := agentproto.CacheCreateArgs{OpID: "j_3", Name: "volume3", Mode: "readwrite", Disks: []agentproto.VolumeDisk{d("s1")}}
	if validateCacheCreate(&cc) == nil {
		t.Error("escritura con 1 SSD")
	}
	cc.Disks = append(cc.Disks, d("s2"))
	if e := validateCacheCreate(&cc); e != nil {
		t.Error(e)
	}
	cc.Mode = "read"
	if e := validateCacheCreate(&cc); e != nil {
		t.Error(e)
	}
	cc.Mode = "writeback"
	if validateCacheCreate(&cc) == nil {
		t.Error("modo desconocido")
	}
}

// La frase nunca llega al journal: jobs.start recibe la copia redactada.
func TestLVCreateRedacted(t *testing.T) {
	a := agentproto.LVCreateArgs{OpID: "j_1", Pool: "pool1", Name: "v", FS: "btrfs", Passphrase: "secreto-muy-largo-123"}
	b, _ := json.Marshal(a.Redacted())
	if strings.Contains(string(b), "secreto") {
		t.Fatalf("journal con la frase: %s", b)
	}
	if a.Passphrase != "secreto-muy-largo-123" {
		t.Fatal("Redacted modificó el original")
	}
}
