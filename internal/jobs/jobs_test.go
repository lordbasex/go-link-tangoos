package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func wait(t *testing.T, s *Store, id string) Job {
	t.Helper()
	for i := 0; i < 200; i++ {
		if j, _ := s.Get(id); j.done() {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("el job no terminó")
	return Job{}
}

func TestLifecycleAndEvents(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var states []string
	s := Open(dir, func(j Job) { mu.Lock(); states = append(states, j.State); mu.Unlock() })
	ended := make(chan Job, 1)
	j := s.Start("x", "Prueba", "", func(_ context.Context, h *Handle) error {
		h.Update(0.5, "mitad")
		return nil
	}, func(j Job) { ended <- j })
	if j.State != Validating {
		t.Fatalf("estado inicial %s", j.State)
	}
	final := wait(t, s, j.ID)
	if final.State != Succeeded || final.Progress != 1 {
		t.Fatalf("final %+v", final)
	}
	if e := <-ended; e.ID != j.ID {
		t.Fatal("onEnd con otro job")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(states) < 3 || states[0] != Validating || states[1] != Running || states[len(states)-1] != Succeeded {
		t.Fatalf("eventos %v", states)
	}
}

func TestFailure(t *testing.T) {
	s := Open(t.TempDir(), nil)
	j := s.Start("x", "Prueba", "", func(context.Context, *Handle) error { return errors.New("se rompió") }, nil)
	if f := wait(t, s, j.ID); f.State != Failed || f.Error != "se rompió" {
		t.Fatalf("final %+v", f)
	}
}

func TestInterruptedOnReload(t *testing.T) {
	dir := t.TempDir()
	block := make(chan struct{})
	s := Open(dir, nil)
	j := s.Start("x", "Larga", "", func(context.Context, *Handle) error { <-block; return nil }, nil)
	s.persist()
	s2 := Open(dir, nil)
	got, ok := s2.Get(j.ID)
	if !ok || got.State != Interrupted {
		t.Fatalf("tras reabrir: %+v", got)
	}
	close(block)
	// Esperar a que el job original termine y persista: si no, su escritura de
	// jobs.json compite con el borrado de TempDir (flake visto en la corrida e2e).
	for i := 0; i < 200; i++ {
		if cur, _ := s.Get(j.ID); cur.State == Succeeded {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "jobs.json")); err != nil {
		t.Fatal(err)
	}
}
