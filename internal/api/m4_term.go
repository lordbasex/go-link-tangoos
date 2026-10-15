package api

import (
	"context"
	"encoding/binary"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lordbasex/go-link-tangoos/internal/rtc"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// Terminal de administración (protocol/README.md §12.1): el agente abre una shell en un
// PTY (unidad transitoria, root) y le pasa el master a tangod, que lo conecta con el
// canal term de la sesión. Solo administradores; term.open queda auditado.

// TermSpawner abre una consola y devuelve el master del PTY.
type TermSpawner interface {
	TermSpawn(ctx context.Context, cols, rows int, user string) (*os.File, error)
}

type termMgr struct {
	a     *API
	sp    TermSpawner
	mu    sync.Mutex
	next  uint32
	terms map[uint32]*termSess
}

type termSess struct {
	id     uint32
	sessID string
	pty    *os.File
	once   sync.Once
}

const maxTermsPerSession = 8

func (a *API) registerTerm(r *rtcrpc.Router, sp TermSpawner) *termMgr {
	m := &termMgr{a: a, sp: sp, terms: map[uint32]*termSess{}}
	type openP struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	type idP struct {
		ID   uint32 `json:"id"`
		Cols int    `json:"cols,omitempty"`
		Rows int    `json:"rows,omitempty"`
	}
	r.Handle("term.open", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p openP) (map[string]uint32, error) {
		if err := a.requireAdmin(s); err != nil {
			return nil, err
		}
		send, ok := s.Get("term").(rtc.TermSender)
		if !ok {
			return nil, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "esta conexión no tiene canal de terminal")
		}
		if m.count(s.ID) >= maxTermsPerSession {
			return nil, rtcrpc.Errorf(rtcrpc.CodeRateLimited, "demasiadas terminales abiertas")
		}
		cols, rows := clamp(p.Cols, 20, 500, 80), clamp(p.Rows, 5, 300, 24)
		pty, err := sp.TermSpawn(ctx, cols, rows, s.User())
		if err != nil {
			return nil, agentErr(err)
		}
		m.mu.Lock()
		m.next++
		t := &termSess{id: m.next, sessID: s.ID, pty: pty}
		m.terms[t.id] = t
		m.mu.Unlock()
		go m.pump(t, send)
		return map[string]uint32{"id": t.id}, nil
	}))
	r.Handle("term.resize", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p idP) (struct{}, error) {
		t := m.get(s, p.ID)
		if t == nil {
			return struct{}{}, rtcrpc.Errorf("TERM_NOT_FOUND", "la terminal ya no existe")
		}
		_ = unix.IoctlSetWinsize(int(t.pty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(clamp(p.Cols, 20, 500, 80)), Row: uint16(clamp(p.Rows, 5, 300, 24))})
		return struct{}{}, nil
	}))
	r.Handle("term.close", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p idP) (struct{}, error) {
		if t := m.get(s, p.ID); t != nil {
			m.close(t)
		}
		return struct{}{}, nil
	}))
	go m.reaper()
	return m
}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	return max(lo, min(hi, v))
}

func (m *termMgr) count(sessID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.terms {
		if t.sessID == sessID {
			n++
		}
	}
	return n
}

func (m *termMgr) get(s *rtcrpc.Session, id uint32) *termSess {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := m.terms[id]; t != nil && t.sessID == s.ID {
		return t
	}
	return nil
}

func (m *termMgr) close(t *termSess) {
	t.once.Do(func() {
		_ = t.pty.Close() // la shell recibe SIGHUP y la unidad termina
		m.mu.Lock()
		delete(m.terms, t.id)
		m.mu.Unlock()
	})
}

// pump copia la salida del PTY al canal term; al terminar manda el frame vacío (§12.1).
func (m *termMgr) pump(t *termSess, send rtc.TermSender) {
	buf := make([]byte, 32<<10)
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, t.id)
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			frame := append(append([]byte(nil), hdr...), buf[:n]...)
			for i := 0; send(frame) != nil && i < 50; i++ { // canal lleno: esperar un poco
				time.Sleep(20 * time.Millisecond)
			}
		}
		if err != nil {
			break
		}
	}
	_ = send(hdr)
	m.close(t)
}

// TermFrame recibe lo que el usuario escribe ([u32 id][bytes]).
func (a *API) TermFrame(s *rtcrpc.Session, data []byte) {
	if a.term == nil || len(data) < 4 || !s.Authenticated() {
		return
	}
	t := a.term.get(s, binary.BigEndian.Uint32(data))
	if t == nil {
		return
	}
	if len(data) == 4 {
		a.term.close(t)
		return
	}
	_, _ = t.pty.Write(data[4:])
}

// reaper cierra las terminales de sesiones que se desconectaron.
func (m *termMgr) reaper() {
	tk := time.NewTicker(10 * time.Second)
	defer tk.Stop()
	for range tk.C {
		live := map[string]bool{}
		for _, s := range m.a.d.Hub.Sessions() {
			live[s.ID] = true
		}
		m.mu.Lock()
		var dead []*termSess
		for _, t := range m.terms {
			if !live[t.sessID] {
				dead = append(dead, t)
			}
		}
		m.mu.Unlock()
		for _, t := range dead {
			m.close(t)
		}
	}
}
