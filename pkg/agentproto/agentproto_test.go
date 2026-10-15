package agentproto

import (
	"context"
	"encoding/json"
	"testing"
)

func newTestRegistry() *Registry {
	r := NewRegistry()
	Register(r, OpDiskSmart, func(a *DiskSmartArgs) *Error {
		if a.DevID == "" {
			return Errf(CodeInvalidArgs, "falta dev_id")
		}
		return nil
	}, func(_ context.Context, a *DiskSmartArgs) (any, *Error) {
		return DiskSmartResult{DevID: a.DevID, Supported: false, Reason: "test"}, nil
	})
	Register(r, OpVersion, nil, func(context.Context, *NoArgs) (any, *Error) {
		return VersionResult{Agent: "test", Protocol: Version}, nil
	})
	return r
}

func TestParseRequest(t *testing.T) {
	cases := []struct {
		name string
		line string
		code string
	}{
		{"ok", `{"v":1,"id":1,"op":"agent.version"}`, ""},
		{"campo de más", `{"v":1,"id":1,"op":"agent.version","sudo":true}`, CodeBadRequest},
		{"versión", `{"v":2,"id":1,"op":"agent.version"}`, CodeVersion},
		{"sin op", `{"v":1,"id":1}`, CodeBadRequest},
		{"basura", `{"v":1,"id":1,"op":"x"} {"v":1}`, CodeBadRequest},
		{"no json", `rm -rf /`, CodeBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, e := ParseRequest([]byte(c.line))
			got := ""
			if e != nil {
				got = e.Code
			}
			if got != c.code {
				t.Fatalf("code = %q, want %q", got, c.code)
			}
		})
	}
}

func TestDispatch(t *testing.T) {
	r := newTestRegistry()
	ctx := context.Background()

	resp := r.Dispatch(ctx, &Request{V: 1, ID: 9, Op: "shell.exec", Args: json.RawMessage(`{"cmd":"id"}`)})
	if resp.OK || resp.Error.Code != CodeUnknownOp || resp.ID != 9 {
		t.Fatalf("op desconocida: %+v", resp)
	}

	resp = r.Dispatch(ctx, &Request{V: 1, ID: 1, Op: OpDiskSmart, Args: json.RawMessage(`{"dev_id":"x","force":true}`)})
	if resp.OK || resp.Error.Code != CodeInvalidArgs {
		t.Fatalf("args con campo de más: %+v", resp)
	}

	resp = r.Dispatch(ctx, &Request{V: 1, ID: 2, Op: OpDiskSmart, Args: json.RawMessage(`{}`)})
	if resp.OK || resp.Error.Code != CodeInvalidArgs {
		t.Fatalf("validación: %+v", resp)
	}

	resp = r.Dispatch(ctx, &Request{V: 1, ID: 3, Op: OpDiskSmart, Args: json.RawMessage(`{"dev_id":"wwn-0x1"}`)})
	if !resp.OK {
		t.Fatalf("ok esperado: %+v", resp)
	}
	var res DiskSmartResult
	if err := json.Unmarshal(resp.Result, &res); err != nil || res.DevID != "wwn-0x1" {
		t.Fatalf("resultado: %v %+v", err, res)
	}

	resp = r.Dispatch(ctx, &Request{V: 1, ID: 4, Op: OpVersion, Args: json.RawMessage(`{"x":1}`)})
	if resp.OK {
		t.Fatalf("NoArgs debe rechazar campos: %+v", resp)
	}
	resp = r.Dispatch(ctx, &Request{V: 1, ID: 5, Op: OpVersion})
	if !resp.OK {
		t.Fatalf("version: %+v", resp)
	}
	if got := r.Ops(); len(got) != 2 || got[0] != OpVersion {
		t.Fatalf("Ops = %v", got)
	}
}
