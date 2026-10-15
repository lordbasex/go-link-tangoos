// Lado página del Service Worker (/sw.js, protocol §13): registra enlaces /dc/stream y
// /dc/download que el SW sirve pidiendo los bytes acá, y acá se bajan por el DataChannel
// en pedazos con files.download.begin {offset, length}. Sin SW (contexto no seguro o
// navegador sin soporte) `dcReady()` es false y las apps usan Blob como antes.
import { mimeFor } from "./rtc/files";
import type { Transfers } from "./transfers";

/** Tamaño de cada pedazo pedido al NAS. */
export const DC_CHUNK = 2 * 1024 * 1024;
/** Cuánto vive un enlace sin uso. */
const TTL_MS = 6 * 3600_000;

interface Entry {
  tr: Transfers;
  path: string;
  name: string;
  size: number;
  snapshot?: string;
  mime: string;
  touched: number;
  onProgress?: (sent: number, total: number) => void;
  onDone?: (err?: Error) => void;
}

const links = new Map<string, Entry>();
let installed = false;

/** Rango pedido por el SW → [start, end) dentro del archivo. null = no satisfacible. */
export function resolveRange(size: number, r: { start: number | null; end: number | null } | null): [number, number] | null {
  if (!r) return [0, size];
  let start: number;
  let end: number;
  if (r.start === null) {
    const n = Math.min(r.end ?? 0, size);
    start = size - n;
    end = size;
  } else {
    start = r.start;
    end = r.end === null ? size : Math.min(r.end + 1, size);
  }
  if (start < 0 || start > size || end < start || (start === size && size > 0)) return null;
  return [start, end];
}

function token(): string {
  const b = crypto.getRandomValues(new Uint8Array(18));
  return btoa(String.fromCharCode(...b)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function onMessage(e: MessageEvent) {
  const d = e.data as { type?: string; token?: string; range?: { start: number | null; end: number | null } | null };
  if (d?.type !== "tango.dc" || !e.ports[0]) return;
  const port = e.ports[0];
  const ent = links.get(d.token ?? "");
  if (!ent || Date.now() - ent.touched > TTL_MS) {
    port.postMessage({ ok: false, status: 404 });
    return;
  }
  ent.touched = Date.now();
  const rg = resolveRange(ent.size, d.range ?? null);
  if (!rg) {
    port.postMessage({ ok: false, status: 416 });
    return;
  }
  const [start, end] = rg;
  let pos = start;
  const ac = new AbortController();
  // Un pedazo adelantado: mientras el navegador consume uno, ya viene el siguiente.
  const fetchAt = (off: number) => {
    const len = Math.min(DC_CHUNK, end - off);
    return ent.tr.download(ent.path, undefined, { range: { offset: off, length: len }, snapshot: ent.snapshot, signal: ac.signal }).then((b) => b!.arrayBuffer());
  };
  let next: Promise<ArrayBuffer> | null = pos < end ? fetchAt(pos) : null;
  next?.catch(() => {});
  port.onmessage = async (m) => {
    const q = m.data as { pull?: boolean; cancel?: boolean };
    if (q.cancel) {
      ac.abort();
      ent.onDone?.(new Error("cancelado"));
      return;
    }
    if (!q.pull) return;
    if (!next) {
      port.postMessage({ done: true });
      ent.onDone?.();
      return;
    }
    try {
      const buf = await next;
      pos += buf.byteLength;
      next = pos < end ? fetchAt(pos) : null;
      next?.catch(() => {});
      ent.onProgress?.(pos - start, end - start);
      port.postMessage({ chunk: buf, done: !next }, [buf]);
      if (!next) ent.onDone?.();
    } catch (err) {
      port.postMessage({ error: (err as Error).message || "error" });
      ent.onDone?.(err as Error);
    }
  };
  port.postMessage({ ok: true, size: ent.size, start, end, mime: ent.mime, name: ent.name });
}

/** Registra /sw.js (solo en contexto seguro). Llamarlo una vez al arrancar. */
export function installDCStream(): void {
  if (installed || typeof navigator === "undefined" || !("serviceWorker" in navigator) || !window.isSecureContext) return;
  installed = true;
  navigator.serviceWorker.addEventListener("message", onMessage);
  navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(() => {
    installed = false;
  });
}

/** ¿Hay un Service Worker controlando esta página? */
export function dcReady(): boolean {
  return typeof navigator !== "undefined" && !!navigator.serviceWorker?.controller;
}

export interface DCLinkOptions {
  name?: string;
  snapshot?: string;
  mime?: string;
  onProgress?: (sent: number, total: number) => void;
  onDone?: (err?: Error) => void;
}

/** Crea un enlace /dc/<kind>/<token>/<nombre> para un archivo del NAS. */
export function dcLink(kind: "stream" | "download", tr: Transfers, path: string, size: number, o: DCLinkOptions = {}): { url: string; revoke: () => void } {
  const name = o.name ?? path.slice(path.lastIndexOf("/") + 1);
  const t = token();
  links.set(t, { tr, path, name, size, snapshot: o.snapshot, mime: o.mime ?? mimeFor(name), touched: Date.now(), onProgress: o.onProgress, onDone: o.onDone });
  return { url: `/dc/${kind}/${t}/${encodeURIComponent(name)}`, revoke: () => links.delete(t) };
}
