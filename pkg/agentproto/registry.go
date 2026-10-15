package agentproto

import (
	"context"
	"encoding/json"
	"sort"
)

// Handler ejecuta una op con argumentos ya validados.
type Handler func(ctx context.Context, args json.RawMessage) (any, *Error)

// Registry es la allow-list tipada de operaciones del agente.
type Registry struct {
	ops map[string]Handler
}

// NewRegistry crea un registro vacío.
func NewRegistry() *Registry { return &Registry{ops: map[string]Handler{}} }

// Register agrega una op con argumentos tipados: el JSON se decodifica en modo estricto
// en A antes de llamar a fn, y validate (opcional) corre antes de ejecutar.
func Register[A any](r *Registry, op string, validate func(*A) *Error, fn func(ctx context.Context, a *A) (any, *Error)) {
	r.ops[op] = func(ctx context.Context, raw json.RawMessage) (any, *Error) {
		var a A
		if len(raw) > 0 && string(raw) != "null" {
			if err := DecodeStrict(raw, &a); err != nil {
				return nil, Errf(CodeInvalidArgs, "argumentos inválidos para %s: %v", op, err)
			}
		}
		if validate != nil {
			if e := validate(&a); e != nil {
				return nil, e
			}
		}
		return fn(ctx, &a)
	}
}

// Ops devuelve las operaciones registradas, ordenadas.
func (r *Registry) Ops() []string {
	out := make([]string, 0, len(r.ops))
	for k := range r.ops {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Dispatch ejecuta un request y arma la respuesta. Nunca entra en pánico por input.
func (r *Registry) Dispatch(ctx context.Context, req *Request) Response {
	resp := Response{V: Version, ID: req.ID}
	h, ok := r.ops[req.Op]
	if !ok {
		resp.Error = Errf(CodeUnknownOp, "op desconocida: %q", req.Op)
		return resp
	}
	res, e := h(ctx, req.Args)
	if e != nil {
		resp.Error = e
		return resp
	}
	b, err := json.Marshal(res)
	if err != nil {
		resp.Error = Errf(CodeInternal, "no se pudo serializar el resultado")
		return resp
	}
	resp.OK = true
	resp.Result = b
	return resp
}
