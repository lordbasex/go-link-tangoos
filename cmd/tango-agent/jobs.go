package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// asyncJobs corre las operaciones largas (volume.create, scrub.start) en segundo
// plano, de a una por vez, con inicio y fin en el journal. tangod las sigue con
// job.status.
type asyncJobs struct {
	j   *journal
	log *slog.Logger

	mu      sync.Mutex
	running string // op_id en curso ("" = libre)
	jobs    map[string]*agentproto.JobStatus
}

func newAsyncJobs(j *journal, log *slog.Logger) *asyncJobs {
	return &asyncJobs{j: j, log: log, jobs: map[string]*agentproto.JobStatus{}}
}

// stepFn reporta progreso (0..1) y la etapa en curso.
type stepFn func(progress float64, stage string)

// start registra la op en el journal y la corre. Solo una destructiva a la vez.
func (a *asyncJobs) start(opID, op string, args any, fn func(ctx context.Context, step stepFn) (any, error)) *agentproto.Error {
	if !opIDRe.MatchString(opID) {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "op_id inválido")
	}
	raw, _ := json.Marshal(args)
	a.mu.Lock()
	if a.running != "" {
		a.mu.Unlock()
		return agentproto.Errf(agentproto.CodeBusy, "hay otra operación de almacenamiento en curso (%s)", a.running)
	}
	if err := a.j.begin(opID, op, raw); err != nil {
		a.mu.Unlock()
		return agentproto.Errf(agentproto.CodeExists, "%v", err)
	}
	st := &agentproto.JobStatus{OpID: opID, Op: op, State: "running", Stage: "Validando"}
	a.jobs[opID] = st
	a.running = opID
	a.mu.Unlock()

	go func() {
		// Las ops de disco no dependen de la conexión de tangod: tienen su propio límite.
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		step := func(p float64, stage string) {
			a.mu.Lock()
			st.Progress, st.Stage = p, stage
			a.mu.Unlock()
		}
		start := time.Now()
		res, err := fn(ctx, step)
		a.mu.Lock()
		if err != nil {
			st.State = "failed"
			var pe *agentproto.Error
			if errors.As(err, &pe) {
				st.Error = pe
			} else {
				st.Error = agentproto.Errf(agentproto.CodeFailed, "%v", err)
			}
		} else {
			st.State, st.Progress, st.Stage, st.Result = "succeeded", 1, "Listo", res
		}
		a.running = ""
		a.mu.Unlock()
		_ = a.j.end(opID, err)
		attrs := []any{"op_id", opID, "op", op, "state", st.State, "s", int(time.Since(start).Seconds())}
		if err != nil {
			attrs = append(attrs, "err", err.Error())
			var xe *diskops.ExecError
			if errors.As(err, &xe) {
				attrs = append(attrs, "output", xe.Output)
			}
		}
		a.log.Info("op asíncrona terminada", attrs...)
	}()
	return nil
}

// status devuelve el estado de una op: en memoria, o del journal si el agente se reinició.
func (a *asyncJobs) status(opID string) (*agentproto.JobStatus, *agentproto.Error) {
	a.mu.Lock()
	if st, ok := a.jobs[opID]; ok {
		cp := *st
		a.mu.Unlock()
		return &cp, nil
	}
	a.mu.Unlock()
	if !opIDRe.MatchString(opID) {
		return nil, agentproto.Errf(agentproto.CodeInvalidArgs, "op_id inválido")
	}
	e, err := a.j.read(opID)
	if err != nil {
		return nil, agentproto.Errf(agentproto.CodeNotFound, "no hay registro de %s", opID)
	}
	st := &agentproto.JobStatus{OpID: opID, Op: e.Op, State: e.State}
	if e.State == "succeeded" {
		st.Progress = 1
	}
	if e.Error != "" {
		code := agentproto.CodeFailed
		if e.State == "interrupted" {
			code = "INTERRUPTED"
		}
		st.Error = agentproto.Errf(code, "%s", e.Error)
	}
	return st, nil
}
