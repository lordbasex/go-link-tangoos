// Package rtcrpc is the request/response and event layer that TangoOS runs
// over WebRTC DataChannels (protocol/README.md §2–3, docs/03 §5–6).
//
// It knows nothing about WebRTC: a Session sends text frames through the
// Sender functions its transport hands it, and Router.Dispatch is fed the
// raw messages that arrive on the `rpc` channel.
package rtcrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Stable error codes (protocol/README.md §3).
const (
	CodeUnauthenticated    = "UNAUTHENTICATED"
	CodeInvalidCredentials = "INVALID_CREDENTIALS"
	CodeRateLimited        = "RATE_LIMITED"
	CodeMethodNotFound     = "METHOD_NOT_FOUND"
	CodeInvalidParams      = "INVALID_PARAMS"
	CodeNotImplemented     = "NOT_IMPLEMENTED"
	CodeInternal           = "INTERNAL"
)

// Error is a failure the client sees: a stable code and a human message.
// Any other error a handler returns is reported as INTERNAL and logged.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf builds an *Error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Frames on the wire.
type request struct {
	Type   string          `json:"type"`
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type response struct {
	Type   string `json:"type"`
	ID     int64  `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

type event struct {
	Type  string `json:"type"`
	Topic string `json:"topic"`
	Seq   uint64 `json:"seq"`
	Data  any    `json:"data"`
}

// Sender writes one text frame on a DataChannel.
type Sender func(text string) error

// Session is one connected client (one RTCPeerConnection).
type Session struct {
	ID       string
	RemoteIP string
	Client   string
	Since    time.Time

	rpc     Sender
	events  Sender
	metrics Sender

	seq   atomic.Uint64
	mu    sync.RWMutex
	user  string
	token string
	data  map[string]any

	slots   chan struct{} // concurrent requests in flight
	limiter *Limiter
}

// NewSession builds a session whose frames go out through the given senders.
// A nil sender drops its frames.
func NewSession(id, remoteIP, client string, rpc, events, metrics Sender) *Session {
	return &Session{
		ID: id, RemoteIP: remoteIP, Client: client, Since: time.Now(),
		rpc: rpc, events: events, metrics: metrics,
		slots:   make(chan struct{}, 16),
		limiter: NewLimiter(50, 100),
		data:    map[string]any{},
	}
}

// Authenticate marks the session as logged in as user, with the token that
// proves it (kept so logout can revoke it).
func (s *Session) Authenticate(user, token string) {
	s.mu.Lock()
	s.user, s.token = user, token
	s.mu.Unlock()
}

// Logout returns the session to the gated state and hands back its token.
func (s *Session) Logout() (token string) {
	s.mu.Lock()
	token, s.user, s.token = s.token, "", ""
	s.mu.Unlock()
	return token
}

// User is the logged-in user name, or "" while gated.
func (s *Session) User() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.user
}

// Token is the session's token ("" while gated): used to keep the caller's
// own session alive when its password changes.
func (s *Session) Token() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

// Authenticated reports whether the session has logged in.
func (s *Session) Authenticated() bool { return s.User() != "" }

// Set and Get keep per-session values for handlers (e.g. stats baselines).
func (s *Session) Set(key string, v any) {
	s.mu.Lock()
	s.data[key] = v
	s.mu.Unlock()
}

func (s *Session) Get(key string) any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key]
}

// Emit sends an event on the `events` channel, numbered with this session's
// sequence. Gated sessions get nothing.
func (s *Session) Emit(topic string, data any) {
	if s.events == nil || !s.Authenticated() {
		return
	}
	b, err := json.Marshal(event{Type: "evt", Topic: topic, Seq: s.seq.Add(1), Data: data})
	if err == nil {
		_ = s.events(string(b))
	}
}

// SendMetrics sends one frame on the unordered `metrics` channel.
func (s *Session) SendMetrics(frame []byte) {
	if s.metrics == nil || !s.Authenticated() {
		return
	}
	_ = s.metrics(string(frame))
}

// ---- Router --------------------------------------------------------------------

// HandlerFunc answers one request. params is the raw JSON (may be "null").
type HandlerFunc func(ctx context.Context, s *Session, params json.RawMessage) (any, error)

type route struct {
	fn     HandlerFunc
	public bool
}

// Option tunes a route.
type Option func(*route)

// Public lets gated (not logged in) sessions call the method.
func Public() Option { return func(r *route) { r.public = true } }

// Router maps method names to handlers.
type Router struct {
	mu     sync.RWMutex
	routes map[string]route
	Log    *slog.Logger
	// OnCall, si no es nil, se llama después de cada request atendido o rechazado
	// (auditoría, protocol/README.md §12). err es nil si salió bien.
	OnCall func(s *Session, method string, params json.RawMessage, err error)
	// Gate, si no es nil, decide antes que nada si la sesión puede llamar al método
	// (sesiones remotas de TangoConnect y de invitados, protocol/README.md §15). Un
	// *Error devuelto rechaza la llamada; nil sigue con las reglas normales.
	Gate func(s *Session, method string) *Error
}

// NewRouter returns an empty router.
func NewRouter(log *slog.Logger) *Router {
	if log == nil {
		log = slog.Default()
	}
	return &Router{routes: map[string]route{}, Log: log}
}

// Handle registers a method. Methods require a logged-in session unless
// Public() is given.
func (r *Router) Handle(method string, fn HandlerFunc, opts ...Option) {
	rt := route{fn: fn}
	for _, o := range opts {
		o(&rt)
	}
	r.mu.Lock()
	r.routes[method] = rt
	r.mu.Unlock()
}

// Methods lists the registered methods, sorted (the `hello` advertises them).
func (r *Router) Methods() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.routes))
	for m := range r.routes {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Typed adapts a handler with a typed params struct. Unknown fields and
// malformed JSON are INVALID_PARAMS; "null" or absent params give the zero P.
func Typed[P any, R any](fn func(ctx context.Context, s *Session, p P) (R, error)) HandlerFunc {
	return func(ctx context.Context, s *Session, raw json.RawMessage) (any, error) {
		var p P
		if len(raw) > 0 && string(raw) != "null" {
			dec := json.NewDecoder(bytesReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, Errorf(CodeInvalidParams, "parámetros inválidos: %v", err)
			}
		}
		return fn(ctx, s, p)
	}
}

// Dispatch handles one message from the `rpc` channel. It returns at once:
// the handler runs on its own goroutine (at most 16 in flight per session)
// and its answer goes back through the session's rpc sender.
func (r *Router) Dispatch(ctx context.Context, s *Session, msg []byte) {
	var req request
	if err := json.Unmarshal(msg, &req); err != nil || req.Type != "req" {
		return // not a request: ignored, like go-link's control channel
	}
	if !s.limiter.Allow() {
		r.reply(s, req.ID, nil, Errorf(CodeRateLimited, "demasiadas solicitudes"))
		return
	}
	r.mu.RLock()
	rt, ok := r.routes[req.Method]
	r.mu.RUnlock()
	if ok && r.Gate != nil {
		if e := r.Gate(s, req.Method); e != nil {
			if r.OnCall != nil {
				r.OnCall(s, req.Method, req.Params, e)
			}
			r.reply(s, req.ID, nil, e)
			return
		}
	}
	switch {
	case !ok:
		r.reply(s, req.ID, nil, Errorf(CodeMethodNotFound, "método desconocido: %s", req.Method))
		return
	case !rt.public && !s.Authenticated():
		e := Errorf(CodeUnauthenticated, "iniciá sesión primero")
		if r.OnCall != nil {
			r.OnCall(s, req.Method, req.Params, e)
		}
		r.reply(s, req.ID, nil, e)
		return
	}
	select {
	case s.slots <- struct{}{}:
	default:
		r.reply(s, req.ID, nil, Errorf(CodeRateLimited, "demasiadas solicitudes en curso"))
		return
	}
	go func() {
		defer func() { <-s.slots }()
		before := s.User() // auditoría: el actor de auth.logout ya no está después de la llamada
		res, err := r.call(ctx, s, req, rt.fn)
		if r.OnCall != nil {
			if before != "" && s.User() == "" {
				s.Set("audit.actor", before)
			}
			r.OnCall(s, req.Method, req.Params, err)
		}
		r.reply(s, req.ID, res, err)
	}()
}

func (r *Router) call(ctx context.Context, s *Session, req request, fn HandlerFunc) (res any, err error) {
	defer func() {
		if p := recover(); p != nil {
			r.Log.Error("rpc handler panic", "method", req.Method, "panic", p, "stack", string(debug.Stack()))
			res, err = nil, Errorf(CodeInternal, "error interno")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return fn(ctx, s, req.Params)
}

func (r *Router) reply(s *Session, id int64, res any, err error) {
	resp := response{ID: id}
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			r.Log.Error("rpc handler error", "id", id, "err", err)
			e = Errorf(CodeInternal, "error interno")
		}
		resp.Type, resp.Error = "err", e
	} else {
		resp.Type, resp.Result = "res", res
		if res == nil {
			resp.Result = struct{}{}
		}
	}
	b, mErr := json.Marshal(resp)
	if mErr != nil {
		r.Log.Error("rpc marshal", "err", mErr)
		b, _ = json.Marshal(response{Type: "err", ID: id, Error: Errorf(CodeInternal, "error interno")})
	}
	if s.rpc != nil {
		_ = s.rpc(string(b))
	}
}

// ---- Hub: live sessions and broadcast ------------------------------------------

// Hub tracks the live sessions and broadcasts events to the logged-in ones.
type Hub struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	topics   []string
}

// NewHub returns a hub that advertises the given topics in `hello`.
func NewHub(topics ...string) *Hub {
	return &Hub{sessions: map[string]*Session{}, topics: topics}
}

func (h *Hub) Add(s *Session) {
	h.mu.Lock()
	h.sessions[s.ID] = s
	h.mu.Unlock()
}

func (h *Hub) Remove(id string) {
	h.mu.Lock()
	delete(h.sessions, id)
	h.mu.Unlock()
}

// Sessions returns a snapshot of the live sessions, oldest first.
func (h *Hub) Sessions() []*Session {
	h.mu.RLock()
	out := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		out = append(out, s)
	}
	h.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out
}

// Topics is what `hello` advertises.
func (h *Hub) Topics() []string { return append([]string(nil), h.topics...) }

// Publish sends an event to every logged-in session.
func (h *Hub) Publish(topic string, data any) {
	for _, s := range h.Sessions() {
		s.Emit(topic, data)
	}
}

// AnyAuthenticated reports whether at least one session is logged in (the
// metrics publisher sleeps otherwise).
func (h *Hub) AnyAuthenticated() bool {
	for _, s := range h.Sessions() {
		if s.Authenticated() {
			return true
		}
	}
	return false
}

// RevokeUser logs out every session of user (after a password change, etc.).
func (h *Hub) RevokeUser(user string) {
	for _, s := range h.Sessions() {
		if s.User() == user {
			s.Logout()
		}
	}
}
