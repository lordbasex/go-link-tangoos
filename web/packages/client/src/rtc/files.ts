// Transferencias sobre los canales binarios files.0…3 (protocol §10).
// Frame: [u32 xfer BE][u64 offset BE][datos]. El NAS elige el canal en *.begin.
import { RpcError } from "../protocol";
import type { Evt, FileEntry, Method, Params, Result, XferEvent } from "../protocol";
import { rateMeter } from "../transfers";
import type { DownloadOptions, Transfers, UploadOptions, XferProgress } from "../transfers";

export const FRAME_HEADER = 12;
const U32 = 0x1_0000_0000;
/** Máximo en vuelo por canal antes de esperar `bufferedamountlow` (protocol §10: ≤ 4 MiB). */
export const HIGH_WATER = 4 * 1024 * 1024;
export const LOW_WATER = 1024 * 1024;
/** Tope de frames guardados de bajadas todavía desconocidas. */
const EARLY_MAX = 8 * 1024 * 1024;
const PROGRESS_EVERY_MS = 100;
/** Si el NAS dice "done" pero faltan bytes, cuánto esperar a que lleguen por el canal de datos. */
const LATE_DATA_MS = 10_000;

export function encodeFrame(xfer: number, offset: number, data: Uint8Array): ArrayBuffer {
  const buf = new ArrayBuffer(FRAME_HEADER + data.byteLength);
  const v = new DataView(buf);
  v.setUint32(0, xfer >>> 0);
  v.setUint32(4, Math.floor(offset / U32));
  v.setUint32(8, offset % U32);
  new Uint8Array(buf, FRAME_HEADER).set(data);
  return buf;
}

export function decodeFrame(buf: ArrayBuffer): { xfer: number; offset: number; data: Uint8Array } | null {
  if (buf.byteLength < FRAME_HEADER) return null;
  const v = new DataView(buf);
  return { xfer: v.getUint32(0), offset: v.getUint32(4) * U32 + v.getUint32(8), data: new Uint8Array(buf, FRAME_HEADER) };
}

const mimes: Record<string, string> = {
  jpg: "image/jpeg", jpeg: "image/jpeg", png: "image/png", gif: "image/gif", webp: "image/webp", avif: "image/avif", svg: "image/svg+xml",
  mp4: "video/mp4", webm: "video/webm", mov: "video/quicktime", mkv: "video/x-matroska",
  mp3: "audio/mpeg", m4a: "audio/mp4", flac: "audio/flac", wav: "audio/wav", ogg: "audio/ogg",
  pdf: "application/pdf", txt: "text/plain", md: "text/markdown", json: "application/json",
};
export const mimeFor = (name: string) => mimes[name.slice(name.lastIndexOf(".") + 1).toLowerCase()] ?? "application/octet-stream";

export interface FilesDeps {
  call<M extends Method>(method: M, params: Params<M>, opts?: { signal?: AbortSignal }): Promise<Result<M>>;
  channel(label: string): RTCDataChannel | undefined;
  subscribe(topic: "files.xfer", fn: (e: Evt<"files.xfer">) => void): () => void;
}

const cancelled = () => new RpcError("CANCELLED", "Transferencia cancelada");
const disconnected = () => new RpcError("DISCONNECTED", "Se cortó la conexión con el NAS durante la transferencia");

function waitLow(ch: RTCDataChannel, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const done = (err?: Error) => {
      ch.removeEventListener("bufferedamountlow", onLow);
      ch.removeEventListener("close", onClose);
      signal?.removeEventListener("abort", onAbort);
      if (err) reject(err);
      else resolve();
    };
    const onLow = () => done();
    const onClose = () => done(disconnected());
    const onAbort = () => done(cancelled());
    ch.addEventListener("bufferedamountlow", onLow);
    ch.addEventListener("close", onClose);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

export class RtcTransfers implements Transfers {
  private downloads = new Map<number, { frame: (offset: number, data: Uint8Array) => void; fail: (e: Error) => void }>();

  constructor(private readonly d: FilesDeps) {}

  /** Frames de bajadas que todavía no conocemos (llegan antes que la respuesta de begin). */
  private early = new Map<number, { offset: number; data: Uint8Array }[]>();
  private earlyBytes = 0;

  /** Frame binario recibido por cualquiera de los canales files.N. */
  onFrame(buf: ArrayBuffer) {
    const f = decodeFrame(buf);
    if (!f) return;
    const d = this.downloads.get(f.xfer);
    if (d) return d.frame(f.offset, f.data);
    // La respuesta de files.download.begin viaja por `rpc` y los datos por files.N: el
    // primer frame puede ganarle. Se guardan (con tope) hasta que la bajada se registre.
    if (this.earlyBytes + f.data.byteLength > EARLY_MAX) return;
    const list = this.early.get(f.xfer) ?? [];
    list.push({ offset: f.offset, data: f.data });
    this.early.set(f.xfer, list);
    this.earlyBytes += f.data.byteLength;
  }

  private takeEarly(xfer: number) {
    const list = this.early.get(xfer) ?? [];
    this.early.delete(xfer);
    for (const x of list) this.earlyBytes -= x.data.byteLength;
    return list;
  }

  /** Se cayó la PeerConnection: todas las bajadas en curso fallan. */
  failAll() {
    for (const d of [...this.downloads.values()]) d.fail(disconnected());
  }

  async upload(dir: string, file: Blob & { name: string }, onProgress?: (p: XferProgress) => void, opts: UploadOptions = {}): Promise<FileEntry> {
    const { signal } = opts;
    if (signal?.aborted) throw cancelled();
    const total = file.size;
    const b = await this.d.call("files.upload.begin", { dir, name: file.name, size: total, overwrite: !!opts.overwrite }, { signal });
    const ch = this.d.channel(b.channel);
    const cancel = () => void this.d.call("files.xfer.cancel", { xfer: b.xfer }).catch(() => {});
    if (!ch || ch.readyState !== "open") {
      cancel();
      throw disconnected();
    }
    ch.bufferedAmountLowThreshold = LOW_WATER;
    const chunk = b.chunk > 0 ? b.chunk : 64 * 1024;
    const meter = rateMeter();
    let lastReport = 0;
    const report = (bytes: number, force = false) => {
      const now = performance.now();
      const rate = meter(bytes);
      if (force || now - lastReport >= PROGRESS_EVERY_MS) {
        lastReport = now;
        onProgress?.({ bytes, total, rate });
      }
    };
    try {
      for (let off = 0; off < total; off += chunk) {
        if (signal?.aborted) throw cancelled();
        if (ch.readyState !== "open") throw disconnected();
        if (ch.bufferedAmount > HIGH_WATER) await waitLow(ch, signal);
        const data = new Uint8Array(await file.slice(off, Math.min(total, off + chunk)).arrayBuffer());
        ch.send(encodeFrame(b.xfer, off, data));
        report(Math.max(0, off + data.byteLength - ch.bufferedAmount));
      }
      // Todo entregado a SCTP antes de pedir el cierre: el NAS igual espera hasta tener `size` bytes.
      while (ch.bufferedAmount > 0) {
        if (ch.readyState !== "open") throw disconnected();
        ch.bufferedAmountLowThreshold = 0;
        await waitLow(ch, signal);
      }
      report(total, true);
    } catch (e) {
      cancel();
      throw e;
    } finally {
      ch.bufferedAmountLowThreshold = LOW_WATER;
    }
    return this.d.call("files.upload.end", { xfer: b.xfer });
  }

  async download(path: string, onProgress?: (p: XferProgress) => void, opts: DownloadOptions = {}): Promise<Blob | null> {
    const { signal, sink } = opts;
    if (signal?.aborted) throw cancelled();
    const params: { path: string; snapshot?: string; offset?: number; length?: number } = { path };
    if (opts.snapshot) params.snapshot = opts.snapshot;
    if (opts.range) {
      params.offset = opts.range.offset;
      params.length = opts.range.length;
    }
    const b = await this.d.call("files.download.begin", params, { signal });
    opts.onSize?.(b.size);
    // Con rango llegan `length` bytes desde `offset` (offsets absolutos en los frames).
    const start = b.offset ?? 0;
    const want = b.length ?? b.size;
    const name = path.slice(path.lastIndexOf("/") + 1);
    return new Promise<Blob | null>((resolve, reject) => {
      const parts: { off: number; data: Uint8Array }[] = [];
      let got = 0;
      let settled = false;
      let writes: Promise<void> = Promise.resolve();
      let lateTimer: ReturnType<typeof setTimeout> | undefined;
      const meter = rateMeter();
      let lastReport = 0;

      const cleanup = () => {
        settled = true;
        this.downloads.delete(b.xfer);
        off();
        clearTimeout(lateTimer);
        signal?.removeEventListener("abort", onAbort);
      };
      const fail = (e: Error) => {
        if (settled) return;
        cleanup();
        void sink?.abort?.(e);
        reject(e);
      };
      const complete = () => {
        if (settled) return;
        cleanup();
        onProgress?.({ bytes: want, total: want, rate: meter(want) });
        if (sink) {
          writes.then(() => sink.close()).then(() => resolve(null), reject);
        } else {
          parts.sort((x, y) => x.off - y.off);
          resolve(new Blob(parts.map((p) => p.data as BlobPart), { type: mimeFor(name) }));
        }
      };
      const onAbort = () => {
        void this.d.call("files.xfer.cancel", { xfer: b.xfer }).catch(() => {});
        fail(cancelled());
      };
      const off = this.d.subscribe("files.xfer", (e) => {
        const x: XferEvent = e.data;
        if (x.xfer !== b.xfer || settled) return;
        if (x.state === "error") fail(new RpcError("XFER_FAILED", x.error || "El NAS cortó la transferencia"));
        else if (x.state === "done") {
          if (got >= want) complete();
          else lateTimer = setTimeout(() => fail(new RpcError("XFER_INCOMPLETE", "Faltaron datos al terminar la descarga")), LATE_DATA_MS);
        }
      });
      signal?.addEventListener("abort", onAbort, { once: true });

      const frame = (offset: number, data: Uint8Array) => {
        if (settled) return;
        got += data.byteLength;
        const rel = offset - start;
        if (sink) writes = writes.then(() => sink.write(data, rel));
        else parts.push({ off: rel, data });
        const now = performance.now();
        const rate = meter(got);
        if (now - lastReport >= PROGRESS_EVERY_MS) {
          lastReport = now;
          onProgress?.({ bytes: got, total: want, rate });
        }
        if (got >= want) complete();
      };
      this.downloads.set(b.xfer, { frame, fail });
      for (const x of this.takeEarly(b.xfer)) frame(x.offset, x.data);
      if (want === 0) complete();
    });
  }
}
