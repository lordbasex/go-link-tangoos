package main

import (
	"strings"
	"testing"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func TestValidateM22(t *testing.T) {
	fp := strings.Repeat("a", 64)
	ok := agentproto.MDReplaceArgs{OpID: "j_1", Name: "volume2", Failed: "scsi-x", Replacement: agentproto.VolumeDisk{DevID: "scsi-y", Fingerprint: fp}}
	if e := validateMDReplace(&ok); e != nil {
		t.Fatal(e)
	}
	bad := ok
	bad.Replacement.DevID = "scsi-x" // reemplazar un disco por sí mismo
	if validateMDReplace(&bad) == nil {
		t.Fatal("mismo disco")
	}
	bad = ok
	bad.Failed = "../sda"
	if validateMDReplace(&bad) == nil {
		t.Fatal("dev_id")
	}
	bad = ok
	bad.Name = "etc"
	if validateMDReplace(&bad) == nil {
		t.Fatal("nombre reservado")
	}

	g := agentproto.MDGrowArgs{OpID: "j_2", Name: "volume2", Mount: "/volume2", RaidDevices: 4, Add: []agentproto.VolumeDisk{{DevID: "scsi-z", Fingerprint: fp}}}
	if e := validateMDGrow(&g); e != nil {
		t.Fatal(e)
	}
	bg := g
	bg.Mount = "/volume1"
	if validateMDGrow(&bg) == nil {
		t.Fatal("montaje de otro volumen")
	}
	bg = g
	bg.Add = append(bg.Add, bg.Add[0])
	if validateMDGrow(&bg) == nil {
		t.Fatal("repetido")
	}
	bg = g
	bg.Add = nil
	if validateMDGrow(&bg) == nil {
		t.Fatal("sin discos")
	}

	sd := agentproto.SnapshotDeleteArgs{Mount: "/volume2", Share: "Fotos", Name: "2026-10-14T15-00-00Z.hourly"}
	if e := validateSnapshotDelete(&sd); e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"../../etc", "2026-10-14T15-00-00Z.monthly", "@shares"} {
		b := sd
		b.Name = n
		if validateSnapshotDelete(&b) == nil {
			t.Fatalf("nombre %q", n)
		}
	}
	if validateDiskLocate(&agentproto.DiskLocateArgs{DevID: "a/b"}) == nil {
		t.Fatal("locate")
	}
}
