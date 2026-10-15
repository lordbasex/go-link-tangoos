package main

import (
	"regexp"
	"strings"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// devIDRe es el mismo formato de nombre de /dev/disk/by-id que acepta smart.Paths.
var devIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+@-]{0,190}$`)

// Validaciones de argumentos de las ops de almacenamiento: corren antes de tocar nada,
// independientes de las que ya hizo tangod (docs/07 §5).

func validateVolumeCreate(a *agentproto.VolumeCreateArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if err := diskops.CheckProfileDisks(a.Profile, len(a.Disks)); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	seen := map[string]bool{}
	for _, d := range a.Disks {
		if d.DevID == "" || len(d.Fingerprint) != 64 || seen[d.DevID] {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "disco inválido o repetido")
		}
		seen[d.DevID] = true
	}
	if !opIDRe.MatchString(a.OpID) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "op_id inválido")
	}
	return nil
}

func validateSamba(a *agentproto.SambaShareArgs) *agentproto.Error {
	if err := diskops.ValidShareName(a.Name); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	mount, rest, ok := strings.Cut(strings.TrimPrefix(a.Path, "/"), "/")
	if !ok || diskops.ValidMount("/"+mount) != nil || rest != "@shares/"+a.Name {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "la ruta tiene que ser /<volumen>/@shares/%s", a.Name)
	}
	if len(a.Comment) > 256 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "descripción demasiado larga")
	}
	return nil
}

func validateVolumeDelete(a *agentproto.VolumeDeleteArgs) *agentproto.Error {
	if err := diskops.ValidVolumeName(a.Name); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if len(a.DevIDs) == 0 || len(a.DevIDs) > 16 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "se esperan entre 1 y 16 discos")
	}
	seen := map[string]bool{}
	for _, d := range a.DevIDs {
		if !devIDRe.MatchString(d) || seen[d] {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "disco inválido o repetido")
		}
		seen[d] = true
	}
	for _, sh := range a.Shares {
		if err := diskops.ValidShareName(sh); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
	}
	if !opIDRe.MatchString(a.OpID) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "op_id inválido")
	}
	return nil
}

func validateShareDelete(a *agentproto.ShareDeleteArgs) *agentproto.Error {
	if err := diskops.ValidMount(a.Mount); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if err := diskops.ValidShareName(a.Share); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	return nil
}

func validateSharePerms(a *agentproto.SharePermsArgs) *agentproto.Error {
	if e := validateShareDelete(&agentproto.ShareDeleteArgs{Mount: a.Mount, Share: a.Share}); e != nil {
		return e
	}
	if len(a.Comment) > 256 || len(a.RW)+len(a.RO) > 256 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "demasiados datos")
	}
	if a.TMMaxBytes < 0 || a.TMMaxBytes > 1<<60 || (a.TMMaxBytes > 0 && !a.TimeMachine) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "tope de Time Machine inválido")
	}
	seen := map[string]bool{}
	for _, u := range append(append([]string{}, a.RW...), a.RO...) {
		if err := diskops.ValidUserName(u); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%s: %v", u, err)
		}
		if seen[u] {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%s aparece dos veces", u)
		}
		seen[u] = true
	}
	return nil
}

func validateUserEnsure(a *agentproto.UserEnsureArgs) *agentproto.Error {
	if err := diskops.ValidUserName(a.Name); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if len(a.Full) > 128 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "nombre completo demasiado largo")
	}
	return nil
}

func validateUserPassword(a *agentproto.UserPasswordArgs) *agentproto.Error {
	if e := validateUserEnsure(&agentproto.UserEnsureArgs{Name: a.Name, Full: a.Full}); e != nil {
		return e
	}
	if len(a.Password) < 10 || len(a.Password) > 256 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "la contraseña tiene que tener entre 10 y 256 caracteres")
	}
	return nil
}
