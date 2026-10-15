package api

import (
	"context"
	"fmt"
	"net/url"

	"github.com/lordbasex/go-link-tangoos/internal/appproxy"
	"github.com/lordbasex/go-link-tangoos/internal/auth"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// apps.ticket (protocol/README.md §12): URL de un solo uso que abre la app y le deja al
// navegador la cookie tango_app que exige el proxy.
func (m *m4API) registerTicket(r *rtcrpc.Router) {
	type idP struct {
		ID string `json:"id"`
	}
	r.Handle("apps.ticket", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p idP) (map[string]string, error) {
		port := appproxy.Default.Ports()[p.ID]
		if port == 0 {
			return nil, rtcrpc.Errorf("APP_NOT_FOUND", "la app no está instalada o no tiene interfaz web")
		}
		host, _ := s.Get("host").(string)
		if host == "" {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInternal, "no se sabe por qué dirección te conectaste")
		}
		base := fmt.Sprintf("https://%s:%d", host, port)
		if appproxy.DefaultGate == nil {
			return map[string]string{"url": base + "/"}, nil
		}
		t := appproxy.DefaultGate.Ticket(p.ID, auth.SessionID(s.Token()))
		return map[string]string{"url": base + appproxy.TicketPath + "?t=" + url.QueryEscape(t) + "&next=%2F"}, nil
	}))
}
