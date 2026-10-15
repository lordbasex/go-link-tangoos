import { describe, expect, it } from "vitest";
import { RtcTransfers, decodeFrame, encodeFrame, HIGH_WATER } from "./files";
import type { FilesDeps } from "./files";
import type { Evt, XferEvent } from "../protocol";

describe("codec de frames files.N", () => {
  it("ida y vuelta con offset > 4 GiB", () => {
    const data = new Uint8Array([1, 2, 3, 250]);
    const off = 5 * 2 ** 32 + 123_456;
    const f = decodeFrame(encodeFrame(0xdeadbeef, off, data))!;
    expect(f.xfer).toBe(0xdeadbeef);
    expect(f.offset).toBe(off);
    expect([...f.data]).toEqual([1, 2, 3, 250]);
  });

  it("cabecera big-endian: [u32 xfer][u64 offset]", () => {
    const b = new Uint8Array(encodeFrame(1, 2, new Uint8Array([9])));
    expect([...b]).toEqual([0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 2, 9]);
  });

  it("frames cortos se descartan", () => {
    expect(decodeFrame(new ArrayBuffer(5))).toBeNull();
  });
});

/** Canal binario de prueba con bufferedAmount controlable y eventos estándar. */
class FakeBinDC extends EventTarget {
  readyState: RTCDataChannelState = "open";
  bufferedAmount = 0;
  bufferedAmountLowThreshold = 0;
  frames: ArrayBuffer[] = [];
  constructor(readonly label: string, private readonly autoDrain = true) {
    super();
  }
  send(buf: ArrayBuffer) {
    this.frames.push(buf);
    this.bufferedAmount += buf.byteLength;
    if (this.autoDrain) queueMicrotask(() => this.drain());
  }
  drain() {
    if (!this.bufferedAmount) return;
    this.bufferedAmount = 0;
    this.dispatchEvent(new Event("bufferedamountlow"));
  }
}

function harness(ch: FakeBinDC) {
  const calls: { method: string; params: unknown }[] = [];
  const subs = new Set<(e: Evt<"files.xfer">) => void>();
  const deps: FilesDeps = {
    call: (async (method: string, params: unknown) => {
      calls.push({ method, params });
      if (method === "files.upload.begin") return { xfer: 7, channel: ch.label, chunk: 4 };
      if (method === "files.upload.end") return { name: "a.txt", path: "/F/a.txt", kind: "text", size: 10, mtime: "" };
      if (method === "files.download.begin") return { xfer: 9, channel: ch.label, size: 6, chunk: 4 };
      return {};
    }) as FilesDeps["call"],
    channel: (l) => (l === ch.label ? (ch as unknown as RTCDataChannel) : undefined),
    subscribe: (_t, fn) => (subs.add(fn), () => subs.delete(fn)),
  };
  const emit = (data: XferEvent) => subs.forEach((f) => f({ topic: "files.xfer", seq: 1, data }));
  return { deps, calls, emit };
}

const blobOf = (s: string) => Object.assign(new Blob([s]), { name: "a.txt" });

describe("RtcTransfers", () => {
  it("sube en chunks con offset y cierra con upload.end", async () => {
    const ch = new FakeBinDC("files.2");
    const { deps, calls } = harness(ch);
    const t = new RtcTransfers(deps);
    const progress: number[] = [];
    const e = await t.upload("/F", blobOf("0123456789"), (p) => progress.push(p.bytes));
    expect(e.path).toBe("/F/a.txt");
    const frames = ch.frames.map((b) => decodeFrame(b)!);
    expect(frames.map((f) => [f.xfer, f.offset, f.data.byteLength])).toEqual([
      [7, 0, 4],
      [7, 4, 4],
      [7, 8, 2],
    ]);
    expect(calls.map((c) => c.method)).toEqual(["files.upload.begin", "files.upload.end"]);
    expect(progress[progress.length - 1]).toBe(10);
  });

  it("respeta la contrapresión: espera bufferedamountlow por encima de 4 MiB", async () => {
    const ch = new FakeBinDC("files.0", false);
    const { deps } = harness(ch);
    const t = new RtcTransfers(deps);
    ch.bufferedAmount = HIGH_WATER + 1;
    let done = false;
    const p = t.upload("/F", blobOf("ab"), undefined).then(() => (done = true));
    await new Promise((r) => setTimeout(r, 10));
    expect(ch.frames.length).toBe(0); // bloqueado esperando que baje el buffer
    ch.drain();
    await new Promise((r) => setTimeout(r, 10));
    ch.drain(); // vacía lo enviado para que pueda cerrar
    await p;
    expect(done).toBe(true);
    expect(ch.frames.length).toBe(1);
  });

  it("cancelar avisa al NAS con files.xfer.cancel", async () => {
    const ch = new FakeBinDC("files.0", false);
    const { deps, calls } = harness(ch);
    const t = new RtcTransfers(deps);
    ch.bufferedAmount = HIGH_WATER + 1;
    const ac = new AbortController();
    const p = t.upload("/F", blobOf("abcdef"), undefined, { signal: ac.signal });
    await new Promise((r) => setTimeout(r, 5));
    ac.abort();
    await expect(p).rejects.toMatchObject({ code: "CANCELLED" });
    expect(calls.some((c) => c.method === "files.xfer.cancel")).toBe(true);
  });

  it("baja frames desordenados y arma el Blob en orden", async () => {
    const ch = new FakeBinDC("files.1");
    const { deps } = harness(ch);
    const t = new RtcTransfers(deps);
    const p = t.download("/F/foto.jpg");
    await new Promise((r) => setTimeout(r, 0));
    t.onFrame(encodeFrame(9, 4, new TextEncoder().encode("ef")));
    t.onFrame(encodeFrame(99, 0, new TextEncoder().encode("xx"))); // otro xfer: se ignora
    t.onFrame(encodeFrame(9, 0, new TextEncoder().encode("abcd")));
    const blob = (await p)!;
    expect(await blob.text()).toBe("abcdef");
    expect(blob.type).toBe("image/jpeg");
  });

  it("un evento error corta la bajada", async () => {
    const ch = new FakeBinDC("files.1");
    const { deps, emit } = harness(ch);
    const t = new RtcTransfers(deps);
    const p = t.download("/F/x.bin");
    await new Promise((r) => setTimeout(r, 0));
    emit({ xfer: 9, state: "error", bytes: 0, error: "PERMISSION_DENIED" });
    await expect(p).rejects.toMatchObject({ code: "XFER_FAILED" });
  });

  it("con sink escribe en streaming y resuelve null", async () => {
    const ch = new FakeBinDC("files.1");
    const { deps } = harness(ch);
    const t = new RtcTransfers(deps);
    const writes: [number, string][] = [];
    let closed = false;
    const p = t.download("/F/x.bin", undefined, { sink: { write: (d, o) => void writes.push([o, new TextDecoder().decode(d)]), close: () => void (closed = true) } });
    await new Promise((r) => setTimeout(r, 0));
    t.onFrame(encodeFrame(9, 0, new TextEncoder().encode("abcd")));
    t.onFrame(encodeFrame(9, 4, new TextEncoder().encode("ef")));
    expect(await p).toBeNull();
    expect(writes).toEqual([
      [0, "abcd"],
      [4, "ef"],
    ]);
    expect(closed).toBe(true);
  });
});
