// Subidas y bajadas con progreso, velocidad, ETA y cancelar (protocol §10).
import { useCallback, useEffect, useRef, useState } from "react";
import { ArrowDown, ArrowUp, CheckCircle2, ChevronDown, ChevronUp, X, XCircle } from "lucide-react";
import { RpcError, dcLink, dcReady, fmtBytes, fmtRate, pickSaveSink, saveBlob } from "@tango/client";
import type { FileEntry, Transport } from "@tango/client";
import { toast } from "@tango/shell";
import { Progress } from "@tango/ui";
import { errText } from "./errors";

export interface XferItem {
  id: string;
  dir: "up" | "down";
  name: string;
  /** Carpeta destino (subida) o ruta del archivo (bajada), en rutas virtuales. */
  where: string;
  bytes: number;
  total: number;
  rate: number;
  state: "queued" | "running" | "done" | "error" | "cancelled";
  error?: string;
  ac: AbortController;
}

const CONCURRENCY = 2;
let seq = 0;

function eta(x: XferItem) {
  if (x.state !== "running" || x.rate < 1) return "";
  const s = Math.max(0, (x.total - x.bytes) / x.rate);
  if (s < 1) return "menos de 1 s";
  if (s < 60) return `${Math.ceil(s)} s`;
  return `${Math.floor(s / 60)} min ${Math.round(s % 60)} s`;
}

export function useTransfers(t: Transport, onUploaded?: (e: FileEntry) => void) {
  const [items, setItems] = useState<XferItem[]>([]);
  const queue = useRef<(() => Promise<void>)[]>([]);
  const active = useRef(0);
  const uploadedRef = useRef(onUploaded);
  uploadedRef.current = onUploaded;

  const patch = useCallback((id: string, p: Partial<XferItem>) => setItems((l) => l.map((x) => (x.id === id ? { ...x, ...p } : x))), []);

  const pump = useCallback(() => {
    while (active.current < CONCURRENCY && queue.current.length) {
      const job = queue.current.shift()!;
      active.current++;
      void job().finally(() => {
        active.current--;
        pump();
      });
    }
  }, []);

  const enqueue = useCallback(
    (item: Omit<XferItem, "id" | "bytes" | "rate" | "state" | "ac">, run: (x: XferItem) => Promise<void>) => {
      const x: XferItem = { ...item, id: `x${++seq}`, bytes: 0, rate: 0, state: "queued", ac: new AbortController() };
      setItems((l) => [...l, x]);
      queue.current.push(async () => {
        if (x.ac.signal.aborted) return;
        patch(x.id, { state: "running" });
        try {
          await run(x);
          patch(x.id, { state: "done", bytes: x.total });
        } catch (e) {
          const c = e instanceof RpcError && e.code === "CANCELLED";
          patch(x.id, { state: c ? "cancelled" : "error", error: c ? undefined : errText(e, { path: x.where, name: x.name, write: x.dir === "up" }) });
        }
      });
      pump();
      return x;
    },
    [patch, pump],
  );

  const upload = useCallback(
    (dir: string, files: File[], overwrite = false) => {
      const tr = t.transfers;
      if (!tr) {
        toast({ level: "crit", title: "No se puede subir", body: "Este NAS no soporta transferencias de archivos." });
        return;
      }
      for (const f of files) {
        enqueue({ dir: "up", name: f.name, where: dir, total: f.size }, async (x) => {
          try {
            const e = await tr.upload(dir, f, (p) => patch(x.id, { bytes: p.bytes, rate: p.rate }), { overwrite, signal: x.ac.signal });
            uploadedRef.current?.(e);
          } catch (e) {
            if (e instanceof RpcError && e.code === "EXISTS")
              toast({ level: "warn", title: `Ya existe «${f.name}»`, body: "No se reemplazó el archivo que estaba en el NAS.", action: { label: "Reemplazar", run: () => upload(dir, [f], true) } });
            throw e;
          }
        });
      }
    },
    [t, enqueue, patch],
  );

  const download = useCallback(
    async (entry: FileEntry, snapshot?: string) => {
      const tr = t.transfers;
      if (!tr) {
        toast({ level: "crit", title: "No se puede descargar", body: "Este NAS no soporta transferencias de archivos." });
        return;
      }
      // Si el navegador lo permite, se elige dónde guardar y se escribe en streaming (archivos grandes sin cargar memoria).
      const sink = await pickSaveSink(entry.name);
      if (sink === "cancelled") return;
      enqueue({ dir: "down", name: entry.name, where: entry.path, total: entry.size }, async (x) => {
        // Sin File System Access pero con Service Worker: el gestor de descargas del navegador
        // recibe el archivo en streaming (/dc/download, §13) en vez de armar un Blob en memoria.
        if (!sink && dcReady()) {
          const t0 = performance.now();
          await new Promise<void>((resolve, reject) => {
            const l = dcLink("download", tr, entry.path, entry.size, {
              name: entry.name,
              snapshot,
              onProgress: (sent, total) => patch(x.id, { bytes: sent, total, rate: sent / Math.max(0.001, (performance.now() - t0) / 1000) }),
              onDone: (err) => (l.revoke(), err ? reject(err) : resolve()),
            });
            x.ac.signal.addEventListener("abort", () => (l.revoke(), reject(new RpcError("CANCELLED", "Cancelado"))));
            const a = document.createElement("a");
            a.href = l.url;
            a.download = entry.name;
            a.click();
          });
          return;
        }
        const blob = await tr.download(entry.path, (p) => patch(x.id, { bytes: p.bytes, rate: p.rate, total: p.total || x.total }), { signal: x.ac.signal, sink: sink ?? undefined, snapshot });
        if (blob) saveBlob(blob, entry.name);
      });
    },
    [t, enqueue, patch],
  );

  const cancel = useCallback(
    (id: string) => {
      setItems((l) =>
        l.map((x) => {
          if (x.id !== id) return x;
          x.ac.abort();
          return x.state === "queued" ? { ...x, state: "cancelled" } : x;
        }),
      );
    },
    [],
  );

  const clearDone = useCallback(() => setItems((l) => l.filter((x) => x.state === "running" || x.state === "queued")), []);

  return { items, upload, download, cancel, clearDone };
}

/** Panel flotante abajo a la derecha de la ventana, al estilo de las descargas de Safari. */
export function TransfersPanel({ items, onCancel, onClear }: { items: XferItem[]; onCancel: (id: string) => void; onClear: () => void }) {
  const [open, setOpen] = useState(true);
  const live = items.filter((x) => x.state === "running" || x.state === "queued");
  const failed = items.filter((x) => x.state === "error").length;

  // Cuando todo terminó bien, el panel se limpia solo.
  useEffect(() => {
    if (!items.length || live.length || failed) return;
    const id = setTimeout(onClear, 6000);
    return () => clearTimeout(id);
  }, [items.length, live.length, failed, onClear]);

  if (!items.length) return null;
  const total = live.reduce((a, x) => a + x.total, 0);
  const done = live.reduce((a, x) => a + x.bytes, 0);
  const title = live.length
    ? `${live.length === 1 ? "1 transferencia" : `${live.length} transferencias`} en curso`
    : failed
      ? `${failed === 1 ? "1 transferencia falló" : `${failed} transferencias fallaron`}`
      : "Transferencias completas";

  return (
    <section className="tf-xfers" aria-label="Transferencias">
      <header>
        <button type="button" className="tf-xfers-toggle" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
          {open ? <ChevronDown size={13} /> : <ChevronUp size={13} />}
          <strong>{title}</strong>
        </button>
        {!open && live.length > 0 && (
          <div className="tf-xfers-mini">
            <Progress value={total ? done / total : 0} />
          </div>
        )}
        {!live.length && (
          <button type="button" className="tf-xfers-clear" onClick={onClear}>
            Limpiar
          </button>
        )}
      </header>
      {open && (
        <ul>
          {items.map((x) => (
            <li key={x.id} data-state={x.state}>
              <span className="tf-xfer-icon" data-dir={x.dir}>
                {x.state === "done" ? <CheckCircle2 size={15} /> : x.state === "error" ? <XCircle size={15} /> : x.dir === "up" ? <ArrowUp size={14} /> : <ArrowDown size={14} />}
              </span>
              <div className="tf-xfer-body">
                <div className="tf-xfer-name" title={x.where}>
                  {x.name}
                </div>
                {x.state === "running" || x.state === "queued" ? (
                  <>
                    <Progress value={x.total ? x.bytes / x.total : 0} indeterminate={x.state === "queued"} />
                    <small className="tg-num">
                      {x.state === "queued"
                        ? "En espera…"
                        : `${fmtBytes(x.bytes)} de ${fmtBytes(x.total)}${x.rate ? ` · ${fmtRate(x.rate)}` : ""}${eta(x) ? ` · faltan ${eta(x)}` : ""}`}
                    </small>
                  </>
                ) : (
                  <small className={x.state === "error" ? "tf-xfer-err" : undefined}>
                    {x.state === "done"
                      ? `${x.dir === "up" ? "Subido" : "Descargado"} · ${fmtBytes(x.total)}`
                      : x.state === "cancelled"
                        ? "Cancelada"
                        : x.error}
                  </small>
                )}
              </div>
              {(x.state === "running" || x.state === "queued") && (
                <button type="button" className="tf-xfer-x" aria-label={`Cancelar ${x.name}`} title="Cancelar" onClick={() => onCancel(x.id)}>
                  <X size={12} strokeWidth={2.6} />
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
