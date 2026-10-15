// Transferencias de archivos (protocol §10). Capacidad opcional del Transport:
// RtcTransport la implementa sobre los canales binarios files.0…3, MockTransport la simula.
import type { FileEntry } from "./protocol";

export interface XferProgress {
  /** Bytes confirmados (subida: entregados a SCTP; bajada: recibidos). */
  bytes: number;
  total: number;
  /** Bytes por segundo, promedio móvil. */
  rate: number;
}

/** Destino en streaming de una bajada (File System Access API u otro). */
export interface DownloadSink {
  write(data: Uint8Array, offset: number): Promise<void> | void;
  close(): Promise<void> | void;
  abort?(reason?: unknown): Promise<void> | void;
}

export interface UploadOptions {
  overwrite?: boolean;
  signal?: AbortSignal;
}

export interface DownloadOptions {
  signal?: AbortSignal;
  /** Si se pasa, los datos se escriben acá y la promesa resuelve `null` (no arma un Blob en memoria). */
  sink?: DownloadSink;
  /** Solo un rango del archivo (§13). Con sink, `offset` del write es relativo al inicio del rango. */
  range?: { offset: number; length: number };
  /** Leer el archivo de una instantánea (Snapshot.id). */
  snapshot?: string;
  /** Recibe el tamaño total del archivo apenas el NAS responde (antes de los datos). */
  onSize?: (total: number) => void;
}

export interface Transfers {
  upload(dir: string, file: Blob & { name: string }, onProgress?: (p: XferProgress) => void, opts?: UploadOptions): Promise<FileEntry>;
  download(path: string, onProgress?: (p: XferProgress) => void, opts?: DownloadOptions): Promise<Blob | null>;
}

/** Medidor de velocidad: promedio móvil exponencial sobre muestras de ~250 ms. */
export function rateMeter() {
  let last = performance.now();
  let lastBytes = 0;
  let rate = 0;
  return (bytes: number) => {
    const now = performance.now();
    const dt = (now - last) / 1000;
    if (dt >= 0.25) {
      const inst = (bytes - lastBytes) / dt;
      rate = rate ? rate * 0.7 + inst * 0.3 : inst;
      last = now;
      lastBytes = bytes;
    }
    return rate;
  };
}

/** Ofrece guardar en disco con la File System Access API si existe; `null` si no hay soporte o el usuario cancela. */
export async function pickSaveSink(suggestedName: string): Promise<DownloadSink | null | "cancelled"> {
  const w = globalThis as unknown as { showSaveFilePicker?: (o: { suggestedName: string }) => Promise<{ createWritable(): Promise<{ write(d: unknown): Promise<void>; close(): Promise<void>; abort(r?: unknown): Promise<void> }> }> };
  if (!w.showSaveFilePicker) return null;
  try {
    const handle = await w.showSaveFilePicker({ suggestedName });
    const ws = await handle.createWritable();
    return {
      write: (data, position) => ws.write({ type: "write", position, data }),
      close: () => ws.close(),
      abort: (r) => ws.abort(r),
    };
  } catch (e) {
    if ((e as DOMException)?.name === "AbortError") return "cancelled";
    return null;
  }
}

/** Descarga un Blob con un <a download> temporal. */
export function saveBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 30_000);
}
