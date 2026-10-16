package api

import (
	"context"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/clusterpki"
	"github.com/lordbasex/go-link-tangoos/internal/storage"
	"github.com/lordbasex/go-link-tangoos/internal/zfs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// routes registra storage.zfs.repl.* (protocol/README.md §18.1).
func (r *zfsRepl) routes(rt *rtcrpc.Router, admin func(*rtcrpc.Session) error) {
	type jobR struct {
		Job string `json:"job"`
	}
	type idP struct {
		ID string `json:"id"`
	}
	type replicaP struct {
		Pool   string `json:"pool"`
		Origin string `json:"origin"`
		Vol    string `json:"vol"`
	}
	validReplica := func(x replicaP) error {
		if zfs.ValidPoolName(x.Pool) != nil || zfs.ValidOrigin(x.Origin) != nil || zfs.ValidVolumeName(x.Vol) != nil {
			return rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "réplica inválida")
		}
		return nil
	}
	rt.Handle("storage.zfs.repl.status", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, _ struct{}) (ReplStatus, error) {
		if err := admin(sess); err != nil {
			return ReplStatus{}, err
		}
		return r.status(ctx), nil
	}))
	rt.Handle("storage.zfs.repl.receive", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x ReplReceive) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		if x.Enabled {
			if _, ok := r.z.pool(ctx, storage.ZfsPoolID(x.Pool)); !ok || zfs.ValidPoolName(x.Pool) != nil {
				return struct{}{}, rtcrpc.Errorf(codePoolNotFound, "elegí un pool ZFS donde guardar las réplicas")
			}
		}
		r.mu.Lock()
		r.st.Receive = x
		if !x.Enabled {
			r.invite = nil
		}
		r.save()
		r.mu.Unlock()
		r.net.sync()
		r.changed()
		return struct{}{}, nil
	}))
	rt.Handle("storage.zfs.repl.invite", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, _ struct{}) (ReplInvite, error) {
		if err := admin(sess); err != nil {
			return ReplInvite{}, err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if !r.st.Receive.Enabled {
			return ReplInvite{}, rtcrpc.Errorf(codeNotSupported, "prendé \"Recibir réplicas\" primero")
		}
		r.invite = &replInvite{code: clusterpki.NewJoinCode(), expires: time.Now().Add(replInviteTTL)}
		return ReplInvite{Code: r.invite.code, Expires: r.invite.expires.UTC().Format(time.RFC3339)}, nil
	}))
	rt.Handle("storage.zfs.repl.targets.probe", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Address string `json:"address"`
	}) (map[string]string, error) {
		if err := admin(sess); err != nil {
			return nil, err
		}
		id, p, err := r.net.probe(ctx, x.Address)
		if err != nil {
			return nil, err
		}
		if p.spki == r.selfSPKI() {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "esa dirección es este mismo NAS: para replicar acá elegí otro pool")
		}
		return map[string]string{"probe": id, "name": p.name, "fingerprint": p.fp, "address": p.addr}, nil
	}))
	rt.Handle("storage.zfs.repl.targets.add", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Probe string `json:"probe"`
		Code  string `json:"code"`
	}) (ReplTarget, error) {
		if err := admin(sess); err != nil {
			return ReplTarget{}, err
		}
		if len(clusterpki.NormalizeCode(x.Code)) != 10 {
			return ReplTarget{}, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "el código tiene 10 caracteres")
		}
		t, err := r.net.addTarget(ctx, x.Probe, x.Code)
		if err == nil {
			r.changed()
		}
		return t, err
	}))
	rt.Handle("storage.zfs.repl.targets.remove", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x idP) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, t := range r.st.Tasks {
			if t.Target == x.ID {
				return struct{}{}, rtcrpc.Errorf(agentproto.CodeBusy, "hay tareas que mandan a ese destino: borralas primero")
			}
		}
		keep := r.st.Targets[:0]
		for _, t := range r.st.Targets {
			if t.ID != x.ID {
				keep = append(keep, t)
			}
		}
		r.st.Targets = keep
		r.save()
		go r.changed()
		return struct{}{}, nil
	}))
	rt.Handle("storage.zfs.repl.sources.remove", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x idP) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		r.mu.Lock()
		keep := r.st.Sources[:0]
		for _, s := range r.st.Sources {
			if s.ID != x.ID {
				keep = append(keep, s)
			}
		}
		r.st.Sources = keep
		r.save()
		r.mu.Unlock()
		r.changed()
		return struct{}{}, nil
	}))
	rt.Handle("storage.zfs.repl.tasks.create", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		Volume string `json:"volume"`
		Target string `json:"target"`
		Every  int    `json:"every"`
		RunNow bool   `json:"runNow"`
	}) (map[string]string, error) {
		if err := admin(sess); err != nil {
			return nil, err
		}
		t, err := r.createTask(ctx, x.Volume, x.Target, x.Every)
		if err != nil {
			return nil, err
		}
		out := map[string]string{"id": t.ID}
		if x.RunNow {
			if j, err := r.start(t.ID, true); err == nil {
				out["job"] = j
			}
		}
		r.changed()
		return out, nil
	}))
	rt.Handle("storage.zfs.repl.tasks.update", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		ID      string `json:"id"`
		Every   int    `json:"every"`
		Enabled bool   `json:"enabled"`
	}) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		if err := validEvery(x.Every); err != nil {
			return struct{}{}, err
		}
		if _, ok := r.task(x.ID); !ok {
			return struct{}{}, rtcrpc.Errorf("NOT_FOUND", "no existe la tarea")
		}
		r.updateTask(x.ID, func(t *ReplTask) { t.Every, t.Enabled = x.Every, x.Enabled })
		r.changed()
		return struct{}{}, nil
	}))
	rt.Handle("storage.zfs.repl.tasks.delete", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x idP) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		err := r.deleteTask(ctx, x.ID)
		if err == nil {
			r.changed()
		}
		return struct{}{}, err
	}))
	rt.Handle("storage.zfs.repl.tasks.run", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x idP) (jobR, error) {
		if err := admin(sess); err != nil {
			return jobR{}, err
		}
		j, err := r.start(x.ID, true)
		return jobR{j}, err
	}))
	rt.Handle("storage.zfs.repl.replicas.promote", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x struct {
		replicaP
		Name       string `json:"name"`
		Passphrase string `json:"passphrase"`
	}) (map[string]bool, error) {
		if err := admin(sess); err != nil {
			return nil, err
		}
		if err := validReplica(x.replicaP); err != nil {
			return nil, err
		}
		locked, err := r.promote(ctx, x.Pool, x.Origin, x.Vol, strings.TrimSpace(x.Name), x.Passphrase)
		return map[string]bool{"locked": locked}, err
	}))
	rt.Handle("storage.zfs.repl.replicas.delete", rtcrpc.Typed(func(ctx context.Context, sess *rtcrpc.Session, x replicaP) (struct{}, error) {
		if err := admin(sess); err != nil {
			return struct{}{}, err
		}
		if err := validReplica(x); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, r.destroyReplica(ctx, x.Pool, x.Origin, x.Vol)
	}))
}

func (r *zfsRepl) selfSPKI() string {
	c, err := r.net.own()
	if err != nil || c.Leaf == nil {
		return ""
	}
	return spkiOf(c.Leaf)
}
