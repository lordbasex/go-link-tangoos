package rtc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// End to end over loopback: signaling, DataChannels, gating, login, events
// and metrics, exactly as the desktop uses them.
func TestEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hub := rtcrpc.NewHub("jobs.changed", "metrics")
	router := rtcrpc.NewRouter(nil)
	router.Handle("system.info", func(context.Context, *rtcrpc.Session, json.RawMessage) (any, error) {
		return map[string]string{"hostname": "tango"}, nil
	}, rtcrpc.Public())
	router.Handle("auth.login", func(_ context.Context, s *rtcrpc.Session, p json.RawMessage) (any, error) {
		if !strings.Contains(string(p), `"secret"`) {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidCredentials, "no")
		}
		s.Authenticate("admin", "tok")
		return map[string]string{"token": "tok"}, nil
	}, rtcrpc.Public())
	router.Handle("whoami", func(_ context.Context, s *rtcrpc.Session, _ json.RawMessage) (any, error) {
		return map[string]string{"user": s.User()}, nil
	})

	srv, err := New(ctx, Config{ICEPort: 0, STUNPort: 0, Loopback: true, Server: "tangod/test", NASID: "tg_test"}, router, hub, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	hs := httptest.NewServer(srv)
	defer hs.Close()

	c, err := Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http")+"/signal?v=1", false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Hello.NASID != "tg_test" || len(c.Hello.Methods) != 3 || len(c.Hello.ICEServers) != 0 {
		t.Fatalf("hello: %+v", c.Hello)
	}

	if _, err := c.Call(ctx, "whoami", nil); !isCode(err, rtcrpc.CodeUnauthenticated) {
		t.Fatalf("gated whoami: %v", err)
	}
	if r, err := c.Call(ctx, "system.info", nil); err != nil || !strings.Contains(string(r), "tango") {
		t.Fatalf("system.info: %s %v", r, err)
	}
	if err := c.Login(ctx, "admin", "bad"); !isCode(err, rtcrpc.CodeInvalidCredentials) {
		t.Fatalf("bad login: %v", err)
	}
	if err := c.Login(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Call(ctx, "whoami", nil); err != nil || !strings.Contains(string(r), "admin") {
		t.Fatalf("whoami: %s %v", r, err)
	}

	// the session is in the hub once connected
	deadline := time.Now().Add(5 * time.Second)
	for len(hub.Sessions()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	hub.Publish("jobs.changed", map[string]int{"n": 1})
	select {
	case ev := <-c.Events:
		if !strings.Contains(string(ev), `"seq":1`) || !strings.Contains(string(ev), `"topic":"jobs.changed"`) {
			t.Fatalf("event: %s", ev)
		}
	case <-ctx.Done():
		t.Fatal("no event")
	}
	for _, s := range hub.Sessions() {
		s.SendMetrics([]byte(`{"t":1,"cpu":0.5}`))
	}
	select {
	case m := <-c.Metrics:
		if !strings.Contains(string(m), `"cpu":0.5`) {
			t.Fatalf("metrics: %s", m)
		}
	case <-ctx.Done():
		t.Fatal("no metrics")
	}
}

func isCode(err error, code string) bool {
	var e *RPCError
	return errors.As(err, &e) && e.Code == code
}
