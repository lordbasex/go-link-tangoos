package main

import (
	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Validaciones de la fase M2.2 (protocol/README.md §11): corren antes de tocar nada,
// independientes de las de tangod.

func validVolumeDisk(d agentproto.VolumeDisk) bool {
	return devIDRe.MatchString(d.DevID) && len(d.Fingerprint) == 64
}

func validateMDReplace(a *agentproto.MDReplaceArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if a.Failed != "" && !devIDRe.MatchString(a.Failed) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "disco fallado inválido")
	}
	if !validVolumeDisk(a.Replacement) || a.Replacement.DevID == a.Failed {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "disco de reemplazo inválido")
	}
	if !opIDRe.MatchString(a.OpID) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "op_id inválido")
	}
	return nil
}

func validateMDGrow(a *agentproto.MDGrowArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if a.Mount != "/"+a.Name {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "el punto de montaje tiene que ser /%s", a.Name)
	}
	if len(a.Add) == 0 || len(a.Add) > 8 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "se agregan entre 1 y 8 discos")
	}
	seen := map[string]bool{}
	for _, d := range a.Add {
		if !validVolumeDisk(d) || seen[d.DevID] {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "disco inválido o repetido")
		}
		seen[d.DevID] = true
	}
	if a.RaidDevices < 4 || a.RaidDevices > 16 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "un RAID ampliado tiene entre 4 y 16 discos")
	}
	if !opIDRe.MatchString(a.OpID) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "op_id inválido")
	}
	return nil
}

func validateSnapshotDelete(a *agentproto.SnapshotDeleteArgs) *agentproto.Error {
	if err := diskops.ValidMount(a.Mount); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if err := diskops.ValidShareName(a.Share); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if err := diskops.ValidSnapshotName(a.Name); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	return nil
}

func validateDiskLocate(a *agentproto.DiskLocateArgs) *agentproto.Error {
	if !devIDRe.MatchString(a.DevID) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "disco inválido")
	}
	return nil
}
