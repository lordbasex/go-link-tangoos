package api

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/lordbasex/go-link-tangoos/internal/auth"
	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Usuarios de TangoOS (protocol/README.md §8): cada uno es un usuario del store de
// tangod (argon2id) y, del lado del sistema, un usuario Unix sin login con cuenta Samba.

const codePermissionDenied = "PERMISSION_DENIED"

var userColors = []string{"#EC4899", "#22C55E", "#F97316", "#8B5CF6", "#0EA5E9", "#EAB308", "#2F7CF6", "#8E8E96"}

// requireAdmin exige que la sesión sea de un administrador.
func (a *API) requireAdmin(s *rtcrpc.Session) error {
	u, ok := a.d.Auth.Get(s.User())
	if !ok || !u.Admin {
		return rtcrpc.Errorf(codePermissionDenied, "esta acción es solo para administradores")
	}
	return nil
}

func validPassword(pw string) error {
	if utf8.RuneCountInString(pw) < 10 || len(pw) > 256 {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la contraseña tiene que tener al menos 10 caracteres")
	}
	if strings.ContainsAny(pw, "\n\r\x00") {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la contraseña no puede tener saltos de línea")
	}
	return nil
}

func (a *API) registerUsers(r *rtcrpc.Router) {
	type createP struct {
		Name     string   `json:"name"`
		Full     string   `json:"full"`
		Email    string   `json:"email"`
		Password string   `json:"password"`
		Admin    bool     `json:"admin"`
		Groups   []string `json:"groups"`
	}
	type passwordP struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}

	r.Handle("users.create", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p createP) (auth.User, error) {
		if err := a.requireAdmin(s); err != nil {
			return auth.User{}, err
		}
		if err := diskops.ValidUserName(p.Name); err != nil {
			return auth.User{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
		if _, exists := a.d.Auth.Get(p.Name); exists {
			return auth.User{}, rtcrpc.Errorf("USER_EXISTS", "ya existe el usuario %s", p.Name)
		}
		p.Full = strings.TrimSpace(p.Full)
		if p.Full == "" || utf8.RuneCountInString(p.Full) > 64 || strings.ContainsAny(p.Full, ":\n\r") {
			return auth.User{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "el nombre completo es obligatorio (máx. 64, sin «:»)")
		}
		if p.Email != "" {
			if _, err := mail.ParseAddress(p.Email); err != nil {
				return auth.User{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "el correo no es válido")
			}
		}
		if err := validPassword(p.Password); err != nil {
			return auth.User{}, err
		}
		groups := []string{}
		for _, g := range p.Groups {
			if g == "" || len(g) > 32 || strings.ContainsAny(g, " :,") {
				return auth.User{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "grupo inválido %q", g)
			}
			if g != "administrators" {
				groups = appendUnique(groups, g)
			}
		}
		if p.Admin {
			groups = appendUnique(groups, "administrators")
		}
		// Primero el sistema (si falla no queda un usuario a medias en TangoOS).
		if a.storage != nil {
			if err := a.storage.agent.UserPassword(ctx, p.Name, p.Full, p.Password); err != nil {
				var pe *agentproto.Error
				if !errors.As(err, &pe) || pe.Code != agentproto.CodeNotInstalled {
					return auth.User{}, agentErr(err)
				}
				// Sin Samba: alcanza con el usuario Unix.
				if err := a.storage.agent.UserEnsure(ctx, p.Name, p.Full); err != nil {
					return auth.User{}, agentErr(err)
				}
			}
		}
		u := auth.User{Name: p.Name, Full: p.Full, Email: p.Email, Groups: groups, Admin: p.Admin,
			Color: userColors[len(a.d.Auth.List())%len(userColors)]}
		if err := a.d.Auth.Add(u, p.Password); err != nil {
			return auth.User{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "%v", err)
		}
		a.d.Log.Info("user created", "user", p.Name, "by", s.User(), "admin", p.Admin)
		a.d.Hub.Publish("shares.changed", map[string]string{})
		created, _ := a.d.Auth.Get(p.Name)
		return created, nil
	}))

	a.registerUsersV10(r) // users.delete y contraseñas de app (users_v10.go)

	r.Handle("users.setPassword", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p passwordP) (struct{}, error) {
		if p.Name != s.User() {
			if err := a.requireAdmin(s); err != nil {
				return struct{}{}, err
			}
		}
		u, ok := a.d.Auth.Get(p.Name)
		if !ok {
			return struct{}{}, rtcrpc.Errorf("USER_NOT_FOUND", "no existe el usuario %s", p.Name)
		}
		if err := validPassword(p.Password); err != nil {
			return struct{}{}, err
		}
		if a.storage != nil && diskops.ValidUserName(u.Name) == nil {
			if err := a.storage.agent.UserPassword(ctx, u.Name, u.Full, p.Password); err != nil {
				var pe *agentproto.Error
				if !errors.As(err, &pe) || pe.Code != agentproto.CodeNotInstalled {
					return struct{}{}, agentErr(err)
				}
			}
		}
		keep := ""
		if p.Name == s.User() {
			keep = s.Token()
		}
		if err := a.d.Auth.SetPassword(u.Name, p.Password, keep); err != nil {
			return struct{}{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
		}
		a.d.Log.Info("password changed", "user", u.Name, "by", s.User())
		return struct{}{}, nil
	}))
}
