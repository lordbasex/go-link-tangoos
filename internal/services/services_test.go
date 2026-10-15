package services

import (
	"strings"
	"testing"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func TestRenderExports(t *testing.T) {
	out, err := RenderExports([]agentproto.NFSExport{
		{Path: "/volume2/@shares/Fotos", Clients: []agentproto.NFSClient{{Host: "192.168.1.0/24", Access: "ro"}}},
		{Path: "/volume1/@shares/Familia", Clients: []agentproto.NFSClient{{Host: "172.17.0.45", Access: "rw"}, {Host: "*.casa.lan", Access: "ro"}}},
		{Path: "/volume1/@shares/Vacia"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "/volume1/@shares/Familia 172.17.0.45(rw,sync,no_subtree_check,root_squash,sec=sys) *.casa.lan(ro,sync,no_subtree_check,root_squash,sec=sys)\n" +
		"/volume2/@shares/Fotos 192.168.1.0/24(ro,sync,no_subtree_check,root_squash,sec=sys)\n"
	if !strings.HasSuffix(out, want) || strings.Contains(out, "Vacia") {
		t.Fatalf("got:\n%s", out)
	}
	for _, bad := range []agentproto.NFSExport{
		{Path: "/etc", Clients: []agentproto.NFSClient{{Host: "*", Access: "rw"}}},
		{Path: "/volume1/@shares/../../etc", Clients: []agentproto.NFSClient{{Host: "*", Access: "rw"}}},
		{Path: "/volume1/@shares/A", Clients: []agentproto.NFSClient{{Host: "1.2.3.4/99", Access: "rw"}}},
		{Path: "/volume1/@shares/A", Clients: []agentproto.NFSClient{{Host: "a(rw,no_root_squash)", Access: "rw"}}},
		{Path: "/volume1/@shares/A", Clients: []agentproto.NFSClient{{Host: "x", Access: "all"}}},
	} {
		if _, err := RenderExports([]agentproto.NFSExport{bad}); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestISCSIPlan(t *testing.T) {
	tg := agentproto.ISCSITarget{Name: "vm-disk", IQN: IQN("vm-disk"), File: LUNFile("/volume1", "vm-disk"), SizeBytes: 1 << 30, CHAPUser: "pc", CHAPSecret: "secreto12345"}
	if err := ValidateTarget(tg); err != nil {
		t.Fatal(err)
	}
	steps := Plan(tg)
	joined := ""
	for _, s := range steps {
		joined += s.Path + "=" + s.Value + "→" + s.Symlink + "\n"
	}
	for _, want := range []string{
		"core/fileio_0/vm-disk/control=fd_dev_name=/volume1/@iscsi/vm-disk.img,fd_dev_size=1073741824",
		"iscsi/iqn.2026-10.local.tango:vm-disk/tpgt_1/auth/userid=pc",
		"tpgt_1/lun/lun_0/tango_lun=→/sys/kernel/config/target/core/fileio_0/vm-disk",
		"tpgt_1/np/0.0.0.0:3260=",
		"tpgt_1/enable=1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("plan misses %q:\n%s", want, joined)
		}
	}
	if steps[len(steps)-1].Path != "/sys/kernel/config/target/iscsi/iqn.2026-10.local.tango:vm-disk/tpgt_1/enable" {
		t.Fatal("enable must be last")
	}
	bad := tg
	bad.CHAPSecret = "corto"
	if ValidateTarget(bad) == nil {
		t.Fatal("short CHAP accepted")
	}
	bad = tg
	bad.File = "/etc/passwd"
	if ValidateTarget(bad) == nil {
		t.Fatal("bad file accepted")
	}
	if ValidTargetName("Mal Nombre") == nil {
		t.Fatal("bad name accepted")
	}
	if td := TeardownPaths(tg); !strings.HasSuffix(td[len(td)-1], "fileio_0/vm-disk") {
		t.Fatal("teardown order")
	}
}
