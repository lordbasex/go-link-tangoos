// Package jobs es el registro de operaciones largas de tangod (docs/02 "Ciclo de vida
// de un job", protocol/README.md §7). Vive en memoria y se persiste en
// <state>/jobs.json; al arrancar, lo que quedó a medias pasa a "interrupted".
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Estados (protocol/README.md §7).
const (
	Queued      = "queued"
	Validating  = "validating"
	Running     = "running"
	Succeeded   = "succeeded"
	Failed      = "failed"
	Interrupted = "interrupted"
)

// Job es exactamente el Job de TS, más el error final.
type Job struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	Detail    string  `json:"detail"`
	Progress  float64 `json:"progress"`
	State     string  `json:"state"`
	StartedAt string  `json:"startedAt"`
	EndedAt   string  `json:"endedAt,omitempty"`
	Rate      string  `json:"rate,omitempty"`
	Error     string  `json:"error,omitempty"`
}

func (j *Job) done() bool {
	return j.State == Succeeded || j.State == Failed || j.State == Interrupted
}

// Handle es lo que recibe la función de un job para informar avance.
type Handle struct {
	s  *Store
	id string
}

// ID del job.
func (h *Handle) ID() string { return h.id }

// Update cambia progreso (0..1) y detalle; publica jobs.changed.
func (h *Handle) Update(progress float64, detail string) {
	h.s.mutate(h.id, func(j *Job) {
		if progress >= 0 {
			j.Progress = min(1, progress)
		}
		if detail != "" {
			j.Detail = detail
		}
		if j.State == Validating || j.State == Queued {
			j.State = Running
		}
	}, false)
}

// Store guarda los jobs.
type Store struct {
	path    string
	publish func(Job)

	mu   sync.Mutex
	jobs map[string]*Job
	last time.Time // última persistencia por progreso
}

// keep: cuántos jobs terminados se conservan.
const keep = 50

// Open carga <dir>/jobs.json. publish se llama en cada cambio (puede ser nil).
func Open(dir string, publish func(Job)) *Store {
	s := &Store{path: filepath.Join(dir, "jobs.json"), publish: publish, jobs: map[string]*Job{}}
	if b, err := os.ReadFile(s.path); err == nil {
		var list []Job
		if json.Unmarshal(b, &list) == nil {
			now := time.Now().UTC().Format(time.RFC3339)
			for i := range list {
				j := list[i]
				if !j.done() {
					j.State, j.EndedAt = Interrupted, now
					j.Error = "tangod se reinició durante la operación; revisá el estado real en Almacenamiento"
				}
				s.jobs[j.ID] = &j
			}
		}
	}
	s.persist()
	return s
}

// Start crea un job y corre fn en segundo plano. Un error de fn deja el job en failed
// con ese mensaje; si no, succeeded. onEnd (opcional) recibe el job final.
func (s *Store) Start(typ, title, detail string, fn func(ctx context.Context, h *Handle) error, onEnd func(Job)) Job {
	id := "j_" + randHex(6)
	j := &Job{ID: id, Type: typ, Title: title, Detail: detail, State: Validating, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	s.mu.Lock()
	s.jobs[id] = j
	cp := *j
	s.mu.Unlock()
	s.persist()
	s.emit(cp)
	go func() {
		err := fn(context.Background(), &Handle{s: s, id: id})
		final := s.mutate(id, func(j *Job) {
			j.EndedAt = time.Now().UTC().Format(time.RFC3339)
			if err != nil {
				j.State, j.Error, j.Detail = Failed, err.Error(), err.Error()
			} else {
				j.State, j.Progress, j.Detail = Succeeded, 1, "Listo"
			}
		}, true)
		if onEnd != nil {
			onEnd(final)
		}
	}()
	return cp
}

// List devuelve los jobs: primero los que corren, después los últimos terminados.
func (s *Store) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].StartedAt > out[b].StartedAt })
	return out
}

// Get devuelve un job.
func (s *Store) Get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

func (s *Store) mutate(id string, fn func(*Job), force bool) Job {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return Job{}
	}
	fn(j)
	cp := *j
	persist := force || time.Since(s.last) > 2*time.Second
	s.mu.Unlock()
	if persist {
		s.persist()
	}
	s.emit(cp)
	return cp
}

func (s *Store) emit(j Job) {
	if s.publish != nil {
		s.publish(j)
	}
}

// persist escribe jobs.json de forma atómica y poda los terminados más viejos.
func (s *Store) persist() {
	s.mu.Lock()
	var done []*Job
	for _, j := range s.jobs {
		if j.done() {
			done = append(done, j)
		}
	}
	if len(done) > keep {
		sort.Slice(done, func(a, b int) bool { return done[a].StartedAt < done[b].StartedAt })
		for _, j := range done[:len(done)-keep] {
			delete(s.jobs, j.ID)
		}
	}
	list := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		list = append(list, *j)
	}
	s.last = time.Now()
	s.mu.Unlock()
	b, err := json.Marshal(list)
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
