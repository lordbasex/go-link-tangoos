package main

import (
	"net"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Validación de las ops del clúster (protocol/README.md §17). Corre antes de armar
// cualquier argv: lo que no pasa acá nunca llega al CLI de GlusterFS.

func badArgs(err error) *agentproto.Error {
	return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
}

func validGlusterAllowed(addrs []string) *agentproto.Error {
	if len(addrs) > 64 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "demasiados nodos permitidos")
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "dirección inválida en la lista de nodos: %q", a)
		}
	}
	return nil
}

func validateGlusterPeer(a *agentproto.GlusterPeerArgs) *agentproto.Error {
	if a.Action != "probe" && a.Action != "detach" {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "acción inválida: %q", a.Action)
	}
	if err := gluster.ValidHost(a.Host); err != nil {
		return badArgs(err)
	}
	return nil
}

func validateGlusterCreate(a *agentproto.GlusterVolumeCreateArgs) *agentproto.Error {
	if err := gluster.ValidateCreate(a.Name, a.Layout, a.Bricks); err != nil {
		return badArgs(err)
	}
	if err := gluster.ValidOptions(a.Options); err != nil {
		return badArgs(err)
	}
	return nil
}

func validateGlusterAction(a *agentproto.GlusterVolumeActionArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Name); err != nil {
		return badArgs(err)
	}
	if _, err := gluster.ArgsVolumeAction(a.Name, a.Action); err != nil {
		return badArgs(err)
	}
	return nil
}

func validateGlusterOptions(a *agentproto.GlusterVolumeOptionsArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Name); err != nil {
		return badArgs(err)
	}
	if len(a.Set)+len(a.Reset) == 0 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "no hay opciones para cambiar")
	}
	if err := gluster.ValidOptions(a.Set); err != nil {
		return badArgs(err)
	}
	for _, k := range a.Reset {
		if _, set := a.Set[k]; set {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "opción repetida: %q", k)
		}
		// Solo claves de la allow-list; las globales no se resetean por volumen.
		if !knownOption(k) || gluster.IsGlobal(k) {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "opción no permitida: %q", k)
		}
	}
	return nil
}

func knownOption(k string) bool {
	for _, o := range gluster.Options() {
		if o.Key == k {
			return true
		}
	}
	return false
}

func validateGlusterHeal(a *agentproto.GlusterHealArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Name); err != nil {
		return badArgs(err)
	}
	return nil
}

func validateGlusterBricks(a *agentproto.GlusterBricksArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Name); err != nil {
		return badArgs(err)
	}
	if a.Replica < 0 || a.Replica > gluster.MaxReplica {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "réplica entre 1 y %d", gluster.MaxReplica)
	}
	switch a.Op {
	case "add", "remove":
		if len(a.Bricks) == 0 || len(a.Bricks) > gluster.MaxBricks {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "se esperan entre 1 y %d bricks", gluster.MaxBricks)
		}
		for _, b := range a.Bricks {
			if err := gluster.ValidBrick(b, a.Name); err != nil {
				return badArgs(err)
			}
		}
		if a.Op == "remove" {
			if _, err := gluster.ArgsRemoveBrick(a.Name, a.Replica, a.Bricks, a.Phase); err != nil {
				return badArgs(err)
			}
		}
	case "replace":
		if err := gluster.ValidBrick(a.From, a.Name); err != nil {
			return badArgs(err)
		}
		if err := gluster.ValidBrick(a.To, a.Name); err != nil {
			return badArgs(err)
		}
		if a.From == a.To {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "el brick nuevo es igual al viejo")
		}
	default:
		return agentproto.Errf(agentproto.CodeInvalidArgs, "operación inválida: %q", a.Op)
	}
	return nil
}

func validateGlusterMount(a *agentproto.GlusterMountArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Name); err != nil {
		return badArgs(err)
	}
	switch a.Action {
	case "":
	case "stop", "start", "acl":
		if a.Share != "" || len(a.Backups)+len(a.RW)+len(a.RO) > 0 {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "stop/start/acl no cambian la configuración")
		}
		return nil
	default:
		return agentproto.Errf(agentproto.CodeInvalidArgs, "acción inválida: %q", a.Action)
	}
	if len(a.Backups) > 16 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "demasiados servidores de respaldo")
	}
	for _, h := range a.Backups {
		if err := gluster.ValidHost(h); err != nil {
			return badArgs(err)
		}
	}
	if a.Share == "" {
		if len(a.RW)+len(a.RO) > 0 {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "permisos sin carpeta compartida")
		}
		return nil
	}
	if !a.Enabled {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "para compartirlo por SMB el volumen tiene que estar montado")
	}
	if err := diskops.ValidShareName(a.Share); err != nil {
		return badArgs(err)
	}
	if len(a.Comment) > 256 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "descripción demasiado larga")
	}
	seen := map[string]bool{}
	for _, u := range append(append([]string{}, a.RW...), a.RO...) {
		if err := diskops.ValidUserName(u); err != nil || seen[u] {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "usuario inválido o repetido: %q", u)
		}
		seen[u] = true
	}
	return nil
}

func validateGlusterBrickClean(a *agentproto.GlusterBrickCleanArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Name); err != nil {
		return badArgs(err)
	}
	if err := diskops.ValidMount("/" + a.TangoVolume); err != nil {
		return badArgs(err)
	}
	return nil
}
