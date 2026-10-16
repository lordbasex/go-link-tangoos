package zfs

import (
	"strings"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func TestReplNames(t *testing.T) {
	n := ReplSnapName("abc123", time.Date(2026, 10, 16, 12, 30, 5, 0, time.UTC))
	if n != "tgrepl-abc123-20261016T123005Z" || ReplSnapTask(n) != "abc123" || ValidAnySnap(n) != nil {
		t.Fatalf("nombre %q", n)
	}
	if ReplSnapTask("2026-10-15T02-00-04Z.hourly") != "" || ValidAnySnap("2026-10-15T02-00-04Z.hourly") != nil {
		t.Fatal("instantánea programada")
	}
	for _, bad := range []string{"x", "tgrepl-AB-20261016T123005Z", "tgrepl-abc123-2026", "a;b"} {
		if ValidAnySnap(bad) == nil {
			t.Fatalf("aceptó %q", bad)
		}
	}
	for _, ok := range []string{"", "shares", "apps", "shares/Documentos", "apps/jellyfin"} {
		if ValidRel(ok) != nil {
			t.Fatalf("rechazó %q", ok)
		}
	}
	for _, bad := range []string{"x", "shares/", "shares/a/b", "../x", "zvol/a", "shares/-x"} {
		if ValidRel(bad) == nil {
			t.Fatalf("aceptó %q", bad)
		}
	}
	if ValidOrigin("local") != nil || ValidOrigin("0123456789ab") != nil || ValidOrigin("x/y") == nil || ValidOrigin("0123456789AB") == nil {
		t.Fatal("origen")
	}
	if ReplDataset("tank", "local", "volume3") != "tank/tgrepl/local/volume3" {
		t.Fatal("dataset")
	}
	if r, ok := RelOf("tank/volume1", "tank/volume1/shares/Fotos"); !ok || r != "shares/Fotos" {
		t.Fatal("RelOf")
	}
	if _, ok := RelOf("tank/volume1", "tank/volume10"); ok {
		t.Fatal("RelOf aceptó un hermano")
	}
}

func TestArgsSendRecv(t *testing.T) {
	a, err := ArgsSend("tank/v/shares/A", "tgrepl-abc123-20261016T123005Z", "2026-10-15T02-00-04Z.hourly", "", false)
	if err != nil || strings.Join(a, " ") != "send -w -I @2026-10-15T02-00-04Z.hourly tank/v/shares/A@tgrepl-abc123-20261016T123005Z" {
		t.Fatalf("%v %v", a, err)
	}
	a, _ = ArgsSend("tank/v", "tgrepl-abc123-20261016T123005Z", "", "", true)
	if strings.Join(a, " ") != "send -n -P -w tank/v@tgrepl-abc123-20261016T123005Z" {
		t.Fatalf("%v", a)
	}
	tok := "1-e604ea4bf-e0-789c636064000310a500c4ec50360710e72765a5269740f80cd8e4d3d28a534b18e00024cf86249f5459925acc802a8facbf243fbd34338581e1d2a0e6c7c78b1b"
	if a, err = ArgsSend("", "", "", tok, false); err != nil || strings.Join(a, " ") != "send -t "+tok {
		t.Fatalf("%v %v", a, err)
	}
	if _, err := ArgsSend("tank/v", "x;rm", "", "", false); err == nil {
		t.Fatal("aceptó una instantánea inválida")
	}
	if _, err := ArgsSend("", "", "", "1-zz", false); err == nil {
		t.Fatal("aceptó un token inválido")
	}
	if strings.Join(ArgsRecv("tank/tgrepl/local/v", true), " ") != "receive -s -u -o canmount=off -o mountpoint=none -o readonly=on -x sharenfs -x sharesmb -F tank/tgrepl/local/v" {
		t.Fatal("recv")
	}
	if ParseSendSize("full\ttank/v@s\t12345\nsize\t12345\n") != 12345 {
		t.Fatal("size")
	}
}

func TestCheckToken(t *testing.T) {
	out := "resume token contents:\nnvlist version: 0\n\tobject = 0x2\n\toffset = 0x760000\n\ttoguid = 0xacb7951a9004e571\n\ttoname = tank/v/shares/A@tgrepl-abc123-20261016T123005Z\n\tcompressok = 1\n\trawok = 1\nfull\ttank/v/shares/A@tgrepl-abc123-20261016T123005Z\t55195780\nsize\t55195780\n"
	if err := CheckToken(out, "tank/v/shares/A", true); err != nil {
		t.Fatal(err)
	}
	if ParseSendSize(out) != 55195780 {
		t.Fatal("size")
	}
	// Otro dataset (otro volumen del NAS), uno que no es raw o una instantánea ajena: no.
	for _, bad := range []string{
		strings.Replace(out, "tank/v/shares/A@", "tank/otro@", 1),
		strings.Replace(out, "\trawok = 1\n", "", 1),
		strings.Replace(out, "@tgrepl-abc123-20261016T123005Z\n\tcompressok", "@manual\n\tcompressok", 1),
		"",
	} {
		if CheckToken(bad, "tank/v/shares/A", true) == nil {
			t.Fatalf("aceptó:\n%s", bad)
		}
	}
	// Sin cifrar, send -w no marca rawok: se acepta.
	if err := CheckToken(strings.Replace(out, "\trawok = 1\n", "", 1), "tank/v/shares/A", false); err != nil {
		t.Fatal(err)
	}
}

func TestParseReplState(t *testing.T) {
	fs := []byte(`{"datasets":{
	 "tank/v":{"name":"tank/v","properties":{"guid":{"value":"11"},"receive_resume_token":{"value":"-"},"encryption":{"value":"aes-256-gcm"},"encryptionroot":{"value":"tank/v"},"keystatus":{"value":"available"}}},
	 "tank/v/shares":{"name":"tank/v/shares","properties":{"guid":{"value":"12"},"receive_resume_token":{"value":"-"},"encryption":{"value":"off"}}},
	 "tank/v/shares/A":{"name":"tank/v/shares/A","properties":{"guid":{"value":"13"},"receive_resume_token":{"value":"1-abc-def-0123456789abcdef"},"encryption":{"value":"off"}}},
	 "tank/v/x/y":{"name":"tank/v/x/y","properties":{"guid":{"value":"14"}}}}}`)
	sn := []byte(`{"datasets":{
	 "tank/v@b":{"name":"tank/v@b","properties":{"guid":{"value":"22"},"createtxg":{"value":"20"}}},
	 "tank/v@a":{"name":"tank/v@a","properties":{"guid":{"value":"21"},"createtxg":{"value":"10"}}},
	 "tank/v/shares/A@a":{"name":"tank/v/shares/A@a","properties":{"guid":{"value":"31"},"createtxg":{"value":"10"}}}}}`)
	got, err := ParseReplState("tank/v", fs, sn)
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Rel != "" || !got[0].Encrypted || !got[0].KeyLoaded || got[0].EncRoot != "tank/v" || len(got[0].Snaps) != 2 || got[0].Snaps[0].Name != "a" {
		t.Fatalf("raíz %+v", got[0])
	}
	if got[1].Rel != "shares" || got[2].Rel != "shares/A" || got[2].Token == "" || got[2].Snaps[0].GUID != "31" {
		t.Fatalf("%+v", got)
	}
}

func TestPlanDataset(t *testing.T) {
	src := agentproto.ReplDataset{Rel: "shares/A", Snaps: []agentproto.ReplSnap{{Name: "h1", GUID: "1", Txg: 1}, {Name: "s1", GUID: "2", Txg: 2}, {Name: "h2", GUID: "3", Txg: 3}, {Name: "s2", GUID: "4", Txg: 4}}}
	st, err := PlanDataset(src, nil, "s2")
	if err != nil || st.Mode != "full" || st.First != "h1" {
		t.Fatalf("full %+v %v", st, err)
	}
	dst := &agentproto.ReplDataset{Rel: "shares/A", Snaps: []agentproto.ReplSnap{{Name: "h1", GUID: "1", Txg: 7}, {Name: "s1", GUID: "2", Txg: 8}}}
	if st, err = PlanDataset(src, dst, "s2"); err != nil || st.Mode != "incr" || st.From != "s1" {
		t.Fatalf("incr %+v %v", st, err)
	}
	dst.Snaps = append(dst.Snaps, agentproto.ReplSnap{Name: "s2", GUID: "4", Txg: 9})
	if st, _ = PlanDataset(src, dst, "s2"); st.Mode != "skip" {
		t.Fatalf("skip %+v", st)
	}
	if st, _ = PlanDataset(src, &agentproto.ReplDataset{Token: "1-a-b-0123456789abcdef"}, "s2"); st.Mode != "resume" {
		t.Fatalf("resume %+v", st)
	}
	if _, err = PlanDataset(src, &agentproto.ReplDataset{Snaps: []agentproto.ReplSnap{{Name: "z", GUID: "99"}}}, "s2"); err == nil {
		t.Fatal("sin base común")
	}
	// Una sola instantánea: full directo.
	if st, _ = PlanDataset(agentproto.ReplDataset{Snaps: []agentproto.ReplSnap{{Name: "s", GUID: "1"}}}, nil, "s"); st.Mode != "full" || st.First != "" {
		t.Fatalf("%+v", st)
	}
	if got := SnapsAfter(*dst, "2"); len(got) != 1 || got[0] != "s2" {
		t.Fatalf("SnapsAfter %v", got)
	}
}
