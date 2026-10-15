import { afterEach, describe, expect, it, vi } from "vitest";
import { RpcClient } from "./rpc";
import { RpcError } from "../protocol";
import { FakeDC } from "./fakes.test-util";

function open() {
  const dc = new FakeDC("rpc");
  dc.readyState = "open";
  const rpc = new RpcClient(1000);
  rpc.bind(dc);
  return { dc, rpc };
}

afterEach(() => vi.useRealTimers());

describe("RpcClient", () => {
  it("numera los pedidos y resuelve por id, en cualquier orden", async () => {
    const { dc, rpc } = open();
    const a = rpc.call("system.info", undefined);
    const b = rpc.call("files.list", { path: "/" });
    expect(dc.sent.map((s) => JSON.parse(s))).toEqual([
      { type: "req", id: 1, method: "system.info", params: null },
      { type: "req", id: 2, method: "files.list", params: { path: "/" } },
    ]);
    rpc.handle(JSON.stringify({ type: "res", id: 2, result: ["b"] }));
    rpc.handle(JSON.stringify({ type: "res", id: 1, result: { hostname: "tango" } }));
    await expect(a).resolves.toEqual({ hostname: "tango" });
    await expect(b).resolves.toEqual(["b"]);
    expect(rpc.inFlight).toBe(0);
  });

  it("convierte `err` en RpcError con el código estable", async () => {
    const { rpc } = open();
    const p = rpc.call("auth.login", { user: "x", password: "y" });
    rpc.handle(JSON.stringify({ type: "err", id: 1, error: { code: "INVALID_CREDENTIALS", message: "Usuario o contraseña incorrectos" } }));
    const e = await p.catch((x) => x);
    expect(e).toBeInstanceOf(RpcError);
    expect(e.code).toBe("INVALID_CREDENTIALS");
    expect(e.message).toBe("Usuario o contraseña incorrectos");
  });

  it("vence por timeout", async () => {
    vi.useFakeTimers();
    const { rpc } = open();
    const p = rpc.call("system.info", undefined).catch((x) => x);
    vi.advanceTimersByTime(1001);
    expect((await p).code).toBe("TIMEOUT");
    expect(rpc.inFlight).toBe(0);
  });

  it("cancela con AbortSignal y avisa al servidor", async () => {
    const { dc, rpc } = open();
    const ac = new AbortController();
    const p = rpc.call("files.search", { q: "x" }, { signal: ac.signal }).catch((x) => x);
    ac.abort();
    expect((await p).code).toBe("CANCELLED");
    expect(dc.lastSent()).toEqual({ type: "cancel", id: 1 });
    // una respuesta tardía no rompe nada
    expect(rpc.handle(JSON.stringify({ type: "res", id: 1, result: [] }))).toBe(false);
  });

  it("rechaza sin canal y al caerse el canal", async () => {
    const rpc = new RpcClient();
    expect((await rpc.call("system.info", undefined).catch((x) => x)).code).toBe("DISCONNECTED");
    const o = open();
    const p = o.rpc.call("system.info", undefined).catch((x) => x);
    o.rpc.failAll();
    expect((await p).code).toBe("DISCONNECTED");
  });

  it("ignora basura y respuestas desconocidas", () => {
    const { rpc } = open();
    expect(rpc.handle("no-json")).toBe(false);
    expect(rpc.handle(JSON.stringify({ type: "res", id: 99, result: 1 }))).toBe(false);
    expect(rpc.handle(JSON.stringify({ type: "evt" }))).toBe(false);
  });
});
