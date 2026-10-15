package diskops

import (
	"strings"
	"testing"
)

func TestTimeMachineSection(t *testing.T) {
	plain := SambaShareWithPerms("Familia", "/volume1/@shares/Familia", "casa")
	if strings.Contains(plain, "time machine") {
		t.Fatal("una carpeta común no debe ser de Time Machine")
	}
	tm := SambaShareWithPerms("TimeMachine", "/volume1/@shares/TimeMachine", "Respaldos", ShareOpts{TimeMachine: true, TMMaxBytes: 200 << 30})
	for _, want := range []string{"[TimeMachine]", "fruit:time machine = yes", "fruit:time machine max size = 200G", "vfs objects = fruit streams_xattr"} {
		if !strings.Contains(tm, want) {
			t.Errorf("falta %q en\n%s", want, tm)
		}
	}
	// El tope se redondea hacia arriba a GiB enteros; sin tope no hay línea.
	if s := SambaShare("TM", "/v/@shares/TM", "", ShareOpts{TimeMachine: true, TMMaxBytes: 1}); !strings.Contains(s, "max size = 1G") {
		t.Fatal(s)
	}
	if s := SambaShare("TM", "/v/@shares/TM", "", ShareOpts{TimeMachine: true}); strings.Contains(s, "max size") {
		t.Fatal(s)
	}
}

func TestSambaGlobalsForMac(t *testing.T) {
	idx := SambaIndex([]string{"tm.conf"})
	for _, want := range []string{"[global]", "fruit:aapl = yes", "fruit:model = MacSamba", "fruit:nfs_aces = no", "fruit:posix_rename = yes",
		"fruit:veto_appledouble = no", "fruit:wipe_intentionally_left_blank_rfork = yes", "fruit:delete_empty_adfiles = yes", "multicast dns register = no",
		"include = /etc/samba/tango.d/tm.conf"} {
		if !strings.Contains(idx, want) {
			t.Errorf("falta %q", want)
		}
	}
	if strings.Index(idx, "[global]") > strings.Index(idx, "include =") {
		t.Fatal("los globales tienen que ir antes de las carpetas")
	}
}

func TestTimeMachineBonjour(t *testing.T) {
	secs := []string{
		SambaShareWithPerms("Familia", "/volume1/@shares/Familia", ""),
		SambaShareWithPerms("TimeMachine", "/volume1/@shares/TimeMachine", "", ShareOpts{TimeMachine: true}),
		SambaShareWithPerms("Backups", "/volume2/@shares/Backups", "", ShareOpts{TimeMachine: true, TMMaxBytes: 1 << 40}),
	}
	tm := TimeMachineShares(secs)
	if strings.Join(tm, ",") != "Backups,TimeMachine" {
		t.Fatalf("%v", tm)
	}
	txt := AdiskTXT(tm)
	want := []string{"sys=waMa=0,adVF=0x100", "dk0=adVN=Backups,adVF=0x82", "dk1=adVN=TimeMachine,adVF=0x82"}
	if strings.Join(txt, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", txt)
	}
	svc := AvahiSMBService(tm)
	for _, w := range []string{"<type>_smb._tcp</type>", "<port>445</port>", "<type>_device-info._tcp</type>", "model=TimeCapsule8,119",
		"<type>_adisk._tcp</type>", "<txt-record>dk1=adVN=TimeMachine,adVF=0x82</txt-record>", `<name replace-wildcards="yes">%h</name>`} {
		if !strings.Contains(svc, w) {
			t.Errorf("falta %q en\n%s", w, svc)
		}
	}
	// Sin carpetas de Time Machine no se anuncia _adisk.
	if s := AvahiSMBService(nil); strings.Contains(s, "_adisk") || !strings.Contains(s, "_smb._tcp") {
		t.Fatal(s)
	}
	// Los nombres se escapan para el XML.
	if s := AvahiSMBService([]string{"a&b"}); !strings.Contains(s, "adVN=a&amp;b") {
		t.Fatal(s)
	}
}
