package main

import (
	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func validateUserDelete(a *agentproto.UserDeleteArgs) *agentproto.Error {
	if err := diskops.ValidUserName(a.Name); err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
	}
	if a.ReassignTo != "" {
		if err := diskops.ValidUserName(a.ReassignTo); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
		if a.ReassignTo == a.Name {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "no se pueden pasar los archivos al mismo usuario")
		}
	}
	if len(a.Mounts) > 32 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "demasiados volúmenes")
	}
	for _, m := range a.Mounts {
		if err := diskops.ValidMount(m); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "%v", err)
		}
	}
	return nil
}
