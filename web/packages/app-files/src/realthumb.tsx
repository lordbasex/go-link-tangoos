// Miniaturas reales: files.thumb del NAS, cargadas al entrar en pantalla y guardadas en un LRU en memoria.
import { createContext, useContext, useEffect, useRef, useState } from "react";
import { useTransport } from "@tango/client";
import type { FileEntry, Transport } from "@tango/client";
import { DocIcon, FolderIcon } from "./thumbs";

const MAX = 400;
/** clave → data URL, o null si el NAS no puede generar miniatura para ese archivo. */
const cache = new Map<string, string | null>();
const inflight = new Map<string, Promise<string | null>>();

/** Instantánea que se está explorando (solo lectura, §13); "" = la carpeta en vivo. */
export const SnapshotCtx = createContext("");
export const useSnapshot = () => useContext(SnapshotCtx);

const keyOf = (e: FileEntry, size: number, snap = "") => `${snap}|${e.path}|${e.mtime}|${e.size}|${size}`;

function remember(k: string, v: string | null) {
  cache.delete(k);
  cache.set(k, v);
  if (cache.size > MAX) cache.delete(cache.keys().next().value!);
}

/** Pide (una sola vez por clave) la miniatura al NAS. */
export function loadThumb(t: Transport, e: FileEntry, size: 128 | 256, snap = ""): Promise<string | null> {
  const k = keyOf(e, size, snap);
  if (cache.has(k)) {
    const v = cache.get(k)!;
    remember(k, v); // toca la entrada: queda como la más reciente
    return Promise.resolve(v);
  }
  let p = inflight.get(k);
  if (!p) {
    p = t
      .call("files.thumb", snap ? { path: e.path, size, snapshot: snap } : { path: e.path, size })
      .then(
        (r) => `data:${r.mime};base64,${r.data}`,
        () => null,
      )
      .then((v) => {
        remember(k, v);
        inflight.delete(k);
        return v;
      });
    inflight.set(k, p);
  }
  return p;
}

export const thumbable = (e: FileEntry) => e.kind === "image" || e.kind === "video";

/** Miniatura de la grilla para datos reales: foto del NAS o el ícono del tipo de archivo. */
export function RealThumb({ entry, size = 64, tint }: { entry: FileEntry; size?: number; tint?: string }) {
  const t = useTransport();
  const snap = useSnapshot();
  const ref = useRef<HTMLDivElement>(null);
  const k = keyOf(entry, 128, snap);
  const [src, setSrc] = useState<string | null | undefined>(() => (cache.has(k) ? cache.get(k) : undefined));

  useEffect(() => {
    if (!thumbable(entry) || cache.has(k)) {
      setSrc(cache.get(k));
      return;
    }
    const el = ref.current;
    if (!el) return;
    let alive = true;
    const io = new IntersectionObserver(
      (es) => {
        if (!es.some((x) => x.isIntersecting)) return;
        io.disconnect();
        void loadThumb(t, entry, 128, snap).then((v) => alive && setSrc(v));
      },
      { rootMargin: "200px" },
    );
    io.observe(el);
    return () => {
      alive = false;
      io.disconnect();
    };
  }, [k, entry, t, snap]);

  if (entry.kind === "dir") return <FolderIcon size={size} tint={tint} />;
  if (!thumbable(entry) || src === null) return <DocIcon entry={entry} size={size} />;
  return (
    <div ref={ref} className={`tf-photo tf-real${entry.kind === "video" ? " tf-video" : ""}`} style={{ width: size * 1.15, height: size * 0.86 }}>
      {src ? <img src={src} alt="" draggable={false} decoding="async" /> : <span className="tf-thumb-wait" />}
    </div>
  );
}
