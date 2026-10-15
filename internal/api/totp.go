package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/lordbasex/go-link-tangoos/internal/auth"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Verificación en dos pasos (protocol/README.md §13).

const (
	totpPendingKey  = "totp.pending"
	totpPendingTTL  = 5 * time.Minute
	totpMaxFailures = 5
)

// totpPending es el login a medio camino (contraseña bien, falta el código), guardado
// en la sesión WebRTC: el código solo vale en la misma conexión.
type totpPending struct {
	user  string
	exp   time.Time
	fails int
}

// loginResult es la respuesta de auth.login/auth.totp/auth.resume.
type loginResult struct {
	User  *auth.User `json:"user,omitempty"`
	Token string     `json:"token,omitempty"`
	Needs string     `json:"needs,omitempty"`
}

// qrSVG arma el QR del URI otpauth como SVG (data URL), sin imágenes externas.
func qrSVG(text string) (string, error) {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return "", err
	}
	q.DisableBorder = false
	bm := q.Bitmap()
	n := len(bm)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n, n, n)
	for y, row := range bm {
		for x, on := range row {
			if on {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(b.String())), nil
}

func (a *API) registerTOTP(r *rtcrpc.Router) {
	d := a.d
	type codeP struct {
		Code string `json:"code"`
	}
	type passP struct {
		Password string `json:"password"`
	}
	type nameP struct {
		Name string `json:"name"`
	}

	r.Handle("auth.totp", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p codeP) (loginResult, error) {
		pend, _ := s.Get(totpPendingKey).(*totpPending)
		if pend == nil || time.Now().After(pend.exp) {
			s.Set(totpPendingKey, nil)
			return loginResult{}, rtcrpc.Errorf("TOTP_NOT_PENDING", "volvé a ingresar tu contraseña")
		}
		u, tok, err := d.Auth.CompleteTOTP(pend.user, p.Code)
		if err != nil {
			pend.fails++
			if pend.fails >= totpMaxFailures {
				s.Set(totpPendingKey, nil)
				d.Log.Warn("totp locked", "user", pend.user, "ip", s.RemoteIP)
				return loginResult{}, rtcrpc.Errorf("TOTP_LOCKED", "demasiados códigos incorrectos: volvé a ingresar tu contraseña")
			}
			return loginResult{}, rtcrpc.Errorf("INVALID_CODE", "el código no es correcto o ya venció")
		}
		s.Set(totpPendingKey, nil)
		s.Authenticate(u.Name, tok)
		d.Log.Info("login", "user", u.Name, "ip", s.RemoteIP, "client", s.Client, "2fa", true)
		return loginResult{User: &u, Token: tok}, nil
	}), rtcrpc.Public())

	r.Handle("users.totp.begin", func(_ context.Context, s *rtcrpc.Session, _ json.RawMessage) (any, error) {
		secret, uri, err := d.Auth.TOTPBegin(s.User())
		if err != nil {
			return nil, err
		}
		qr, err := qrSVG(uri)
		if err != nil {
			return nil, err
		}
		return map[string]string{"secret": secret, "uri": uri, "qr": qr}, nil
	})
	r.Handle("users.totp.confirm", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p codeP) (map[string][]string, error) {
		codes, err := d.Auth.TOTPConfirm(s.User(), p.Code)
		switch {
		case errors.Is(err, auth.ErrNoPending):
			return nil, rtcrpc.Errorf("TOTP_NOT_PENDING", "%s", err.Error())
		case errors.Is(err, auth.ErrBadCode):
			return nil, rtcrpc.Errorf("INVALID_CODE", "el código no coincide: revisá la hora del teléfono")
		case err != nil:
			return nil, err
		}
		return map[string][]string{"recoveryCodes": codes}, nil
	}))
	r.Handle("users.totp.disable", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p passP) (struct{}, error) {
		if err := d.Auth.TOTPDisable(s.User(), p.Password); err != nil {
			return struct{}{}, rtcrpc.Errorf(rtcrpc.CodeInvalidCredentials, "la contraseña no es correcta")
		}
		return struct{}{}, nil
	}))
	r.Handle("users.totp.reset", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p nameP) (struct{}, error) {
		if me, ok := d.Auth.Get(s.User()); !ok || !me.Admin {
			return struct{}{}, rtcrpc.Errorf("PERMISSION_DENIED", "solo un administrador puede desactivar la verificación de otro usuario")
		}
		if err := d.Auth.TOTPReset(p.Name); err != nil {
			return struct{}{}, rtcrpc.Errorf("USER_NOT_FOUND", "%s", err.Error())
		}
		return struct{}{}, nil
	}))
}
