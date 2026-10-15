package main

import (
	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Validaciones de las ops de grupos de almacenamiento (protocol/README.md §16): corren
// antes de tocar nada, independientes de las de tangod (docs/07 §5).

func invalid(format string, a ...any) *agentproto.Error {
	return agentproto.Errf(agentproto.CodeInvalidArgs, format, a...)
}

func validDisks(ds []agentproto.VolumeDisk, minN, maxN int) *agentproto.Error {
	if len(ds) < minN || len(ds) > maxN {
		return invalid("se esperan entre %d y %d discos", minN, maxN)
	}
	seen := map[string]bool{}
	for _, d := range ds {
		if !validVolumeDisk(d) || seen[d.DevID] {
			return invalid("disco inválido o repetido")
		}
		seen[d.DevID] = true
	}
	return nil
}

func validatePoolCreate(a *agentproto.PoolCreateArgs) *agentproto.Error {
	if err := diskops.ValidPoolName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if err := diskops.CheckPoolDisks(a.Profile, len(a.Disks)); err != nil {
		return invalid("%v", err)
	}
	if e := validDisks(a.Disks, 1, 16); e != nil {
		return e
	}
	if !opIDRe.MatchString(a.OpID) {
		return invalid("op_id inválido")
	}
	return nil
}

func validatePoolExpand(a *agentproto.PoolExpandArgs) *agentproto.Error {
	if err := diskops.ValidPoolName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if e := validDisks(a.Add, 1, 8); e != nil {
		return e
	}
	if !opIDRe.MatchString(a.OpID) {
		return invalid("op_id inválido")
	}
	return nil
}

func validatePoolReplace(a *agentproto.PoolReplaceArgs) *agentproto.Error {
	if err := diskops.ValidPoolName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if a.Failed != "" && !devIDRe.MatchString(a.Failed) {
		return invalid("disco fallado inválido")
	}
	if !validVolumeDisk(a.Replacement) || a.Replacement.DevID == a.Failed {
		return invalid("disco de reemplazo inválido")
	}
	if !opIDRe.MatchString(a.OpID) {
		return invalid("op_id inválido")
	}
	return nil
}

func validatePoolDelete(a *agentproto.PoolDeleteArgs) *agentproto.Error {
	if err := diskops.ValidPoolName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if len(a.DevIDs) == 0 || len(a.DevIDs) > 18 {
		return invalid("se esperan entre 1 y 18 discos")
	}
	seen := map[string]bool{}
	for _, d := range a.DevIDs {
		if !devIDRe.MatchString(d) || seen[d] {
			return invalid("disco inválido o repetido")
		}
		seen[d] = true
	}
	if !opIDRe.MatchString(a.OpID) {
		return invalid("op_id inválido")
	}
	return nil
}

func validateLVCreate(a *agentproto.LVCreateArgs) *agentproto.Error {
	if err := diskops.ValidPoolName(a.Pool); err != nil {
		return invalid("%v", err)
	}
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if !diskops.PoolFS[a.FS] {
		return invalid("filesystem no soportado (btrfs, xfs o ext4)")
	}
	if a.Size != 0 && a.Size < 256<<20 {
		return invalid("un volumen tiene al menos 256 MB")
	}
	if a.Passphrase != "" {
		if err := diskops.ValidPassphrase(a.Passphrase); err != nil {
			return invalid("%v", err)
		}
	}
	if a.KeyDevice != "" {
		if a.Passphrase == "" {
			return invalid("el archivo de clave solo sirve para volúmenes cifrados")
		}
		if err := diskops.ValidKeyDeviceUUID(a.KeyDevice); err != nil {
			return invalid("%v", err)
		}
	}
	if !opIDRe.MatchString(a.OpID) {
		return invalid("op_id inválido")
	}
	return nil
}

func validateLVResize(a *agentproto.LVResizeArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if a.Size < 256<<20 || a.Size > 1<<60 {
		return invalid("tamaño inválido")
	}
	if !opIDRe.MatchString(a.OpID) {
		return invalid("op_id inválido")
	}
	return nil
}

func validateLVDelete(a *agentproto.LVDeleteArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if a.UUID == "" || len(a.UUID) > 64 || !devIDRe.MatchString(a.UUID) {
		return invalid("UUID de volumen inválido")
	}
	for _, sh := range a.Shares {
		if err := diskops.ValidShareName(sh); err != nil {
			return invalid("%v", err)
		}
	}
	if !opIDRe.MatchString(a.OpID) {
		return invalid("op_id inválido")
	}
	return nil
}

func validateLUKSUnlock(a *agentproto.LUKSUnlockArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if a.UseKeyDevice == (a.Passphrase != "") {
		return invalid("se desbloquea con la frase o con el dispositivo de clave (uno de los dos)")
	}
	if a.Passphrase != "" && len(a.Passphrase) > 512 {
		return invalid("frase demasiado larga")
	}
	return nil
}

func validateLUKSLock(a *agentproto.LUKSLockArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return invalid("%v", err)
	}
	return nil
}

func validateLUKSPassphrase(a *agentproto.LUKSPassphraseArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if a.Old == "" || len(a.Old) > 512 {
		return invalid("falta la frase actual")
	}
	if err := diskops.ValidPassphrase(a.New); err != nil {
		return invalid("%v", err)
	}
	if a.Old == a.New {
		return invalid("la frase nueva es igual a la actual")
	}
	return nil
}

func validateLUKSKeyDevice(a *agentproto.LUKSKeyDeviceArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if a.Passphrase == "" || len(a.Passphrase) > 512 {
		return invalid("falta la frase del volumen")
	}
	if a.Device != "" {
		if err := diskops.ValidKeyDeviceUUID(a.Device); err != nil {
			return invalid("%v", err)
		}
	}
	return nil
}

func validateCacheCreate(a *agentproto.CacheCreateArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return invalid("%v", err)
	}
	switch a.Mode {
	case "read":
		if e := validDisks(a.Disks, 1, 2); e != nil {
			return e
		}
	case "readwrite":
		if e := validDisks(a.Disks, 2, 2); e != nil {
			return invalid("la caché de lectura y escritura necesita exactamente 2 SSD (en RAID 1)")
		}
	default:
		return invalid("modo de caché inválido (read o readwrite)")
	}
	if !opIDRe.MatchString(a.OpID) {
		return invalid("op_id inválido")
	}
	return nil
}

func validateCacheRemove(a *agentproto.CacheRemoveArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return invalid("%v", err)
	}
	if !opIDRe.MatchString(a.OpID) {
		return invalid("op_id inválido")
	}
	return nil
}
