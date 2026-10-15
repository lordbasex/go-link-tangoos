//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// registerUsersV10 agrega user.delete (protocol/README.md §14).
func registerUsersV10(r *agentproto.Registry, log *slog.Logger) {
	agentproto.Register(r, agentproto.OpUserDelete, validateUserDelete, func(ctx context.Context, a *agentproto.UserDeleteArgs) (any, *agentproto.Error) {
		res := agentproto.UserDeleteResult{}
		if a.ReassignTo != "" {
			from, err := diskops.LookupTangoUID(a.Name)
			if err == nil {
				to, err := diskops.LookupTangoUID(a.ReassignTo)
				if err != nil {
					return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe el usuario del sistema %s", a.ReassignTo)
				}
				for _, m := range a.Mounts {
					root := filepath.Join(m, "@shares")
					if _, err := os.Stat(root); err != nil {
						continue
					}
					n, err := diskops.ReassignFiles(root, from, to)
					res.Reassigned += n
					if err != nil {
						log.Warn("user.delete reassign", "mount", m, "err", err)
						return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudieron pasar los archivos a %s", a.ReassignTo)
					}
				}
			}
		}
		existed, err := diskops.DeleteUser(ctx, a.Name)
		res.Existed = existed
		if err != nil {
			if errors.Is(err, diskops.ErrSystemUser) {
				return nil, agentproto.Errf("SYSTEM_USER", "%v", err)
			}
			log.Warn("user.delete", "user", a.Name, "err", err, "output", execOutput(err))
			return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo borrar el usuario del sistema")
		}
		log.Info("user deleted", "user", a.Name, "existed", existed, "reassigned", res.Reassigned)
		return res, nil
	})
}
