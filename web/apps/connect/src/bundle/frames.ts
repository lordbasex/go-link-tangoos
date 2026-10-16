// Recolector de frames binarios [u32 xfer][u64 offset][datos] (mismo framing que files.N, protocol §10).
// Escucha uno o más DataChannels (bundle, files.*) y arma cada transferencia por xfer.
import { decodeFrame, t } from "@tango/client";

interface Pending {
  size: number;
  buf: Uint8Array;
  got: number;
  onProgress?: (got: number, size: number) => void;
  resolve: (b: Uint8Array) => void;
  reject: (e: Error) => void;
  timer: ReturnType<typeof setTimeout>;
}

export class FrameCollector {
  private pending = new Map<number, Pending>();
  /** Frames de un xfer que todavía no se registró (el NAS puede mandar antes de que llegue la respuesta rpc). */
  private early = new Map<number, ArrayBuffer[]>();
  private readonly idleMs: number;

  constructor(idleMs = 30_000) {
    this.idleMs = idleMs;
  }

  /** Engancha un canal binario. Devuelve la función para soltarlo. */
  attach(ch: RTCDataChannel): () => void {
    ch.binaryType = "arraybuffer";
    const on = (e: MessageEvent) => {
      if (e.data instanceof ArrayBuffer) this.onFrame(e.data);
    };
    ch.addEventListener("message", on);
    return () => ch.removeEventListener("message", on);
  }

  onFrame(buf: ArrayBuffer) {
    const f = decodeFrame(buf);
    if (!f) return;
    const p = this.pending.get(f.xfer);
    if (!p) {
      const list = this.early.get(f.xfer) ?? [];
      if (list.length < 4096) list.push(buf.slice(0));
      this.early.set(f.xfer, list);
      return;
    }
    if (f.offset + f.data.byteLength > p.size) return p.reject(new Error(t("frame fuera de rango")));
    p.buf.set(f.data, f.offset);
    p.got += f.data.byteLength;
    p.onProgress?.(p.got, p.size);
    this.bump(f.xfer, p);
    if (p.got >= p.size) this.finish(f.xfer);
  }

  /** Espera `size` bytes del xfer. */
  expect(xfer: number, size: number, onProgress?: (got: number, size: number) => void): Promise<Uint8Array> {
    return new Promise((resolve, reject) => {
      const p: Pending = { size, buf: new Uint8Array(size), got: 0, onProgress, resolve, reject, timer: setTimeout(() => {}, 0) };
      this.pending.set(xfer, p);
      this.bump(xfer, p);
      if (size === 0) return this.finish(xfer);
      for (const b of this.early.get(xfer) ?? []) this.onFrame(b);
      this.early.delete(xfer);
    });
  }

  failAll(err: Error) {
    for (const [id, p] of this.pending) {
      clearTimeout(p.timer);
      p.reject(err);
      this.pending.delete(id);
    }
  }

  private bump(xfer: number, p: Pending) {
    clearTimeout(p.timer);
    p.timer = setTimeout(() => {
      this.pending.delete(xfer);
      p.reject(new Error(t("La transferencia se quedó sin datos")));
    }, this.idleMs);
  }

  private finish(xfer: number) {
    const p = this.pending.get(xfer);
    if (!p) return;
    clearTimeout(p.timer);
    this.pending.delete(xfer);
    p.resolve(p.buf);
  }
}
