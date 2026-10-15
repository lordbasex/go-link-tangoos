import { describe, expect, it } from "vitest";
import { mergeIce } from "./socket";
import { endpoint, humanSignalError, parseICEServers, SignalError } from "./signalhub";

describe("señalización", () => {
  it("usa los ICE de signalhub y descarta los STUN de la LAN del NAS", () => {
    const sig = parseICEServers([{ urls: ["stun:stun.go-link.org:3478"] }, { urls: "turn:turn.go-link.org:3478", username: "u", credential: "c" }, { urls: ["http://x"] }]);
    expect(sig.length).toBe(2);
    const out = mergeIce(sig, [{ urls: ["stun:192.168.1.50:3478"] }, { urls: ["stun:tango.local:3478"] }, { urls: ["stun:nas.example.com:3478"] }]);
    expect(out.map((s) => (Array.isArray(s.urls) ? s.urls[0] : s.urls))).toEqual(["stun:stun.go-link.org:3478", "turn:turn.go-link.org:3478", "stun:nas.example.com:3478"]);
  });
  it("agrega ?v=1 y traduce los errores fijos", () => {
    expect(endpoint("wss://signal.go-link.org/ws")).toBe("wss://signal.go-link.org/ws?v=1");
    expect(endpoint("wss://signal.go-link.org/ws?v=1")).toBe("wss://signal.go-link.org/ws?v=1");
    expect(humanSignalError(new SignalError("app not allowed", true))).toMatch(/ALLOWED_APPS/);
    expect(humanSignalError(new SignalError("algo nuevo", true))).toBe("algo nuevo");
  });
});

/** WebSocket falso que hace de signalhub y del NAS del otro lado. */
function fakeHub(reply: (env: Record<string, unknown>) => Record<string, unknown>[]) {
  const sent: Record<string, unknown>[] = [];
  let last: { onmessage: ((e: { data: string }) => void) | null } | null = null;
  class WS {
    readyState = 1;
    onmessage: ((e: { data: string }) => void) | null = null;
    onclose: (() => void) | null = null;
    onerror: (() => void) | null = null;
    constructor() {
      last = this;
      setTimeout(() => this.push({ type: "hello", peer_id: "me", ice_servers: [{ urls: ["stun:stun.go-link.org:3478"] }] }));
    }
    push(m: Record<string, unknown>) {
      this.onmessage?.({ data: JSON.stringify(m) });
    }
    send(raw: string) {
      const env = JSON.parse(raw);
      sent.push(env);
      for (const r of reply(env)) setTimeout(() => this.push(r));
    }
    close() {
      this.readyState = 3;
      this.onclose?.();
    }
  }
  return { WS: WS as unknown as typeof WebSocket, sent, nas: (m: Record<string, unknown>) => (last as unknown as WS).push(m) };
}

describe("joinSignal", () => {
  it("reach → paired, y los payloads del NAS pasan tal cual (sin envoltura)", async () => {
    const hub = fakeHub((env) => (env.type === "reach" ? [{ type: "paired", remote: "nas-peer" }] : []));
    const { joinSignal } = await import("./socket");
    const p = await joinSignal({ mode: "reach", deviceId: "11111111-1111-4111-8111-111111111111" }, { signalUrl: "wss://x/ws", WebSocketImpl: hub.WS });
    expect(hub.sent[0]).toMatchObject({ type: "reach", app: "tangoos", device_id: "11111111-1111-4111-8111-111111111111" });
    expect(p.iceServers.length).toBe(1);
    const got: unknown[] = [];
    p.onPayload((x) => got.push(x));
    hub.nas({ type: "signal", from: "nas-peer", payload: { kind: "offer", sdp: "v=0" } });
    hub.nas({ type: "signal", from: "otro", payload: { kind: "offer", sdp: "x" } });
    expect(got).toEqual([{ kind: "offer", sdp: "v=0" }]);
    p.send({ kind: "answer", sdp: "a" });
    expect(hub.sent.at(-1)).toEqual({ type: "signal", to: "nas-peer", payload: { kind: "answer", sdp: "a" } });
    let closed = false;
    p.onClose(() => (closed = true));
    hub.nas({ type: "peer_left", from: "nas-peer" });
    expect(closed).toBe(true);
  });
  it("un código vencido rechaza con el error fijo de signalhub", async () => {
    const hub = fakeHub((env) => (env.type === "claim" ? [{ type: "error", error: "invalid or expired code" }] : []));
    const { joinSignal } = await import("./socket");
    await expect(joinSignal({ mode: "claim", code: "123 456 789" }, { signalUrl: "wss://x/ws", WebSocketImpl: hub.WS })).rejects.toThrow("invalid or expired code");
    expect(hub.sent[0]).toMatchObject({ type: "claim", code: "123456789" });
  });
});
