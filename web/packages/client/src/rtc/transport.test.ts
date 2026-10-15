import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RtcTransport } from "./RtcTransport";
import { HybridTransport } from "./HybridTransport";
import type { Transport } from "../transport";
import type { Evt } from "../protocol";
import { FakePC, FakeWS, flush } from "./fakes.test-util";

const hello = { type: "hello", v: 1, server: "tangod/test", nas_id: "tg_1", ice_servers: [{ urls: ["stun:nas:3478"] }], methods: ["system.info", "auth.login"], topics: ["metrics", "jobs.changed"] };

function make() {
  const t = new RtcTransport({ url: "wss://nas/signal?v=1", WebSocketImpl: FakeWS as never, RTCPeerConnectionImpl: FakePC as never, minBackoffMs: 10, maxBackoffMs: 40 });
  return t;
}

/** Recorre el handshake completo hasta `connected` y devuelve los canales. */
async function handshake(t: RtcTransport) {
  const ws = FakeWS.last;
  ws.open();
  ws.recv(hello);
  ws.recv({ type: "signal", payload: { kind: "offer", sdp: "OFFER" } });
  await flush();
  const pc = FakePC.last;
  const rpc = pc.channel("rpc");
  const events = pc.channel("events");
  const metrics = pc.channel("metrics");
  rpc.open();
  await flush();
  expect(t.state()).toBe("connected");
  return { ws, pc, rpc, events, metrics };
}

beforeEach(() => {
  FakeWS.all = [];
  FakePC.all = [];
});
afterEach(() => vi.useRealTimers());

describe("RtcTransport", () => {
  it("responde la offer del NAS, hace trickle ICE y usa los ice_servers del hello", async () => {
    const t = make();
    const ws = FakeWS.last;
    expect(ws.url).toBe("wss://nas/signal?v=1");
    ws.open();
    ws.recv(hello);
    expect(ws.sent[0]).toEqual({ type: "signal", payload: { kind: "hello_client", client: "web" } });
    expect(t.isReal("system.info")).toBe(true);
    expect(t.isReal("files.list")).toBe(false);
    // un candidato que llega antes que la offer se encola
    ws.recv({ type: "signal", payload: { kind: "candidate", candidate: { candidate: "c1", sdpMid: "0", sdpMLineIndex: 0 } } });
    ws.recv({ type: "signal", payload: { kind: "offer", sdp: "OFFER" } });
    await flush();
    const pc = FakePC.last;
    expect(pc.config).toEqual({ iceServers: hello.ice_servers });
    expect(pc.remoteDescription).toEqual({ type: "offer", sdp: "OFFER" });
    expect(pc.added).toHaveLength(1);
    expect(ws.sent[1]).toEqual({ type: "signal", payload: { kind: "answer", sdp: "ANSWER-SDP" } });
    pc.onicecandidate?.({ candidate: { toJSON: () => ({ candidate: "mine" }) } });
    pc.onicecandidate?.({ candidate: null });
    expect(ws.sent.slice(2)).toEqual([
      { type: "signal", payload: { kind: "candidate", candidate: { candidate: "mine" } } },
      { type: "signal", payload: { kind: "candidate", candidate: null } },
    ]);
    t.close();
  });

  it("hace RPC por el canal rpc y reparte eventos y métricas por topic", async () => {
    const t = make();
    const { rpc, events, metrics } = await handshake(t);
    const p = t.call("system.info", undefined);
    await flush();
    const req = rpc.lastSent();
    expect(req).toMatchObject({ type: "req", method: "system.info" });
    rpc.recv({ type: "res", id: req.id, result: { hostname: "tango" } });
    await expect(p).resolves.toEqual({ hostname: "tango" });

    const jobs: Evt[] = [];
    const frames: Evt[] = [];
    t.subscribe("jobs.changed", (e) => jobs.push(e));
    t.subscribe("metrics", (e) => frames.push(e));
    events.recv({ type: "evt", topic: "jobs.changed", seq: 1, data: { id: "j1" } });
    events.recv({ type: "evt", topic: "jobs.changed", seq: 1, data: { id: "j1" } }); // duplicado
    events.recv({ type: "evt", topic: "jobs.changed", seq: 2, data: { id: "j2" } });
    metrics.recv({ t: 1, cpu: 0.5 });
    expect(jobs.map((e) => (e.data as { id: string }).id)).toEqual(["j1", "j2"]);
    expect(frames[0].data).toMatchObject({ cpu: 0.5 });
    t.close();
  });

  it("espera la conexión antes de llamar", async () => {
    const t = make();
    const p = t.call("system.info", undefined);
    const { rpc } = await handshake(t);
    await flush();
    const req = rpc.lastSent();
    rpc.recv({ type: "res", id: req.id, result: 1 });
    await expect(p).resolves.toBe(1);
    t.close();
  });

  it("reconecta con backoff si la PC falla, y rechaza lo pendiente", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const t = make();
    const { pc } = await handshake(t);
    const p = t.call("system.info", undefined).catch((e) => e);
    await Promise.resolve();
    pc.setState("failed");
    expect(t.state()).toBe("reconnecting");
    expect(pc.closed).toBe(true);
    expect((await p).code).toBe("DISCONNECTED");
    expect(FakeWS.all).toHaveLength(1);
    vi.advanceTimersByTime(20);
    expect(FakeWS.all).toHaveLength(2); // nuevo intento
    t.close();
    expect(t.state()).toBe("closed");
  });

  it("si el WS se cae antes de conectar, reintenta", () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const t = make();
    FakeWS.last.close();
    expect(t.state()).toBe("reconnecting");
    vi.advanceTimersByTime(20);
    expect(FakeWS.all).toHaveLength(2);
    t.close();
  });
});

describe("HybridTransport", () => {
  function fakeMock(): Transport & { emit: (topic: string, data: unknown) => void } {
    const subs = new Map<string, Set<(e: Evt) => void>>();
    return {
      kind: "mock",
      call: (async (m: string) => `mock:${m}`) as Transport["call"],
      subscribe: ((topic: string, fn: (e: Evt) => void) => {
        if (!subs.has(topic)) subs.set(topic, new Set());
        subs.get(topic)!.add(fn);
        return () => subs.get(topic)!.delete(fn);
      }) as Transport["subscribe"],
      state: () => "connected",
      onState: () => () => {},
      isReal: () => false,
      onCapabilities: () => () => {},
      onAuthLost: () => () => {},
      emit(topic, data) {
        subs.get(topic)?.forEach((f) => f({ topic, seq: 1, data } as Evt));
      },
    };
  }

  it("enruta por hello.methods y filtra topics de forma dinámica", async () => {
    const rtc = make();
    const mock = fakeMock();
    const h = new HybridTransport(rtc, mock, 1000);
    const got: string[] = [];
    h.subscribe("jobs.changed", (e) => got.push(`jobs:${e.data}`));
    h.subscribe("notice", (e) => got.push(`notice:${(e.data as unknown) as string}`));
    // antes del hello: la llamada espera
    const early = h.call("files.list", { path: "/" });
    const { rpc, events } = await handshake(rtc);
    await expect(early).resolves.toBe("mock:files.list");
    const real = h.call("system.info", undefined);
    await flush();
    rpc.recv({ type: "res", id: rpc.lastSent().id, result: "real" });
    await expect(real).resolves.toBe("real");
    expect(h.isReal("system.info")).toBe(true);
    // jobs.changed es real: el mock queda silenciado; notice no lo es: viene del mock
    mock.emit("jobs.changed", "mock");
    events.recv({ type: "evt", topic: "jobs.changed", seq: 1, data: "rtc" });
    mock.emit("notice", "mock");
    expect(got).toEqual(["jobs:rtc", "notice:mock"]);
    rtc.close();
  });

  it("si el NAS no aparece, cae al mock", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const rtc = make();
    const h = new HybridTransport(rtc, fakeMock(), 100);
    const p = h.call("system.info", undefined);
    vi.advanceTimersByTime(101);
    await expect(p).resolves.toBe("mock:system.info");
    rtc.close();
  });
});
