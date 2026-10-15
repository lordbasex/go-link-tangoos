package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/auth"
	"github.com/lordbasex/go-link-tangoos/internal/jobs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

func init() { sambaMigrateDelay = time.Hour } // en los tests no se regenera Samba

// fakeAgentV10 suma user.delete al fakeAgent.
type fakeAgentV10 struct {
	*fakeAgent
	mu      sync.Mutex
	deleted []agentproto.UserDeleteArgs
	smbPw   map[string]string
}

func (f *fakeAgentV10) UserDelete(_ context.Context, a agentproto.UserDeleteArgs) (*agentproto.UserDeleteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, a)
	return &agentproto.UserDeleteResult{Existed: true}, nil
}
func (f *fakeAgentV10) UserPassword(_ context.Context, name, _, pw string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.smbPw[name] = pw
	return nil
}

type rpcHarness struct {
	t      *testing.T
	r      *rtcrpc.Router
	store  *auth.Store
	agent  *fakeAgentV10
	mu     sync.Mutex
	replys map[int]json.RawMessage
	seq    int
}

func newRPCHarness(t *testing.T) *rpcHarness {
	t.Helper()
	dir := t.TempDir()
	st, err := auth.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureAdmin(dir); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(auth.User{Name: "sofia", Full: "Sofía"}, "una-clave-larga"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &rpcHarness{t: t, r: rtcrpc.NewRouter(log), store: st, replys: map[int]json.RawMessage{},
		agent: &fakeAgentV10{fakeAgent: &fakeAgent{fp: map[string]string{}}, smbPw: map[string]string{}}}
	Register(h.r, Deps{Auth: st, Inventory: testInventory(t), Hub: rtcrpc.NewHub(Topics...), StateDir: dir, Log: log,
		Agent: h.agent, Jobs: jobs.Open(filepath.Join(dir, "jobs"), nil)})
	return h
}

// call ejecuta un método como user y devuelve (result, error code).
func (h *rpcHarness) call(user, method string, params any) (json.RawMessage, string) {
	h.t.Helper()
	got := make(chan []byte, 1)
	s := rtcrpc.NewSession("s", "10.0.0.1", "web", func(text string) error { got <- []byte(text); return nil }, nil, nil)
	if user != "" {
		s.Authenticate(user, "tok-"+user)
	}
	h.seq++
	p, _ := json.Marshal(params)
	msg, _ := json.Marshal(map[string]any{"type": "req", "id": h.seq, "method": method, "params": json.RawMessage(p)})
	h.r.Dispatch(context.Background(), s, msg)
	select {
	case b := <-got:
		var resp struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			h.t.Fatal(err)
		}
		if resp.Error != nil {
			return nil, resp.Error.Code
		}
		return resp.Result, ""
	case <-time.After(5 * time.Second):
		h.t.Fatalf("%s: no reply", method)
		return nil, ""
	}
}

func TestUsersDeleteRules(t *testing.T) {
	h := newRPCHarness(t)
	cases := []struct {
		user, name, typed, want string
	}{
		{"sofia", "admin", "admin", "PERMISSION_DENIED"},
		{"admin", "nadie", "nadie", "USER_NOT_FOUND"},
		{"admin", "sofia", "Sofia", "CONFIRMATION_MISMATCH"},
		{"admin", "admin", "admin", "CANNOT_DELETE_SELF"},
	}
	for _, c := range cases {
		if _, code := h.call(c.user, "users.delete", map[string]any{"name": c.name, "typed_name": c.typed}); code != c.want {
			t.Fatalf("%s deletes %s: %q, want %q", c.user, c.name, code, c.want)
		}
	}
	// Último administrador: un segundo admin no puede borrar al único otro... con dos admins sí.
	_ = h.store.Add(auth.User{Name: "jefa", Full: "Jefa", Admin: true, Groups: []string{"administrators"}}, "otra-clave-larga")
	if _, code := h.call("jefa", "users.delete", map[string]any{"name": "admin", "typed_name": "admin"}); code != "" {
		t.Fatalf("delete admin with 2 admins: %q", code)
	}
	if _, code := h.call("jefa", "users.delete", map[string]any{"name": "jefa", "typed_name": "jefa"}); code != "CANNOT_DELETE_SELF" {
		t.Fatalf("self: %q", code)
	}
	// Borrar sofia moviendo sus archivos a quien borra.
	if _, code := h.call("jefa", "users.delete", map[string]any{"name": "sofia", "typed_name": "sofia", "keepFiles": false}); code != "" {
		t.Fatalf("delete sofia: %q", code)
	}
	if _, ok := h.store.Get("sofia"); ok {
		t.Fatal("sofia still in the store")
	}
	h.agent.mu.Lock()
	defer h.agent.mu.Unlock()
	if len(h.agent.deleted) != 2 || h.agent.deleted[1].Name != "sofia" || h.agent.deleted[1].ReassignTo != "jefa" || h.agent.deleted[0].ReassignTo != "" {
		t.Fatalf("agent calls: %+v", h.agent.deleted)
	}
}

func TestAppPasswordsRPC(t *testing.T) {
	h := newRPCHarness(t)
	res, code := h.call("sofia", "users.appPasswords.create", map[string]any{"label": "Mac SMB", "smb": true})
	if code != "" {
		t.Fatalf("create: %s", code)
	}
	var c struct {
		ID, Password string
		SMB          bool
	}
	_ = json.Unmarshal(res, &c)
	if c.Password == "" || !c.SMB || h.agent.smbPw["sofia"] != c.Password {
		t.Fatalf("create result %+v smb=%q", c, h.agent.smbPw["sofia"])
	}
	// Otro usuario común no ve ni revoca las de sofia; admin sí.
	_ = h.store.Add(auth.User{Name: "martin", Full: "Martín"}, "clave-de-martin")
	if _, code := h.call("martin", "users.appPasswords.list", map[string]any{"name": "sofia"}); code != "PERMISSION_DENIED" {
		t.Fatalf("martin lists sofia: %q", code)
	}
	list, code := h.call("admin", "users.appPasswords.list", map[string]any{"name": "sofia"})
	if code != "" || len(list) < 10 {
		t.Fatalf("admin list: %s %s", code, list)
	}
	if _, code := h.call("sofia", "users.appPasswords.revoke", map[string]any{"id": c.ID}); code != "" {
		t.Fatalf("revoke: %s", code)
	}
	if h.agent.smbPw["sofia"] == c.Password {
		t.Fatal("revoking the SMB app password must rotate the Samba password")
	}
	if _, code := h.call("sofia", "users.appPasswords.revoke", map[string]any{"id": c.ID}); code != "NOT_FOUND" {
		t.Fatalf("revoke twice: %q", code)
	}
}
