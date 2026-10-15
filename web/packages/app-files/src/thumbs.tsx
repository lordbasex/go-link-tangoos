// Miniaturas generativas y determinísticas (docs/13 §7.2): nunca imágenes externas.
import { memo, useId } from "react";
import type { FileEntry, FileKind } from "@tango/client";

export function rng(seed: number) {
  let s = seed >>> 0 || 1;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 4294967296;
  };
}

const scenes = [
  { sky: ["#2b4c8c", "#f29a6b", "#ffd29a"], sun: "#fff1cf", land: ["#2e2a4a", "#18162b"], water: true },
  { sky: ["#7fb6ef", "#bfe0fb", "#eaf6ff"], sun: "#ffffff", land: ["#5f8f5a", "#2f5a35"], water: false },
  { sky: ["#9cc7f0", "#d5e8f7", "#f6fbff"], sun: "#ffffff", land: ["#8ea2bd", "#566a86"], water: true },
  { sky: ["#141a3a", "#3c2d6b", "#a35a7d"], sun: "#ffe7c2", land: ["#0f0d1f", "#07060f"], water: false },
  { sky: ["#f4c9a0", "#f7dfc0", "#fff3e1"], sun: "#fff9ef", land: ["#c18a5a", "#7a4f32"], water: false },
  { sky: ["#3a8fd0", "#7cc2ec", "#c9ecff"], sun: "#ffffff", land: ["#2f8a8f", "#1d5c69"], water: true },
];

/** Una "foto" generada: cielo, sol, montañas o ciudad, agua y bokeh. */
export const Photo = memo(function Photo({ seed, w = 160, h = 120, className }: { seed: number; w?: number; h?: number; className?: string }) {
  const id = useId().replace(/:/g, "");
  const r = rng(seed);
  const sc = scenes[Math.floor(r() * scenes.length)];
  const horizon = h * (0.5 + r() * 0.2);
  const ridge = (base: number, amp: number) => {
    const pts: string[] = [`0,${h}`];
    let y = base;
    for (let x = 0; x <= w; x += w / 14) {
      y = Math.min(h, Math.max(base - amp, y + (r() - 0.5) * amp * 0.9));
      pts.push(`${x.toFixed(1)},${y.toFixed(1)}`);
    }
    pts.push(`${w},${h}`);
    return pts.join(" ");
  };
  const city = sc === scenes[3];
  const bokeh = r() < 0.35;
  return (
    <svg className={className} viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="xMidYMid slice" width="100%" height="100%" aria-hidden="true">
      <defs>
        <linearGradient id={`${id}s`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor={sc.sky[0]} />
          <stop offset="0.65" stopColor={sc.sky[1]} />
          <stop offset="1" stopColor={sc.sky[2]} />
        </linearGradient>
        <linearGradient id={`${id}l`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor={sc.land[0]} />
          <stop offset="1" stopColor={sc.land[1]} />
        </linearGradient>
        <radialGradient id={`${id}g`}>
          <stop offset="0" stopColor={sc.sun} stopOpacity="0.9" />
          <stop offset="1" stopColor={sc.sun} stopOpacity="0" />
        </radialGradient>
      </defs>
      <rect width={w} height={h} fill={`url(#${id}s)`} />
      <circle cx={w * (0.2 + r() * 0.6)} cy={horizon - h * (0.08 + r() * 0.2)} r={h * 0.38} fill={`url(#${id}g)`} />
      <circle cx={w * (0.2 + r() * 0.6)} cy={horizon - h * 0.12} r={h * (0.05 + r() * 0.05)} fill={sc.sun} opacity="0.9" />
      {city ? (
        Array.from({ length: 16 }, (_, i) => {
          const bw = w / 16;
          const bh = h * (0.08 + r() * 0.3);
          return <rect key={i} x={i * bw} y={horizon - bh} width={bw - 1} height={bh + 2} fill={sc.land[0]} />;
        })
      ) : (
        <polygon points={ridge(horizon - h * 0.08, h * 0.22)} fill={sc.land[0]} opacity="0.55" />
      )}
      <polygon points={ridge(horizon, h * 0.1)} fill={`url(#${id}l)`} />
      {sc.water && <rect y={horizon + h * 0.12} width={w} height={h} fill={sc.sky[0]} opacity="0.55" />}
      {bokeh &&
        Array.from({ length: 7 }, (_, i) => <circle key={i} cx={r() * w} cy={r() * h} r={4 + r() * 12} fill="#fff" opacity={0.08 + r() * 0.16} />)}
    </svg>
  );
});

/** Portada de disco / video: bloques de color abstractos. */
export const Cover = memo(function Cover({ seed, w = 120, h = 120 }: { seed: number; w?: number; h?: number }) {
  const r = rng(seed ^ 0x9e3779b9);
  const hue = Math.floor(r() * 360);
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width="100%" height="100%" preserveAspectRatio="xMidYMid slice" aria-hidden="true">
      <rect width={w} height={h} fill={`hsl(${hue} 45% 22%)`} />
      {Array.from({ length: 5 }, (_, i) => (
        <circle key={i} cx={r() * w} cy={r() * h} r={18 + r() * 40} fill={`hsl(${(hue + i * 40) % 360} 70% ${45 + r() * 20}%)`} opacity={0.55} />
      ))}
    </svg>
  );
});

export const extOf = (name: string) => (name.includes(".") ? name.slice(name.lastIndexOf(".") + 1).toLowerCase() : "");

export const kindLabel: Record<FileKind, string> = {
  dir: "Carpeta", image: "Imagen", video: "Video", audio: "Audio", pdf: "Documento PDF", doc: "Documento", sheet: "Planilla", code: "Código fuente", archive: "Archivo comprimido", text: "Texto", other: "Documento",
};

const kindColor: Record<FileKind, string> = {
  dir: "#3b8cf5", image: "#ec4899", video: "#8b5cf6", audio: "#f97316", pdf: "#e5484d", doc: "#2f7cf6", sheet: "#1fa463", code: "#5b5f6b", archive: "#a16207", text: "#6b7280", other: "#8e8e96",
};

/** Ícono de carpeta estilo macOS; `tint` para carpetas compartidas. */
export function FolderIcon({ size = 64, tint }: { size?: number; tint?: string }) {
  const id = useId().replace(/:/g, "");
  const base = tint ?? "#5aa9ff";
  return (
    <svg width={size} height={size * 0.8} viewBox="0 0 100 80" aria-hidden="true" style={{ display: "block" }}>
      <defs>
        <linearGradient id={`${id}b`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" style={{ stopColor: `color-mix(in srgb, ${base} 78%, white)` }} />
          <stop offset="1" style={{ stopColor: base }} />
        </linearGradient>
      </defs>
      <path d="M6 12c0-3.3 2.7-6 6-6h22.5c1.7 0 3.3.7 4.4 2l4.2 4.8h45c3.3 0 6 2.7 6 6V20H6Z" style={{ fill: `color-mix(in srgb, ${base} 72%, #0a2a6a)` }} />
      <rect x="6" y="18" width="88" height="56" rx="6" fill={`url(#${id}b)`} />
      <rect x="6" y="18" width="88" height="1.4" fill="#fff" opacity="0.45" />
      {tint && <circle cx="50" cy="47" r="9" fill="#fff" opacity="0.35" />}
    </svg>
  );
}

/** Página con esquina doblada y etiqueta de extensión de color. */
export function DocIcon({ entry, size = 64 }: { entry: Pick<FileEntry, "name" | "kind">; size?: number }) {
  const ext = extOf(entry.name).slice(0, 4).toUpperCase() || "DOC";
  const color = kindColor[entry.kind];
  return (
    <svg width={size * 0.8} height={size} viewBox="0 0 80 100" aria-hidden="true" style={{ display: "block" }}>
      <path d="M10 6h42l20 20v66a4 4 0 0 1-4 4H10a4 4 0 0 1-4-4V10a4 4 0 0 1 4-4Z" fill="#fff" stroke="rgb(0 0 0 / .14)" strokeWidth="1" />
      <path d="M52 6v16a4 4 0 0 0 4 4h16" fill="#eef0f4" stroke="rgb(0 0 0 / .14)" strokeWidth="1" />
      {[38, 46, 54, 62].map((y, i) => (
        <rect key={y} x="16" y={y} width={i === 3 ? 30 : 48} height="3" rx="1.5" fill="#d7dae1" />
      ))}
      <rect x="10" y="70" width={Math.max(30, ext.length * 11 + 10)} height="18" rx="4" fill={color} />
      <text x="15" y="83.5" fontSize="12" fontWeight="700" fill="#fff" fontFamily="Inter Variable, Inter, sans-serif">
        {ext}
      </text>
    </svg>
  );
}

const fakeDuration = (seed: number, kind: FileKind) => {
  const r = rng(seed);
  const total = kind === "audio" ? 140 + Math.floor(r() * 220) : 60 + Math.floor(r() * 7200);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
};
export { fakeDuration };

/** Miniatura para la grilla de íconos. */
export function Thumb({ entry, size = 64, tint }: { entry: FileEntry; size?: number; tint?: string }) {
  const seed = entry.seed ?? 1;
  if (entry.kind === "dir") return <FolderIcon size={size} tint={tint} />;
  if (entry.kind === "image")
    return (
      <div className="tf-photo" style={{ width: size * 1.15, height: size * 0.86 }}>
        <Photo seed={seed} />
      </div>
    );
  if (entry.kind === "video")
    return (
      <div className="tf-photo tf-video" style={{ width: size * 1.2, height: size * 0.72 }}>
        <Photo seed={seed} />
        <span className="tf-dur">{fakeDuration(seed, "video")}</span>
      </div>
    );
  if (entry.kind === "audio")
    return (
      <div className="tf-photo tf-cover" style={{ width: size * 0.9, height: size * 0.9 }}>
        <Cover seed={seed} />
      </div>
    );
  return <DocIcon entry={entry} size={size} />;
}
