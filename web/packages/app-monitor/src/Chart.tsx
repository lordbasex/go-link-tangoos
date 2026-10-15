import { memo, useId, useRef, useState } from "react";

export interface Series {
  label: string;
  values: number[];
  color: string;
  fill?: boolean;
}

const VW = 600;

/** Escala "linda" para el eje Y: 1, 2, 2.5, 5 × 10^n. */
function niceMax(v: number) {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

function pathOf(vs: number[], max: number, h: number, total: number) {
  if (vs.length < 2) return "";
  const step = VW / (total - 1);
  const off = total - vs.length;
  let d = "";
  for (let i = 0; i < vs.length; i++) {
    const x = (off + i) * step;
    const y = h - (Math.min(vs[i], max) / max) * (h - 2) - 1;
    d += `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
  }
  return d;
}

/**
 * Gráfico de serie temporal: líneas de guía, eje Y, cruz al pasar el mouse con tooltip
 * y pausa mientras el mouse está encima (como Activity Monitor).
 */
export const Chart = memo(function Chart({
  series, height = 120, max, format, times, capacity = 240, compact,
}: {
  series: Series[];
  height?: number;
  max?: number;
  format: (v: number) => string;
  times: number[];
  capacity?: number;
  compact?: boolean;
}) {
  const gid = useId().replace(/:/g, "");
  const box = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const [frozen, setFrozen] = useState<{ series: Series[]; times: number[] } | null>(null);
  const data = frozen ?? { series, times };
  const len = data.series[0]?.values.length ?? 0;
  const top = max ?? niceMax(Math.max(1e-9, ...data.series.flatMap((s) => s.values)));
  const off = capacity - len;

  const onMove = (e: React.PointerEvent) => {
    const r = box.current!.getBoundingClientRect();
    const i = Math.round(((e.clientX - r.left) / r.width) * (capacity - 1)) - off;
    setHover(i >= 0 && i < len ? i : null);
  };

  const hx = hover === null ? 0 : ((off + hover) / (capacity - 1)) * 100;
  const ago = hover === null ? 0 : Math.round((Date.now() - data.times[hover]) / 1000);

  return (
    <div
      className="tm-chart"
      data-compact={compact || undefined}
      ref={box}
      style={{ height }}
      onPointerEnter={() => setFrozen({ series, times })}
      onPointerMove={onMove}
      onPointerLeave={() => {
        setFrozen(null);
        setHover(null);
      }}
    >
      <svg viewBox={`0 0 ${VW} ${height}`} preserveAspectRatio="none" aria-hidden="true">
        <defs>
          {data.series.map((s, i) => (
            <linearGradient key={i} id={`${gid}${i}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stopColor={s.color} stopOpacity="0.32" />
              <stop offset="1" stopColor={s.color} stopOpacity="0.02" />
            </linearGradient>
          ))}
        </defs>
        {[0.25, 0.5, 0.75].map((g) => (
          <line key={g} x1="0" x2={VW} y1={height * g} y2={height * g} className="tm-grid" vectorEffect="non-scaling-stroke" />
        ))}
        {data.series.map((s, i) => {
          const d = pathOf(s.values, top, height, capacity);
          if (!d) return null;
          const x0 = ((off / (capacity - 1)) * VW).toFixed(1);
          return (
            <g key={s.label}>
              {s.fill !== false && <path d={`${d}L${VW},${height}L${x0},${height}Z`} fill={`url(#${gid}${i})`} />}
              <path d={d} fill="none" stroke={s.color} strokeWidth="1.6" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
            </g>
          );
        })}
      </svg>
      {!compact && (
        <>
          <span className="tm-axis tm-axis-top">{format(top)}</span>
          <span className="tm-axis tm-axis-mid">{format(top / 2)}</span>
        </>
      )}
      {frozen && <span className="tm-paused">En pausa</span>}
      {hover !== null && (
        <>
          <div className="tm-cross" style={{ left: `${hx}%` }} />
          {data.series.map((s) => (
            <div key={s.label} className="tm-dot" style={{ left: `${hx}%`, top: height - (Math.min(s.values[hover], top) / top) * (height - 2) - 1, background: s.color }} />
          ))}
          <div className="tm-tip" style={hx > 60 ? { right: `${100 - hx}%`, marginRight: 10 } : { left: `${hx}%`, marginLeft: 10 }}>
            <div className="tm-tip-time">{ago <= 0 ? "Ahora" : `Hace ${ago} s`}</div>
            {data.series.map((s) => (
              <div key={s.label} className="tm-tip-row">
                <i style={{ background: s.color }} />
                {s.label}
                <b>{format(s.values[hover])}</b>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
});

/** Mini gráfico sin interacción para tablas (canales WebRTC). */
export function Spark({ values, color, width = 90, height = 22 }: { values: number[]; color: string; width?: number; height?: number }) {
  const top = niceMax(Math.max(1e-9, ...values));
  if (values.length < 2) return <svg width={width} height={height} />;
  const step = width / (values.length - 1);
  const d = values.map((v, i) => `${i ? "L" : "M"}${(i * step).toFixed(1)},${(height - (v / top) * (height - 2) - 1).toFixed(1)}`).join("");
  return (
    <svg width={width} height={height} aria-hidden="true" style={{ display: "block" }}>
      <path d={`${d}L${width},${height}L0,${height}Z`} fill={color} fillOpacity="0.15" />
      <path d={d} fill="none" stroke={color} strokeWidth="1.3" strokeLinejoin="round" />
    </svg>
  );
}
