package api

import (
	"context"
	"strings"

	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Time Machine por carpeta (protocol/README.md §13). shares.update cambia descripción,
// SMB y Time Machine de una carpeta existente y vuelve a aplicar su sección de Samba
// (con los permisos que ya tiene).

const tmMinBytes = 50 << 30 // un tope menor a 50 GiB no sirve para Time Machine

func validTM(smb, tm bool, max int64) error {
	if tm && !smb {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "Time Machine necesita que la carpeta se publique por SMB")
	}
	if max < 0 || (max > 0 && max < tmMinBytes) {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "el tope de Time Machine tiene que ser de al menos 50 GB (o sin tope)")
	}
	return nil
}

func (s *storageAPI) registerShareUpdate(r *rtcrpc.Router) {
	type updateP struct {
		Share               string  `json:"share"`
		Description         *string `json:"description"`
		SMB                 *bool   `json:"smb"`
		TimeMachine         *bool   `json:"timeMachine"`
		TimeMachineMaxBytes *int64  `json:"timeMachineMaxBytes"` // null o 0 = sin tope
	}
	type jobR struct {
		Job string `json:"job"`
	}
	r.Handle("shares.update", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, p updateP) (jobR, error) {
		if err := s.a.requireAdmin(sess); err != nil {
			return jobR{}, err
		}
		sh, mount, ok := s.shareByID(ctx, p.Share)
		if !ok {
			return jobR{}, rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida")
		}
		key := strings.ToLower(sh.Name)
		s.mu.Lock()
		m := s.m.Shares[key]
		s.mu.Unlock()
		if p.Description != nil {
			if len(*p.Description) > 256 {
				return jobR{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "la descripción es demasiado larga")
			}
			m.Description = *p.Description
		}
		if p.SMB != nil {
			m.SMB = *p.SMB
		}
		if p.TimeMachine != nil {
			m.TimeMachine = *p.TimeMachine
		}
		if p.TimeMachineMaxBytes != nil {
			m.TMMaxBytes = *p.TimeMachineMaxBytes
		}
		if !m.TimeMachine {
			m.TMMaxBytes = 0
		}
		if err := validTM(m.SMB, m.TimeMachine, m.TMMaxBytes); err != nil {
			return jobR{}, err
		}
		title := "Actualizando " + sh.Name
		j := s.jobs.Start("shares.update", title, "", func(ctx context.Context, h *jobs.Handle) error {
			s.mu.Lock()
			s.m.Shares[key] = m
			s.save()
			entries := s.m.Perms[sh.ID]
			s.mu.Unlock()
			if entries == nil {
				entries = defaultPerms()
			}
			sh.Description, sh.SMB, sh.TimeMachine = m.Description, m.SMB, m.TimeMachine && m.SMB
			sh.TimeMachineMaxBytes = nil
			if m.TMMaxBytes > 0 {
				v := m.TMMaxBytes
				sh.TimeMachineMaxBytes = &v
			}
			h.Update(0.5, "Aplicando la configuración de SMB")
			return s.applyPerms(ctx, sh, mount, entries)
		}, func(j jobs.Job) {
			s.a.d.Hub.Publish("shares.changed", map[string]string{"id": sh.ID})
			s.a.d.Hub.Publish("services.changed", map[string]string{"id": "smb"})
			if j.State != jobs.Succeeded {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.settings", Title: "No se pudo actualizar " + sh.Name, Body: j.Error})
			}
		})
		return jobR{j.ID}, nil
	}))
}

// tmCount cuenta las carpetas que son destino de Time Machine (para services.list).
func (s *storageAPI) tmCount(ctx context.Context) int {
	n := 0
	for _, sh := range s.shares(ctx) {
		if sh.TimeMachine {
			n++
		}
	}
	return n
}
