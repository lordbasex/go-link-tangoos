package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/auth"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Cierre v1.0 (protocol/README.md §14): borrar usuarios y contraseñas de app.

// sambaRev: 2 = secciones con vfs_shadow_copy2; 3 = snapdir en los enlaces .gmt
// (Versiones anteriores de Windows, protocol/README.md §14).
const sambaRev = 3

// sambaMigrateDelay da tiempo a que el agente y los volúmenes estén listos al arrancar.
var sambaMigrateDelay = 20 * time.Second

type userDeleter interface {
	UserDelete(ctx context.Context, a agentproto.UserDeleteArgs) (*agentproto.UserDeleteResult, error)
}

// reapplyAllPerms vuelve a aplicar permisos y sección de Samba de todas las carpetas.
func (s *storageAPI) reapplyAllPerms(ctx context.Context) (int, error) {
	n := 0
	var firstErr error
	for _, sh := range s.shares(ctx) {
		s.mu.Lock()
		entries := append([]PermEntry{}, s.m.Perms[sh.ID]...)
		_, has := s.m.Perms[sh.ID]
		s.mu.Unlock()
		if !has {
			entries = defaultPerms()
		}
		mount := strings.TrimSuffix(sh.Path, "/@shares/"+sh.Name)
		if err := s.applyPerms(ctx, sh, mount, entries); err != nil {
			s.a.d.Log.Warn("reapply perms", "share", sh.Name, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		n++
	}
	return n, firstErr
}

// migrateSamba regenera una vez las secciones de las carpetas existentes cuando cambió
// el formato (por ejemplo, para sumar las Versiones anteriores).
func (s *storageAPI) migrateSamba() {
	s.mu.Lock()
	need := s.m.SambaRev < sambaRev
	s.mu.Unlock()
	if !need || s.agent == nil {
		return
	}
	time.Sleep(sambaMigrateDelay)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	n, err := s.reapplyAllPerms(ctx)
	if err != nil {
		s.a.d.Log.Warn("samba migration incomplete; will retry on next start", "applied", n, "err", err)
		return
	}
	s.mu.Lock()
	s.m.SambaRev = sambaRev
	s.save()
	s.mu.Unlock()
	s.a.d.Log.Info("samba sections regenerated", "shares", n, "rev", sambaRev)
}

// mounts devuelve los puntos de montaje de los volúmenes con carpetas compartidas.
func (s *storageAPI) mounts(ctx context.Context) []string {
	seen := map[string]bool{}
	var out []string
	for _, sh := range s.shares(ctx) {
		m := strings.TrimSuffix(sh.Path, "/@shares/"+sh.Name)
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// dropUserPerms saca las entradas de un usuario de los permisos de todas las carpetas.
func (s *storageAPI) dropUserPerms(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for id, entries := range s.m.Perms {
		kept := entries[:0:0]
		for _, e := range entries {
			if e.Kind == "user" && e.Name == name {
				changed = true
				continue
			}
			kept = append(kept, e)
		}
		s.m.Perms[id] = kept
	}
	if changed {
		s.save()
	}
}

func (a *API) registerUsersV10(r *rtcrpc.Router) {
	type deleteP struct {
		Name      string `json:"name"`
		TypedName string `json:"typed_name"`
		KeepFiles *bool  `json:"keepFiles"`
	}
	r.Handle("users.delete", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p deleteP) (struct{}, error) {
		if err := a.requireAdmin(s); err != nil {
			return struct{}{}, err
		}
		u, ok := a.d.Auth.Get(p.Name)
		if !ok {
			return struct{}{}, rtcrpc.Errorf("USER_NOT_FOUND", "no existe el usuario %s", p.Name)
		}
		if p.TypedName != p.Name {
			return struct{}{}, rtcrpc.Errorf("CONFIRMATION_MISMATCH", "escribí el nombre del usuario para confirmar")
		}
		if p.Name == s.User() {
			return struct{}{}, rtcrpc.Errorf("CANNOT_DELETE_SELF", "no podés borrar tu propio usuario")
		}
		if u.Admin && a.d.Auth.AdminCount() <= 1 {
			return struct{}{}, rtcrpc.Errorf("LAST_ADMIN", "no se puede borrar el último administrador")
		}
		keep := p.KeepFiles == nil || *p.KeepFiles
		if a.storage != nil {
			if del, ok := a.storage.agent.(userDeleter); ok {
				args := agentproto.UserDeleteArgs{Name: p.Name}
				if !keep {
					args.ReassignTo = s.User()
					args.Mounts = a.storage.mounts(ctx)
				}
				if _, err := del.UserDelete(ctx, args); err != nil {
					var pe *agentproto.Error
					if errors.As(err, &pe) && pe.Code == agentproto.CodeNotFound && !keep {
						return struct{}{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "tu usuario no tiene cuenta en el sistema para recibir los archivos: borrá conservando los archivos")
					}
					return struct{}{}, agentErr(err)
				}
			}
		}
		if err := a.d.Auth.Delete(p.Name); err != nil {
			return struct{}{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
		}
		a.d.Hub.RevokeUser(p.Name)
		if a.storage != nil {
			a.storage.dropUserPerms(p.Name)
			if _, err := a.storage.reapplyAllPerms(ctx); err != nil {
				a.d.Log.Warn("users.delete: reapply perms", "err", err)
			}
		}
		if a.files != nil {
			a.files.InvalidateWorkers()
		}
		a.d.Log.Info("user deleted", "user", p.Name, "by", s.User(), "keepFiles", keep)
		a.d.Hub.Publish("users.changed", map[string]string{"user": p.Name})
		a.d.Hub.Publish("shares.changed", map[string]string{})
		return struct{}{}, nil
	}))

	type listP struct {
		Name string `json:"name"`
	}
	target := func(s *rtcrpc.Session, name string) (string, error) {
		if name == "" || name == s.User() {
			return s.User(), nil
		}
		if err := a.requireAdmin(s); err != nil {
			return "", err
		}
		if _, ok := a.d.Auth.Get(name); !ok {
			return "", rtcrpc.Errorf("USER_NOT_FOUND", "no existe el usuario %s", name)
		}
		return name, nil
	}
	r.Handle("users.appPasswords.list", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p listP) ([]auth.AppPasswordInfo, error) {
		name, err := target(s, p.Name)
		if err != nil {
			return nil, err
		}
		return a.d.Auth.AppPasswords(name)
	}))

	type createP struct {
		Label string `json:"label"`
		SMB   bool   `json:"smb"`
	}
	type createR struct {
		auth.AppPasswordInfo
		Password string `json:"password"`
	}
	r.Handle("users.appPasswords.create", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p createP) (createR, error) {
		info, pw, err := a.d.Auth.CreateAppPassword(s.User(), p.Label, p.SMB)
		switch {
		case errors.Is(err, auth.ErrTooManyApp):
			return createR{}, rtcrpc.Errorf("TOO_MANY", "ya tenés %d contraseñas de app: revocá alguna", auth.MaxAppPasswords)
		case err != nil:
			return createR{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
		if p.SMB {
			if err := a.setSMBPassword(ctx, s.User(), pw); err != nil {
				_, _ = a.d.Auth.RevokeAppPassword(s.User(), info.ID)
				return createR{}, err
			}
		}
		return createR{AppPasswordInfo: info, Password: pw}, nil
	}))

	type revokeP struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	r.Handle("users.appPasswords.revoke", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p revokeP) (struct{}, error) {
		name, err := target(s, p.Name)
		if err != nil {
			return struct{}{}, err
		}
		wasSMB, err := a.d.Auth.RevokeAppPassword(name, p.ID)
		if errors.Is(err, auth.ErrAppPwNotFound) {
			return struct{}{}, rtcrpc.Errorf("NOT_FOUND", "no existe esa contraseña de app")
		}
		if err != nil {
			return struct{}{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
		}
		if wasSMB {
			// SMB queda cerrado hasta crear otra contraseña de app para SMB o cambiar la de la cuenta.
			if err := a.setSMBPassword(ctx, name, auth.RandomSecret()); err != nil {
				a.d.Log.Warn("revoke app password: smb", "user", name, "err", err)
			}
		}
		return struct{}{}, nil
	}))
}

// setSMBPassword cambia solo la contraseña de Samba del usuario.
func (a *API) setSMBPassword(ctx context.Context, name, pw string) error {
	if a.storage == nil {
		return nil
	}
	u, ok := a.d.Auth.Get(name)
	if !ok {
		return rtcrpc.Errorf("USER_NOT_FOUND", "no existe el usuario %s", name)
	}
	if err := a.storage.agent.UserPassword(ctx, name, u.Full, pw); err != nil {
		var pe *agentproto.Error
		if errors.As(err, &pe) && pe.Code == agentproto.CodeNotInstalled {
			return rtcrpc.Errorf(agentproto.CodeNotInstalled, "Samba no está instalado")
		}
		return agentErr(err)
	}
	return nil
}
