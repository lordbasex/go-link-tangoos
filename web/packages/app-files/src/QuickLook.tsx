import { useEffect, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, Download, Play, X } from "lucide-react";
import { RpcError, dcLink, dcReady, fmtBytes, fmtDate, locale, t, useTransport } from "@tango/client";
import type { FileEntry } from "@tango/client";
import { Button, Progress } from "@tango/ui";
import { Cover, DocIcon, FolderIcon, Photo, fakeDuration, kindLabel, rng } from "./thumbs";
import { loadThumb, useSnapshot } from "./realthumb";
import { errText } from "./errors";

/** Hasta dónde la vista rápida baja el archivo completo (el resto se descarga a disco). */
export const QL_MEDIA_LIMIT = 20 * 1024 * 1024;
export const QL_TEXT_LIMIT = 1024 * 1024;

/** ¿La vista rápida puede mostrar el contenido real de este archivo? */
export function previewable(e: FileEntry) {
  if (e.kind === "image") return true; // aunque sea grande, muestra la miniatura del NAS
  if (e.kind === "video" || e.kind === "audio") return e.size <= QL_MEDIA_LIMIT;
  if (e.kind === "text" || e.kind === "code") return e.size <= QL_TEXT_LIMIT;
  return false;
}

/** Contenido real: miniatura del NAS, y el archivo completo por DataChannel si es chico. */
function RealBody({ entry, onDownload }: { entry: FileEntry; onDownload?: () => void }) {
  const tp = useTransport();
  const snap = useSnapshot();
  const [thumb, setThumb] = useState<string | null>(null);
  const [url, setUrl] = useState<string | null>(null);
  const [text, setText] = useState<string | null>(null);
  const [pct, setPct] = useState<number | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    setThumb(null);
    setUrl(null);
    setText(null);
    setPct(null);
    setErr(null);
    let alive = true;
    let objectUrl: string | null = null;
    const ac = new AbortController();
    if (entry.kind === "image" || entry.kind === "video") void loadThumb(tp, entry, 256, snap).then((v) => alive && setThumb(v));
    const isText = entry.kind === "text" || entry.kind === "code";
    // Video y audio en streaming por el Service Worker (§13): Range → 206, sin tope de tamaño.
    if ((entry.kind === "video" || entry.kind === "audio") && tp.transfers && dcReady()) {
      const l = dcLink("stream", tp.transfers, entry.path, entry.size, { snapshot: snap || undefined });
      setUrl(l.url);
      return () => {
        alive = false;
        l.revoke();
      };
    }
    const full = previewable(entry) && (isText ? entry.size <= QL_TEXT_LIMIT : entry.size <= QL_MEDIA_LIMIT);
    if (full && tp.transfers) {
      setPct(0);
      tp.transfers
        .download(entry.path, (p) => alive && setPct(p.total ? p.bytes / p.total : 0), { signal: ac.signal, snapshot: snap || undefined })
        .then(async (b) => {
          if (!alive || !b) return;
          if (isText) setText(await b.text());
          else setUrl((objectUrl = URL.createObjectURL(b)));
          setPct(null);
        })
        .catch((e) => {
          if (!alive || (e instanceof RpcError && e.code === "CANCELLED")) return;
          setErr(errText(e, { path: entry.path, name: entry.name, write: false }));
          setPct(null);
        });
    }
    return () => {
      alive = false;
      ac.abort();
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [entry, tp, snap]);

  const loading = pct !== null && (
    <div className="tf-ql-loading">
      <Progress value={pct} />
      <small className="tg-num">{t("Trayendo del NAS… {pct} %", { pct: Math.round(pct * 100) })}</small>
    </div>
  );
  const fallback = (msg?: string) => (
    <div className="tf-ql-icon tf-ql-fallback">
      <DocIcon entry={entry} size={140} />
      {msg && <p>{msg}</p>}
      {onDownload && (
        <Button onClick={onDownload}>
          <Download size={13} /> {t("Descargar")}
        </Button>
      )}
    </div>
  );

  if (err) return fallback(err);
  switch (entry.kind) {
    case "image":
      return (
        <div className="tf-ql-media">
          {url || thumb ? <img className="tf-ql-img" src={url ?? thumb!} alt={entry.name} data-preview={!url || undefined} /> : !loading && fallback(t("El NAS no generó una vista previa de esta imagen."))}
          {loading}
        </div>
      );
    case "video":
      if (url) return <video className="tf-ql-av" src={url} controls />;
      if (!previewable(entry)) return fallback(t("Es un video de {size}: descargalo para verlo completo.", { size: fmtBytes(entry.size) }));
      return (
        <div className="tf-ql-media">
          {thumb && <img className="tf-ql-img" src={thumb} alt="" data-preview />}
          {loading}
        </div>
      );
    case "audio":
      if (url)
        return (
          <div className="tf-ql-icon tf-ql-fallback">
            <DocIcon entry={entry} size={120} />
            <audio src={url} controls />
          </div>
        );
      if (!previewable(entry)) return fallback(t("Es un audio de {size}: descargalo para escucharlo.", { size: fmtBytes(entry.size) }));
      return <div className="tf-ql-icon">{loading}</div>;
    case "text":
    case "code":
      if (text !== null) return <pre className="tf-ql-text">{text}</pre>;
      if (!previewable(entry)) return fallback(t("El archivo pesa {size}: es demasiado largo para la vista rápida.", { size: fmtBytes(entry.size) }));
      return <div className="tf-ql-icon">{loading}</div>;
    case "dir":
      return (
        <div className="tf-ql-icon">
          <FolderIcon size={160} />
        </div>
      );
    default:
      return fallback(t("Este tipo de archivo no tiene vista rápida todavía."));
  }
}

function Waveform({ seed }: { seed: number }) {
  const r = rng(seed);
  return (
    <div className="tf-ql-wave" aria-hidden="true">
      {Array.from({ length: 64 }, (_, i) => (
        <span key={i} style={{ height: `${18 + Math.abs(Math.sin(i / 4 + r() * 2)) * 70 * (0.5 + r() * 0.5)}%`, opacity: i < 22 ? 1 : 0.4 }} />
      ))}
    </div>
  );
}

function PageMock({ entry }: { entry: FileEntry }) {
  const r = rng(entry.seed ?? 3);
  const lines = 18;
  return (
    <div className="tf-ql-page">
      <div className="tf-ql-page-title">{entry.name.replace(/\.[^.]+$/, "")}</div>
      {Array.from({ length: lines }, (_, i) => (
        <div key={i} className="tf-ql-line" style={{ width: `${i % 6 === 5 ? 40 + r() * 20 : 82 + r() * 18}%`, marginTop: i % 6 === 0 && i ? 14 : undefined }} />
      ))}
    </div>
  );
}

function Sheet({ seed }: { seed: number }) {
  const r = rng(seed);
  return (
    <table className="tf-ql-sheet">
      <tbody>
        {Array.from({ length: 12 }, (_, i) => (
          <tr key={i}>
            {Array.from({ length: 5 }, (_, j) => (
              <td key={j}>{i === 0 ? ["Concepto", "Enero", "Febrero", "Marzo", "Total"][j] : j === 0 ? ["Luz", "Gas", "Agua", "Internet", "Expensas", "Súper", "Nafta", "Prepaga", "Colegio", "Seguro", "Varios"][i - 1] : `$ ${Math.round(5000 + r() * 90000).toLocaleString(locale())}`}</td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

const textSample = (name: string) =>
  name.endsWith(".go")
    ? `package main\n\nimport "log/slog"\n\nfunc main() {\n\tslog.Info("tangod listo", "addr", ":443")\n}\n`
    : name.toLowerCase().includes("wifi")
      ? "Red: Casa-Invitados\nClave: (ver con Federico)\n"
      : `# ${name.replace(/\.[^.]+$/, "")}\n\n- Revisar el presupuesto del mes\n- Llamar al técnico del aire\n- Comprar discos para el NAS (2 × 8 TB)\n- Backup de las fotos del celular\n`;

export function QuickLook({
  entry, onClose, onPrev, onNext, index, total, real, onDownload,
}: {
  entry: FileEntry;
  onClose: () => void;
  onPrev: () => void;
  onNext: () => void;
  index: number;
  total: number;
  /** Datos reales del NAS: muestra el contenido de verdad en vez de la simulación. */
  real?: boolean;
  onDownload?: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.focus();
    return () => prev?.focus?.();
  }, []);
  const seed = entry.seed ?? 1;
  const onKey = (e: React.KeyboardEvent) => {
    e.stopPropagation();
    if (e.key === " " || e.key === "Escape") {
      e.preventDefault();
      onClose();
    } else if (e.key === "ArrowRight" || e.key === "ArrowDown") {
      e.preventDefault();
      onNext();
    } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
      e.preventDefault();
      onPrev();
    }
  };

  let body: React.ReactNode;
  if (real) body = <RealBody entry={entry} onDownload={onDownload} />;
  else switch (entry.kind) {
    case "image":
      body = (
        <div className="tf-ql-media">
          <Photo seed={seed} w={640} h={440} />
        </div>
      );
      break;
    case "video":
      body = (
        <div className="tf-ql-media tf-ql-video">
          <Photo seed={seed} w={640} h={360} />
          <button type="button" className="tf-ql-play" aria-label="Reproducir (demo)">
            <Play size={28} fill="currentColor" />
          </button>
          <div className="tf-ql-scrub">
            <span>0:00</span>
            <div>
              <i style={{ width: "0%" }} />
            </div>
            <span>{fakeDuration(seed, "video")}</span>
          </div>
        </div>
      );
      break;
    case "audio":
      body = (
        <div className="tf-ql-audio">
          <div className="tf-ql-cover">
            <Cover seed={seed} />
          </div>
          <div className="tf-ql-audio-meta">
            <strong>{entry.name.replace(/\.[^.]+$/, "")}</strong>
            <span>{entry.path.split("/").slice(-2, -1)[0]}</span>
            <Waveform seed={seed} />
            <small className="tg-num">1:02 / {fakeDuration(seed, "audio")}</small>
          </div>
        </div>
      );
      break;
    case "pdf":
    case "doc":
      body = <PageMock entry={entry} />;
      break;
    case "sheet":
      body = <Sheet seed={seed} />;
      break;
    case "text":
    case "code":
      body = <pre className="tf-ql-text">{textSample(entry.name)}</pre>;
      break;
    case "dir":
      body = (
        <div className="tf-ql-icon">
          <FolderIcon size={160} />
        </div>
      );
      break;
    default:
      body = (
        <div className="tf-ql-icon">
          <DocIcon entry={entry} size={160} />
        </div>
      );
  }

  return (
    <div className="tf-ql-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="tf-ql" role="dialog" aria-label={t("Vista rápida de {name}", { name: entry.name })} tabIndex={-1} ref={ref} onKeyDown={onKey}>
        <div className="tf-ql-head">
          <button type="button" className="tf-ql-x" onClick={onClose} aria-label={t("Cerrar vista rápida")}>
            <X size={13} strokeWidth={2.4} />
          </button>
          <div className="tf-ql-name">
            <strong>{entry.name}</strong>
            <span>
              {t(kindLabel[entry.kind])} · {entry.kind === "dir" ? t("carpeta") : fmtBytes(entry.size)} · {fmtDate(entry.mtime)}
            </span>
          </div>
          {real && onDownload && entry.kind !== "dir" && (
            <button type="button" className="tf-ql-dl" onClick={onDownload} aria-label={t("Descargar {name}", { name: entry.name })} title={t("Descargar")}>
              <Download size={14} />
            </button>
          )}
          {total > 1 && (
            <div className="tf-ql-nav">
              <button type="button" onClick={onPrev} aria-label={t("Anterior")}>
                <ChevronLeft size={16} />
              </button>
              <span className="tg-num">
                {t("{n} de {total}", { n: index + 1, total })}
              </span>
              <button type="button" onClick={onNext} aria-label={t("Siguiente")}>
                <ChevronRight size={16} />
              </button>
            </div>
          )}
        </div>
        <div className="tf-ql-body">{body}</div>
      </div>
    </div>
  );
}
