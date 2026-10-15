package main

import (
	"strings"
	"testing"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func TestValidateM21(t *testing.T) {
	fp := strings.Repeat("a", 64)
	disks := func(n int) []agentproto.VolumeDisk {
		var d []agentproto.VolumeDisk
		for i := range n {
			d = append(d, agentproto.VolumeDisk{DevID: "scsi-0QEMU_QEMU_HARDDISK_drive-scsi" + string(rune('5'+i)), Fingerprint: fp})
		}
		return d
	}
	if e := validateVolumeCreate(&agentproto.VolumeCreateArgs{OpID: "j_1", Name: "volume2", Profile: "raid5", Disks: disks(3)}); e != nil {
		t.Errorf("raid5 con 3: %v", e)
	}
	if e := validateVolumeCreate(&agentproto.VolumeCreateArgs{OpID: "j_1", Name: "volume2", Profile: "raid10", Disks: disks(3)}); e == nil {
		t.Error("raid10 con 3 tiene que fallar")
	}
	ok := agentproto.VolumeDeleteArgs{OpID: "j_2", Name: "volume2", DevIDs: []string{"scsi-a", "scsi-b"}, Shares: []string{"Fotos"}}
	if e := validateVolumeDelete(&ok); e != nil {
		t.Errorf("delete válido: %v", e)
	}
	for _, bad := range []agentproto.VolumeDeleteArgs{
		{OpID: "j_2", Name: "etc", DevIDs: []string{"scsi-a"}},
		{OpID: "j_2", Name: "volume2", DevIDs: []string{"../../sda"}},
		{OpID: "j_2", Name: "volume2", DevIDs: []string{"scsi-a", "scsi-a"}},
		{OpID: "j_2", Name: "volume2"},
		{OpID: "j 2", Name: "volume2", DevIDs: []string{"scsi-a"}},
		{OpID: "j_2", Name: "volume2", DevIDs: []string{"scsi-a"}, Shares: []string{"../x"}},
	} {
		if validateVolumeDelete(&bad) == nil {
			t.Errorf("tendría que fallar: %+v", bad)
		}
	}
	if e := validateSharePerms(&agentproto.SharePermsArgs{Mount: "/volume1", Share: "Familia", RW: []string{"admin"}, RO: []string{"sofia"}}); e != nil {
		t.Errorf("perms válidos: %v", e)
	}
	for _, bad := range []agentproto.SharePermsArgs{
		{Mount: "/etc", Share: "Familia"},
		{Mount: "/volume1", Share: "../x"},
		{Mount: "/volume1", Share: "Familia", RW: []string{"root"}},
		{Mount: "/volume1", Share: "Familia", RW: []string{"sofia"}, RO: []string{"sofia"}},
	} {
		if validateSharePerms(&bad) == nil {
			t.Errorf("tendría que fallar: %+v", bad)
		}
	}
	if validateUserPassword(&agentproto.UserPasswordArgs{Name: "sofia", Password: "corta"}) == nil {
		t.Error("contraseña corta")
	}
	if validateUserPassword(&agentproto.UserPasswordArgs{Name: "tango", Password: "suficientemente-larga"}) == nil {
		t.Error("usuario reservado")
	}
}
