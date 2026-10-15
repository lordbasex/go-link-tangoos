package rtcrpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type sink struct{ ch chan string }

func newSink() *sink { return &sink{ch: make(chan string, 16)} }

func (s *sink) send(t string) error { s.ch <- t; return nil }

func (s *sink) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case m := <-s.ch:
		var v map[string]any
		if err := json.Unmarshal([]byte(m), &v); err != nil {
			t.Fatalf("bad frame %q: %v", m, err)
		}
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
	}
	return nil
}

func setup() (*Router, *Session, *sink, *sink) {
	r := NewRouter(nil)
	rpc, ev := newSink(), newSink()
	s := NewSession("s1", "192.0.2.1", "web", rpc.send, ev.send, nil)
	type echoP struct {
		Text string `json:"text"`
	}
	r.Handle("echo", Typed(func(_ context.Context, _ *Session, p echoP) (map[string]string, error) {
		return map[string]string{"text": p.Text}, nil
	}))
	r.Handle("system.info", func(context.Context, *Session, json.RawMessage) (any, error) {
		return map[string]string{"hostname": "tango"}, nil
	}, Public())
	r.Handle("fail", func(context.Context, *Session, json.RawMessage) (any, error) {
		return nil, Errorf("VOLUME_BUSY", "ocupado")
	})
	r.Handle("boom", func(context.Context, *Session, json.RawMessage) (any, error) {
		return nil, errors.New("disk on fire")
	})
	r.Handle("panic", func(context.Context, *Session, json.RawMessage) (any, error) {
		panic("oops")
	})
	return r, s, rpc, ev
}

func req(id int, method, params string) []byte {
	return []byte(`{"type":"req","id":` + itoa(id) + `,"method":"` + method + `","params":` + params + `}`)
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func errCode(v map[string]any) string {
	e, _ := v["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestGatedSessionOnlyReachesPublicMethods(t *testing.T) {
	r, s, rpc, _ := setup()
	r.Dispatch(context.Background(), s, req(1, "echo", `{"text":"hola"}`))
	if v := rpc.next(t); v["type"] != "err" || errCode(v) != CodeUnauthenticated {
		t.Fatalf("got %v", v)
	}
	r.Dispatch(context.Background(), s, req(2, "system.info", "null"))
	if v := rpc.next(t); v["type"] != "res" || v["id"].(float64) != 2 {
		t.Fatalf("got %v", v)
	}
}

func TestAuthenticatedCalls(t *testing.T) {
	r, s, rpc, _ := setup()
	s.Authenticate("admin", "tok")
	r.Dispatch(context.Background(), s, req(3, "echo", `{"text":"hola"}`))
	v := rpc.next(t)
	if v["type"] != "res" || v["result"].(map[string]any)["text"] != "hola" {
		t.Fatalf("got %v", v)
	}
}

func TestErrors(t *testing.T) {
	r, s, rpc, _ := setup()
	s.Authenticate("admin", "tok")
	cases := []struct {
		msg  []byte
		code string
	}{
		{req(1, "nope", "null"), CodeMethodNotFound},
		{req(2, "echo", `{"text":"x","extra":1}`), CodeInvalidParams},
		{req(3, "echo", `{"text":`), ""}, // malformed frame: ignored
		{req(4, "echo", `[1]`), CodeInvalidParams},
		{req(5, "fail", "null"), "VOLUME_BUSY"},
		{req(6, "boom", "null"), CodeInternal},
		{req(7, "panic", "null"), CodeInternal},
	}
	for _, c := range cases {
		r.Dispatch(context.Background(), s, c.msg)
		if c.code == "" {
			continue
		}
		if v := rpc.next(t); errCode(v) != c.code {
			t.Errorf("%s: got %v, want %s", c.msg, v, c.code)
		}
	}
	select {
	case m := <-rpc.ch:
		t.Fatalf("unexpected frame %s", m)
	default:
	}
}

func TestEventsOnlyWhenAuthenticatedAndSequenced(t *testing.T) {
	_, s, _, ev := setup()
	h := NewHub("jobs.changed")
	h.Add(s)
	h.Publish("jobs.changed", map[string]int{"n": 1})
	select {
	case m := <-ev.ch:
		t.Fatalf("gated session got %s", m)
	default:
	}
	s.Authenticate("admin", "tok")
	h.Publish("jobs.changed", map[string]int{"n": 2})
	h.Publish("jobs.changed", map[string]int{"n": 3})
	a, b := ev.next(t), ev.next(t)
	if a["seq"].(float64) != 1 || b["seq"].(float64) != 2 || a["topic"] != "jobs.changed" {
		t.Fatalf("got %v %v", a, b)
	}
	h.RevokeUser("admin")
	if s.Authenticated() {
		t.Fatal("still authenticated after revoke")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(1, 2)
	l.now = func() time.Time { return now }
	if !l.Allow() || !l.Allow() || l.Allow() {
		t.Fatal("burst")
	}
	now = now.Add(time.Second)
	if !l.Allow() || l.Allow() {
		t.Fatal("refill")
	}
}

func TestRateLimitedReply(t *testing.T) {
	r, s, rpc, _ := setup()
	s.limiter = NewLimiter(0, 1)
	r.Dispatch(context.Background(), s, req(1, "system.info", "null"))
	rpc.next(t)
	r.Dispatch(context.Background(), s, req(2, "system.info", "null"))
	if v := rpc.next(t); errCode(v) != CodeRateLimited {
		t.Fatalf("got %v", v)
	}
}

func TestGateRunsFirst(t *testing.T) {
	r := NewRouter(nil)
	r.Handle("open.thing", func(context.Context, *Session, json.RawMessage) (any, error) { return "ok", nil }, Public())
	r.Gate = func(s *Session, m string) *Error {
		if s.Get("scope") == "guest" && m != "guest.info" {
			return Errorf(CodeUnauthenticated, "no permitido")
		}
		return nil
	}
	out := make(chan string, 4)
	s := NewSession("g", "1.2.3.4", "guest", func(t string) error { out <- t; return nil }, nil, nil)
	s.Set("scope", "guest")
	r.Dispatch(context.Background(), s, []byte(`{"type":"req","id":1,"method":"open.thing"}`))
	if got := <-out; !strings.Contains(got, CodeUnauthenticated) {
		t.Fatalf("gate no rechazó: %s", got)
	}
	s.Set("scope", nil)
	r.Dispatch(context.Background(), s, []byte(`{"type":"req","id":2,"method":"open.thing"}`))
	if got := <-out; !strings.Contains(got, `"res"`) {
		t.Fatalf("sin scope debería pasar: %s", got)
	}
}
