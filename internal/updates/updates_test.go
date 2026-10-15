package updates

import (
	"testing"
	"time"
)

const simulate = `NOTE: This is only a simulation!
Reading package lists...
Calculating upgrade...
The following packages will be upgraded:
  libssl3t64 openssl tzdata
Inst libssl3t64 [3.5.1-1] (3.5.1-1+deb13u1 Debian-Security:13/stable-security [amd64])
Inst openssl [3.5.1-1] (3.5.1-1+deb13u1 Debian-Security:13/stable-security [amd64]) []
Inst tzdata [2025b-4] (2026a-0+deb13u1 Debian:13.8/stable [all])
Inst newdep (1.2-1 Debian:13.8/stable [amd64])
Conf libssl3t64 (3.5.1-1+deb13u1 Debian-Security:13/stable-security [amd64])
`

func TestParseSimulate(t *testing.T) {
	u := ParseSimulate(simulate)
	if len(u) != 4 {
		t.Fatalf("got %d: %+v", len(u), u)
	}
	if !u[0].Security || u[0].Name != "libssl3t64" || u[0].From != "3.5.1-1" || u[0].To != "3.5.1-1+deb13u1" {
		t.Fatalf("first: %+v", u[0])
	}
	var tz, nd bool
	for _, p := range u {
		if p.Name == "tzdata" && !p.Security && p.To == "2026a-0+deb13u1" {
			tz = true
		}
		if p.Name == "newdep" && p.From == "" {
			nd = true
		}
	}
	if !tz || !nd {
		t.Fatalf("parse: %+v", u)
	}
	if s := SecurityNames(u); len(s) != 2 {
		t.Fatalf("security: %v", s)
	}
	if ValidPackageName("--allow-unauthenticated") || ValidPackageName("../x") || !ValidPackageName("libssl3t64") {
		t.Fatal("package name validation")
	}
}

func TestPoints(t *testing.T) {
	a := SnapName(time.Date(2026, 10, 14, 22, 0, 0, 0, time.UTC))
	b := SnapName(time.Date(2026, 10, 15, 9, 30, 5, 0, time.UTC))
	if a != "rootfs-20261014-220000" || !ValidSnapID(a) || ValidSnapID("rootfs-../x") {
		t.Fatal(a)
	}
	p := Points([]string{a, "otra", b}, map[string]string{b: "Antes de actualizar 3 paquetes"})
	if len(p) != 2 || p[0].ID != b || p[0].Label != "Antes de actualizar 3 paquetes" || p[1].Label != "Instantánea del sistema" {
		t.Fatalf("%+v", p)
	}
}

func TestParseJournalAndRedact(t *testing.T) {
	out := []byte(`{"__REALTIME_TIMESTAMP":"1792000000000000","PRIORITY":"3","_SYSTEMD_UNIT":"tangod.service","MESSAGE":"login failed password=hunter2 user=sofia"}
{"__REALTIME_TIMESTAMP":"1791999999000000","PRIORITY":"6","_TRANSPORT":"kernel","MESSAGE":[109,100,49,50,55]}
not json
`)
	l := ParseJournal(out, "", 10)
	if len(l) != 2 || l[0].Unit != "tangod" || l[0].Prio != 3 || l[1].Unit != "kernel" || l[1].Msg != "md127" {
		t.Fatalf("%+v", l)
	}
	if l[0].Msg != "login failed password=*** user=sofia" {
		t.Fatalf("redact: %q", l[0].Msg)
	}
	if f := ParseJournal(out, "MD12", 10); len(f) != 1 {
		t.Fatalf("filter: %+v", f)
	}
}
