package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Fase M2.1 (protocol/README.md §8): eliminar volúmenes y carpetas, permisos.

// ---- Eliminar un volumen ---------------------------------------------------------------

func (s *storageAPI) deletePlan(ctx context.Context, volID string) (DeletePlan, error) {
	v, ok := s.volumeByID(ctx, volID)
	if !ok {
		return DeletePlan{}, rtcrpc.Errorf("VOLUME_NOT_FOUND", "no existe el volumen")
	}
	if err := diskops.ValidVolumeName(v.Name); err != nil || (v.Mount != "" && v.Mount != "/"+v.Name) {
		return DeletePlan{}, rtcrpc.Errorf(codeNotEligible, "%s no es un volumen de TangoOS: eliminalo a mano", v.Name)
	}
	if v.State == "building" || v.State == "scrubbing" {
		return DeletePlan{}, rtcrpc.Errorf("VOLUME_BUSY", "esperá a que termine la %s", map[string]string{"building": "construcción", "scrubbing": "verificación"}[v.State])
	}
	disks, err := s.inv.Disks(ctx)
	if err != nil {
		return DeletePlan{}, rtcrpc.Errorf(rtcrpc.CodeInternal, "%v", err)
	}
	byID := map[string]storage.Disk{}
	for _, d := range disks {
		byID[d.ID] = d
	}
	var pds []planDisk
	out := DeletePlan{Summary: DeleteSummary{Name: v.Name, Shares: []string{}, Used: v.Used, Disks: []DeleteDisk{}}}
	for _, id := range v.Disks {
		d, ok := byID[id]
		if !ok || d.ByID == "" {
			return DeletePlan{}, rtcrpc.Errorf(codeChanged, "no se pudo identificar un disco de %s: revisá el inventario", v.Name)
		}
		pds = append(pds, planDisk{ID: d.ID, ByID: d.ByID, Model: d.Model, Serial: d.Serial, Size: d.Size})
		out.Summary.Disks = append(out.Summary.Disks, DeleteDisk{ID: d.ID, Model: d.Model, Serial: d.Serial})
	}
	if len(pds) == 0 {
		return DeletePlan{}, rtcrpc.Errorf(codeChanged, "%s no tiene discos visibles", v.Name)
	}
	for _, sh := range s.shares(ctx) {
		if sh.Volume == v.ID {
			out.Summary.Shares = append(out.Summary.Shares, sh.Name)
			out.Summary.Snapshots += len(s.snapshots(ctx, sh.ID))
		}
	}
	p := s.planner.addDelete(v.Name, v.ID, v.Mount, pds, out.Summary.Shares)
	out.PlanID, out.ConfirmToken, out.Expires = p.ID, p.Token, p.Expires.UTC().Format(time.RFC3339)
	return out, nil
}

func (s *storageAPI) deleteVolume(ctx context.Context, planID, token, typed string) (string, error) {
	p, code, msg := s.planner.checkKind(planDelete, planID, token, typed)
	if p == nil {
		return "", rtcrpc.Errorf(code, "%s", msg)
	}
	// Revalidar: el volumen sigue existiendo con los mismos discos.
	v, ok := s.volumeByID(ctx, p.VolumeID)
	if !ok || v.Name != p.Name {
		s.planner.consume(planID)
		return "", rtcrpc.Errorf("VOLUME_NOT_FOUND", "el volumen ya no existe")
	}
	want := make([]string, len(p.Disks))
	for i, d := range p.Disks {
		want[i] = d.ID
	}
	got := append([]string{}, v.Disks...)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "|") != strings.Join(got, "|") {
		s.planner.consume(planID)
		return "", rtcrpc.Errorf(codeChanged, "los discos de %s cambiaron desde el plan: revisalo de nuevo", v.Name)
	}
	s.planner.consume(planID)

	args := agentproto.VolumeDeleteArgs{Name: p.Name, Shares: p.Shares}
	for _, d := range p.Disks {
		args.DevIDs = append(args.DevIDs, d.ByID)
	}
	j := s.jobs.Start("storage.volume.delete", "Eliminando "+p.Name, fmt.Sprintf("%d discos quedan libres", len(p.Disks)),
		func(ctx context.Context, h *jobs.Handle) error {
			args.OpID = h.ID()
			if s.a.files != nil {
				s.a.files.ForgetUnder("/" + p.Name + "/")
				s.a.files.InvalidateWorkers() // sueltan los fds abiertos en el volumen antes de desmontar
			}
			if err := s.agent.VolumeDelete(ctx, args); err != nil {
				return errors.New(agentMessage(err))
			}
			_, err := s.follow(ctx, h)
			return err
		},
		func(j jobs.Job) {
			if j.State == jobs.Succeeded {
				s.mu.Lock()
				for _, sh := range p.Shares {
					delete(s.m.Shares, strings.ToLower(sh))
					delete(s.m.Perms, strings.ToLower(sh))
				}
				delete(s.m.LastScrub, p.Mount)
				s.save()
				s.mu.Unlock()
			}
			s.a.d.Hub.Publish("storage.changed", map[string]string{"what": "volumes"})
			s.a.d.Hub.Publish("storage.changed", map[string]string{"what": "disks"})
			s.a.d.Hub.Publish("shares.changed", map[string]string{})
			if j.State == jobs.Succeeded {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.storage", Title: p.Name + " eliminado",
					Body: fmt.Sprintf("Los %d discos quedaron libres para un volumen nuevo.", len(p.Disks))})
			} else {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.storage", Title: "No se pudo eliminar " + p.Name, Body: j.Error})
			}
		})
	return j.ID, nil
}

// ---- Eliminar una carpeta compartida ------------------------------------------------------

func (s *storageAPI) deleteShare(ctx context.Context, shareID, typed string) (string, error) {
	sh, mount, ok := s.shareByID(ctx, shareID)
	if !ok {
		return "", rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida")
	}
	if strings.TrimSpace(typed) != sh.Name {
		return "", rtcrpc.Errorf(codeMismatch, "el nombre escrito no coincide con el de la carpeta")
	}
	j := s.jobs.Start("shares.delete", "Eliminando la carpeta "+sh.Name, "Subvolumen e instantáneas",
		func(ctx context.Context, h *jobs.Handle) error {
			if s.a.files != nil {
				s.a.files.ForgetUnder(mount + "/@shares/" + sh.Name + "/")
				s.a.files.InvalidateWorkers() // el subvolumen no se puede borrar con fds abiertos
			}
			if _, err := s.agent.ShareDelete(ctx, mount, sh.Name); err != nil {
				return errors.New(agentMessage(err))
			}
			s.mu.Lock()
			delete(s.m.Shares, sh.ID)
			delete(s.m.Perms, sh.ID)
			s.save()
			s.mu.Unlock()
			return nil
		},
		func(j jobs.Job) {
			s.a.d.Hub.Publish("shares.changed", map[string]string{"id": sh.ID})
			s.a.d.Hub.Publish("snapshots.changed", map[string]string{"share": sh.ID})
			if j.State == jobs.Succeeded {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "ok", App: "tango.settings", Title: "Carpeta " + sh.Name + " eliminada", Body: "Se borraron sus archivos y sus instantáneas."})
			} else {
				s.a.Notify(Notice{ID: "n_" + j.ID, Level: "crit", App: "tango.settings", Title: "No se pudo eliminar " + sh.Name, Body: j.Error})
			}
		})
	return j.ID, nil
}

// ---- Permisos de carpetas -------------------------------------------------------------------

// defaultPerms: una carpeta nueva es de lectura y escritura para los administradores.
func defaultPerms() []PermEntry {
	return []PermEntry{{Kind: "group", Name: "administrators", Access: "rw"}}
}

func (s *storageAPI) permissions(ctx context.Context, shareID string) ([]PermEntry, error) {
	if _, _, ok := s.shareByID(ctx, shareID); !ok {
		return nil, rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m.Perms[shareID]
	if !ok {
		return defaultPerms(), nil
	}
	return append([]PermEntry{}, e...), nil
}

// expand convierte las entradas en usuarios con lectura/escritura y solo lectura.
// "rw" gana sobre "ro"; "none" no da acceso (y un usuario con none explícito no
// recibe acceso por un grupo).
func (s *storageAPI) expand(entries []PermEntry) (rw, ro []string, err error) {
	users := s.a.d.Auth.List()
	exists := map[string]bool{}
	members := map[string][]string{}
	for _, u := range users {
		exists[u.Name] = true
		for _, g := range u.Groups {
			members[g] = append(members[g], u.Name)
		}
		if u.Admin {
			members["administrators"] = appendUnique(members["administrators"], u.Name)
		}
	}
	access := map[string]string{}
	denied := map[string]bool{}
	rank := map[string]int{"none": 0, "ro": 1, "rw": 2}
	grant := func(u, a string) {
		if rank[a] > rank[access[u]] {
			access[u] = a
		}
	}
	for _, e := range entries {
		if _, ok := rank[e.Access]; !ok {
			return nil, nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "acceso inválido %q (rw, ro o none)", e.Access)
		}
		switch e.Kind {
		case "user":
			if !exists[e.Name] {
				return nil, nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "no existe el usuario %s", e.Name)
			}
			if e.Access == "none" {
				denied[e.Name] = true
			} else {
				grant(e.Name, e.Access)
			}
		case "group":
			if _, ok := members[e.Name]; !ok && e.Name != "administrators" {
				return nil, nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "no existe el grupo %s", e.Name)
			}
			if e.Access != "none" {
				for _, u := range members[e.Name] {
					grant(u, e.Access)
				}
			}
		default:
			return nil, nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "tipo inválido %q (user o group)", e.Kind)
		}
	}
	for u, a := range access {
		if denied[u] {
			continue
		}
		if diskops.ValidUserName(u) != nil {
			continue // usuarios que no pueden ser cuentas del sistema (no pasa con nombres válidos)
		}
		if a == "rw" {
			rw = append(rw, u)
		} else if a == "ro" {
			ro = append(ro, u)
		}
	}
	sort.Strings(rw)
	sort.Strings(ro)
	return rw, ro, nil
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func (s *storageAPI) setPermissions(ctx context.Context, shareID string, entries []PermEntry) error {
	sh, mount, ok := s.shareByID(ctx, shareID)
	if !ok {
		return rtcrpc.Errorf("SHARE_NOT_FOUND", "no existe la carpeta compartida")
	}
	if len(entries) > 128 {
		return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "demasiadas entradas")
	}
	if err := s.applyPerms(ctx, sh, mount, entries); err != nil {
		return err
	}
	s.mu.Lock()
	s.m.Perms[sh.ID] = append([]PermEntry{}, entries...)
	s.save()
	s.mu.Unlock()
	s.a.d.Hub.Publish("shares.changed", map[string]string{"id": sh.ID})
	return nil
}

// applyPerms lleva los permisos al sistema: grupos, ACL y la sección de Samba.
func (s *storageAPI) applyPerms(ctx context.Context, sh Share, mount string, entries []PermEntry) error {
	rw, ro, err := s.expand(entries)
	if err != nil {
		return err
	}
	args := agentproto.SharePermsArgs{
		Mount: mount, Share: sh.Name, Comment: sh.Description, SMB: sh.SMB, RW: nonNil(rw), RO: nonNil(ro),
		TimeMachine: sh.TimeMachine && sh.SMB,
	}
	if args.TimeMachine && sh.TimeMachineMaxBytes != nil {
		args.TMMaxBytes = *sh.TimeMachineMaxBytes
	}
	_, err = s.agent.SharePermissions(ctx, args)
	if err != nil {
		return agentErr(err)
	}
	if s.a.files != nil {
		s.a.files.InvalidateWorkers() // los file-workers toman los grupos nuevos
	}
	return nil
}
