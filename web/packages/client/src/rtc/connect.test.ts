import { beforeEach, describe, expect, it } from "vitest";
import { rtcFromSignal } from "./RtcTransport";
import type { SignalProvider } from "./RtcTransport";
import { BundleReceiver, sha256Hex } from "./bundle";
import { FakePC, flush } from "./fakes.test-util";

function frame(id: number, off: number, data: number[]) {
  const b = new ArrayBuffer(12 + data.length);
  const v = new DataView(b);
  v.setUint32(0, id);
  v.setBigUint64(4, BigInt(off));
  new Uint8Array(b, 12).set(data);
  return b;
}

describe("BundleReceiver", () => {
  it("arma el archivo con frames desordenados y frames que llegan antes del pedido", async () => {
    const r = new BundleReceiver();
    r.onFrame(frame(7, 3, [4, 5])); // llega antes que expect()
    const p = r.expect(7, 5);
    r.onFrame(frame(7, 0, [1, 2, 3]));
    const got = await p;
    expect(Array.from(got)).toEqual([1, 2, 3, 4, 5]);
    expect(await sha256Hex(new Uint8Array([1, 2, 3]))).toBe("039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81");
  });

  it("falla lo pendiente al cortarse la conexión", async () => {
    const r = new BundleReceiver();
    const p = r.expect(1, 10);
    r.failAll();
    await expect(p).rejects.toThrow();
  });
});

class FakeProvider implements SignalProvider {
  static last: FakeProvider;
  iceServers = [{ urls: ["turn:relay:3478"], username: "u", credential: "c" }];
  sent: unknown[] = [];
  private payloadFns: ((p: unknown) => void)[] = [];
  closed = false;
  constructor() {
    FakeProvider.last = this;
  }
  send(p: unknown) {
    this.sent.push(p);
  }
  onPayload(fn: (p: unknown) => void) {
    this.payloadFns.push(fn);
    return () => {};
  }
  onClose() {
    return () => {};
  }
  close() {
    this.closed = true;
  }
  deliver(p: unknown) {
    this.payloadFns.forEach((f) => f(p));
  }
}

describe("rtcFromSignal", () => {
  beforeEach(() => {
    FakePC.all = [];
  });

  it("un open repetido del canal rpc no corre dos veces el paso de vínculo", async () => {
    let runs = 0;
    const t = rtcFromSignal(async () => new FakeProvider(), {
      RTCPeerConnectionImpl: FakePC as never,
      tokenStore: { get: () => null, set: () => {} },
      remote: { beforeAuth: async () => void runs++ },
    });
    await flush();
    FakeProvider.last.deliver({ kind: "offer", sdp: "OFFER" });
    await flush();
    const rpc = FakePC.last.channel("rpc");
    rpc.open();
    rpc.onopen?.(); // el navegador puede entregar el canal ya abierto y además disparar `open`
    await flush();
    const hellos = rpc.sent.map((m) => JSON.parse(m)).filter((m) => m.method === "connect.hello");
    expect(hellos).toHaveLength(1);
    rpc.recv({ type: "res", id: hellos[0].id, result: { methods: [], topics: [] } });
    await flush();
    expect(runs).toBe(1);
    t.close();
  });

  it("usa los ICE de signalhub, contesta por el provider y pide connect.hello antes del vínculo", async () => {
    const order: string[] = [];
    const t = rtcFromSignal(async () => new FakeProvider(), {
      RTCPeerConnectionImpl: FakePC as never,
      tokenStore: { get: () => null, set: () => {} },
      remote: {
        beforeAuth: async (call) => {
          order.push("beforeAuth");
          await call("connect.resume", { linkId: "l", token: "t" });
        },
      },
    });
    await flush();
    const prov = FakeProvider.last;
    prov.deliver({ kind: "offer", sdp: "OFFER" });
    await flush();
    const pc = FakePC.last;
    expect(pc.config).toEqual({ iceServers: prov.iceServers });
    expect(prov.sent).toEqual([{ kind: "answer", sdp: "ANSWER-SDP" }]); // sin hello_client
    const rpc = pc.channel("rpc");
    rpc.open();
    await flush();
    const hello = rpc.lastSent();
    expect(hello.method).toBe("connect.hello");
    rpc.recv({ type: "res", id: hello.id, result: { methods: ["system.info", "bundle.get"], topics: ["metrics"] } });
    await flush();
    expect(order).toEqual(["beforeAuth"]);
    const resume = rpc.lastSent();
    expect(resume.method).toBe("connect.resume");
    rpc.recv({ type: "res", id: resume.id, result: {} });
    await flush();
    expect(t.state()).toBe("connected");
    expect(t.isReal("bundle.get" as never)).toBe(true);
    t.close();
    expect(prov.closed).toBe(true);
  });

  it("si el vínculo falla, cierra y avisa", async () => {
    let err: unknown = null;
    const t = rtcFromSignal(async () => new FakeProvider(), {
      RTCPeerConnectionImpl: FakePC as never,
      tokenStore: { get: () => null, set: () => {} },
      remote: {
        beforeAuth: async () => {
          throw new Error("el NAS no probó el vínculo");
        },
        onError: (e) => (err = e),
      },
    });
    await flush();
    FakeProvider.last.deliver({ kind: "offer", sdp: "OFFER" });
    await flush();
    const rpc = FakePC.last.channel("rpc");
    rpc.open();
    await flush();
    rpc.recv({ type: "res", id: rpc.lastSent().id, result: { methods: [], topics: [] } });
    await flush();
    await flush();
    expect(String(err)).toContain("no probó");
    expect(t.state()).toBe("closed");
  });
});
