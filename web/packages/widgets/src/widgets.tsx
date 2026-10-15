import { memo, useEffect, useRef, useState } from "react";
import type { ComponentType, ReactNode } from "react";
import { Activity, AlertTriangle, ArrowDown, ArrowUp, CheckCircle2, Clock3, Cpu, Fan, HardDrive, Loader2, Lock, MemoryStick, Network, Server, Thermometer, Timer, Users, XOctagon, Zap } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { fmtBytes, fmtDuration, fmtRate, useJobs, useLive, useMetrics, fmtCpuTemp, fmtRpm } from "@tango/client";
import type { Disk, MetricsFrame, Volume } from "@tango/client";
import { AreaChart, Ring } from "@tango/ui";

export type WidgetSize = "small" | "medium" | "large";

export interface WidgetDef {
  id: string;
  name: string;
  description: string;
  size: WidgetSize;
  icon: LucideIcon;
  app?: { id: string; name: string };
  className?: string;
  Component: ComponentType;
}

const MONITOR = { id: "tango.monitor", name: "Monitor" };
const STORAGE = { id: "tango.storage", name: "Almacenamiento" };

// ---- Piezas comunes ---------------------------------------------------------------

function Head({ icon: Icon, children, right }: { icon: LucideIcon; children: ReactNode; right?: ReactNode }) {
  return (
    <div className="tw-head">
      <Icon size={12} strokeWidth={2.4} />
      {children}
      {right !== undefined && <span className="tw-head-right">{right}</span>}
    </div>
  );
}

const nf1 = new Intl.NumberFormat("es-AR", { maximumFractionDigits: 1, minimumFractionDigits: 1 });
const nf0 = new Intl.NumberFormat("es-AR", { maximumFractionDigits: 0 });
/** La RAM se cuenta en GiB, como la muestran todos los sistemas operativos. */
const gib = (b: number) => nf1.format(b / 2 ** 30);

/** Dona de varios segmentos (la Ring de @tango/ui dibuja uno solo). */
function Donut({ segments, size, stroke, children }: { segments: { value: number; color: string }[]; size: number; stroke: number; children?: ReactNode }) {
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  let acc = 0;
  return (
    <div style={{ position: "relative", width: size, height: size, flex: "none" }}>
      <svg width={size} height={size} style={{ display: "block", transform: "rotate(-90deg)" }} aria-hidden="true">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--tg-track)" strokeWidth={stroke} />
        {segments.map((s, i) => {
          const len = Math.max(0, Math.min(1, s.value)) * c;
          const off = -acc;
          acc += len;
          return (
            <circle
              key={i}
              cx={size / 2}
              cy={size / 2}
              r={r}
              fill="none"
              stroke={s.color}
              strokeWidth={stroke}
              strokeDasharray={`${len} ${c}`}
              strokeDashoffset={off}
              strokeLinecap={len > stroke ? "round" : "butt"}
              style={{ transition: "stroke-dasharray 400ms var(--tg-ease), stroke-dashoffset 400ms var(--tg-ease)" }}
            />
          );
        })}
      </svg>
      {children && <div style={{ position: "absolute", inset: 0, display: "grid", placeItems: "center", textAlign: "center" }}>{children}</div>}
    </div>
  );
}

function useLast(): [MetricsFrame | undefined, MetricsFrame[]] {
  const frames = useMetrics();
  return [frames[frames.length - 1], frames];
}

const cpuTone = (v: number) => (v > 0.85 ? "var(--tg-crit)" : v > 0.6 ? "var(--tg-warn)" : "var(--tg-accent)");

// ---- CPU ----------------------------------------------------------------------------

function CpuWidget() {
  const [m, frames] = useLast();
  const cpu = m?.cpu ?? 0;
  const spark = frames.slice(-120).map((f) => f.cpu);
  return (
    <>
      <Head icon={Cpu} right={m && m.tempCpu > 0 ? fmtCpuTemp(m.tempCpu) : ""}>
        CPU
      </Head>
      <div className="tw-cpu-ring">
        <Ring value={cpu} size={62} stroke={7} color={cpuTone(cpu)}>
          <span className="tw-ring-pct">
            {Math.round(cpu * 100)}
            <small>%</small>
          </span>
        </Ring>
        <div className="tw-cores" aria-label="Carga por núcleo">
          {(m?.perCore ?? [0, 0, 0, 0]).map((c, i) => (
            <div key={i} className="tw-core">
              {i + 1}
              <div className="tw-core-bar">
                <div style={{ width: `${Math.round(c * 100)}%` }} />
              </div>
            </div>
          ))}
        </div>
      </div>
      <div className="tw-spark">
        <AreaChart values={spark} max={1} width={140} height={30} color={cpuTone(cpu)} strokeWidth={1.4} />
      </div>
    </>
  );
}

// ---- Memoria ------------------------------------------------------------------------

function MemoryWidget() {
  const [m] = useLast();
  const total = m?.memTotal ?? 16 * 2 ** 30;
  const used = m?.memUsed ?? 0;
  const cache = m?.memCache ?? 0;
  return (
    <>
      <Head icon={MemoryStick}>Memoria</Head>
      <div className="tw-mem">
        <Donut
          size={56}
          stroke={7}
          segments={[
            { value: used / total, color: "var(--tg-accent)" },
            { value: cache / total, color: "rgb(var(--tg-accent-rgb) / 0.38)" },
          ]}
        />
        <div>
          <div className="tw-big" style={{ fontSize: 26 }}>
            {gib(used)}
          </div>
          <div className="tw-sub">de {nf0.format(total / 2 ** 30)} GB</div>
        </div>
      </div>
      <div className="tw-legend">
        <div className="tw-legend-row">
          <span className="tw-dot" style={{ background: "var(--tg-accent)" }} />
          En uso <b>{Math.round((used / total) * 100)} %</b>
        </div>
        <div className="tw-legend-row">
          <span className="tw-dot" style={{ background: "rgb(var(--tg-accent-rgb) / 0.38)" }} />
          Caché <b>{gib(cache)} GB</b>
        </div>
      </div>
    </>
  );
}

// ---- Red ----------------------------------------------------------------------------

const UP = "var(--tg-ok)";

function NetworkWidget() {
  const [m, frames] = useLast();
  const rx = frames.map((f) => f.netRx);
  const tx = frames.map((f) => f.netTx);
  return (
    <>
      <Head icon={Network} right="eth0 · 1 GbE">
        Red
      </Head>
      <div className="tw-net">
        <div className="tw-net-rates">
          <div>
            <div className="tw-rate-label" style={{ color: "var(--tg-accent)" }}>
              <ArrowDown size={12} strokeWidth={2.8} /> Bajada
            </div>
            <div className="tw-mid">{fmtRate(m?.netRx ?? 0)}</div>
          </div>
          <div>
            <div className="tw-rate-label" style={{ color: UP }}>
              <ArrowUp size={12} strokeWidth={2.8} /> Subida
            </div>
            <div className="tw-mid">{fmtRate(m?.netTx ?? 0)}</div>
          </div>
        </div>
        <div className="tw-net-chart">
          <AreaChart values={rx} width={200} height={92} color="var(--tg-accent)" secondary={{ values: tx, color: UP }} strokeWidth={1.5} />
          <div className="tw-net-axis">
            <span>hace 60 s</span>
            <span>ahora</span>
          </div>
        </div>
      </div>
    </>
  );
}

// ---- Almacenamiento -----------------------------------------------------------------

function volumeStatus(v: Volume): { tone: "ok" | "warn" | "crit" | "info"; label: string; icon: LucideIcon } {
  switch (v.state) {
    case "degraded":
      return { tone: "crit", label: "Sin redundancia", icon: XOctagon };
    case "scrubbing":
      return { tone: "info", label: `Verificando ${Math.round((v.progress ?? 0) * 100)} %`, icon: Loader2 };
    case "building":
      return { tone: "info", label: `Construyendo ${Math.round((v.progress ?? 0) * 100)} %`, icon: Loader2 };
    case "readonly":
      return { tone: "warn", label: "Solo lectura", icon: AlertTriangle };
    case "locked":
      return { tone: "warn", label: "Bloqueado (cifrado)", icon: Lock };
    default:
      return { tone: "ok", label: "En orden", icon: CheckCircle2 };
  }
}

function bayTone(d?: Disk): "ok" | "warn" | "crit" | "idle" | undefined {
  if (!d) return undefined;
  if (d.use === "failed" || d.smart === "crit") return "crit";
  if (d.smart === "warn") return "warn";
  return d.use === "member" ? "ok" : "idle";
}

const profileName: Record<Volume["profile"], string> = { basic: "Básico", raid1: "RAID 1", raid5: "RAID 5", raid6: "RAID 6", raid10: "RAID 10", hybrid1: "Híbrido-1", hybrid2: "Híbrido-2" };
const fsName: Record<Volume["fs"], string> = { btrfs: "Btrfs", xfs: "XFS", ext4: "ext4" };

function StorageWidget() {
  const vols = useLive("storage.volumes.list", undefined, ["storage.changed"]).data;
  const disks = useLive("storage.disks.list", undefined, ["storage.changed"]).data ?? [];
  const v = vols?.[0];
  const bayCount = Math.max(4, ...disks.map((d) => d.bay ?? 0));
  const bays = Array.from({ length: bayCount }, (_, i) => disks.find((d) => d.bay === i + 1));
  const compact = bayCount > 4; // con muchas bahías solo se muestran los LEDs
  const bayRow = (
    <div className="tw-bays" aria-label="Bahías" data-compact={compact || undefined}>
      {bays.map((d, i) => {
        const tone = bayTone(d);
        return (
          <span key={i} className="tw-bay" title={d ? `Bahía ${i + 1} · ${d.model} · ${d.use === "failed" ? "falló" : d.use === "member" ? "en uso" : "libre"}` : "Bahía vacía"}>
            <span className="tw-led" data-tone={tone} />
            {!compact && i + 1}
          </span>
        );
      })}
    </div>
  );

  if (!vols) return <Head icon={HardDrive}>Almacenamiento</Head>;

  if (!v) {
    const foreign = disks.some((d) => d.foreign);
    return (
      <>
        <Head icon={HardDrive}>Almacenamiento</Head>
        <div className="tw-vol">
          <Donut size={86} stroke={9} segments={[]}>
            <HardDrive size={22} strokeWidth={1.7} style={{ color: "var(--tg-widget-text-2)" }} />
          </Donut>
          <div className="tw-vol-info">
            <strong style={{ fontSize: 15, fontWeight: 650 }}>{foreign ? "Discos de otro NAS" : "Sin volúmenes"}</strong>
            <span className="tw-sub">{foreign ? "Podés montarlos en solo lectura y revisar tus datos." : `${disks.filter((d) => d.use === "free").length} discos listos para crear el primero.`}</span>
            {bayRow}
          </div>
        </div>
      </>
    );
  }

  const st = volumeStatus(v);
  const pct = v.size ? v.used / v.size : 0;
  const cached = vols.find((x) => x.cache);
  const busy = v.state === "scrubbing" || v.state === "building";
  return (
    <>
      <Head icon={HardDrive} right={`${vols.length} ${vols.length === 1 ? "volumen" : "volúmenes"}`}>
        Almacenamiento
      </Head>
      <div className="tw-vol">
        <Donut size={86} stroke={9} segments={[{ value: pct, color: v.state === "degraded" ? "var(--tg-crit)" : "var(--tg-accent)" }]}>
          <div>
            <div className="tw-ring-pct" style={{ fontSize: 20 }}>
              {Math.round(pct * 100)}
              <small>%</small>
            </div>
            <div className="tw-sub" style={{ fontSize: 10 }}>
              usado
            </div>
          </div>
        </Donut>
        <div className="tw-vol-info">
          <div className="tw-vol-name">
            <strong>{v.name}</strong>
            <span className="tw-sub">
              {profileName[v.profile]} · {fsName[v.fs] ?? v.fs}
            </span>
          </div>
          <span className="tw-status" data-tone={st.tone}>
            <st.icon size={12} strokeWidth={2.6} className={busy ? "tg-spin" : undefined} />
            {st.label}
          </span>
          {busy ? (
            <div className="tw-bar" role="progressbar" aria-valuenow={Math.round((v.progress ?? 0) * 100)}>
              <div style={{ width: `${(v.progress ?? 0) * 100}%`, background: "var(--tg-info)" }} />
            </div>
          ) : (
            <span className="tw-sub">
              {fmtBytes(v.size - v.used)} libres de {fmtBytes(v.size)}
            </span>
          )}
          {cached && (
            <span className="tw-sub tw-cache" title="Caché SSD: lecturas servidas desde la SSD sobre el total">
              <Zap size={11} strokeWidth={2.4} /> Caché SSD{cached.cache?.hitRatio != null ? ` · ${Math.round(cached.cache.hitRatio * 100)} % aciertos` : ""}
            </span>
          )}
          {bayRow}
        </div>
      </div>
    </>
  );
}

// ---- Actividad ----------------------------------------------------------------------

function ActivityWidget() {
  const jobs = useJobs();
  const list = [...jobs].sort((a, b) => Number(b.state === "running") - Number(a.state === "running")).slice(0, 3);
  const running = jobs.filter((j) => j.state === "running").length;
  return (
    <>
      <Head icon={Activity} right={running ? `${running} en curso` : undefined}>
        Actividad
      </Head>
      {list.length === 0 ? (
        <div className="tw-calm">
          <CheckCircle2 size={26} strokeWidth={2} />
          <strong>Todo tranquilo</strong>
          <span className="tw-sub">No hay tareas en curso.</span>
        </div>
      ) : (
        <div className="tw-jobs">
          {list.map((j) => {
            const done = j.state === "succeeded";
            return (
              <div key={j.id}>
                <div className="tw-job-head">
                  <span>{j.title}</span>
                  <span>{done ? "Listo" : `${Math.round(j.progress * 100)} %${j.rate ? ` · ${j.rate}` : ""}`}</span>
                </div>
                <div className="tw-bar" style={{ marginTop: 5 }}>
                  <div style={{ width: `${j.progress * 100}%`, background: done ? "var(--tg-ok)" : undefined }} />
                </div>
              </div>
            );
          })}
        </div>
      )}
    </>
  );
}

// ---- Reloj --------------------------------------------------------------------------

const TZ = "America/Argentina/Buenos_Aires";

function baNow() {
  // Hora de Buenos Aires sin depender de la zona del navegador.
  const parts = new Intl.DateTimeFormat("en-US", { timeZone: TZ, hour: "numeric", minute: "numeric", second: "numeric", hourCycle: "h23" }).formatToParts(new Date());
  const get = (t: string) => Number(parts.find((p) => p.type === t)?.value ?? 0);
  return { h: get("hour"), m: get("minute"), s: get("second") + (Date.now() % 1000) / 1000 };
}

const ClockFace = memo(function ClockFace({ size }: { size: number }) {
  const c = size / 2;
  return (
    <>
      <circle cx={c} cy={c} r={c - 1} fill="var(--tg-content-bg)" stroke="var(--tg-separator-strong)" strokeWidth="0.5" />
      {Array.from({ length: 60 }, (_, i) => {
        const a = (i / 60) * Math.PI * 2;
        const major = i % 5 === 0;
        const r1 = c - 5;
        const r2 = c - (major ? 9.5 : 7);
        return (
          <line
            key={i}
            x1={c + Math.sin(a) * r1}
            y1={c - Math.cos(a) * r1}
            x2={c + Math.sin(a) * r2}
            y2={c - Math.cos(a) * r2}
            stroke={major ? "var(--tg-text-1)" : "var(--tg-text-3)"}
            strokeWidth={major ? 1.6 : 0.7}
            strokeLinecap="round"
          />
        );
      })}
      {Array.from({ length: 12 }, (_, i) => {
        const n = i + 1;
        const a = (n / 12) * Math.PI * 2;
        const r = c - 19;
        return (
          <text key={n} x={c + Math.sin(a) * r} y={c - Math.cos(a) * r} textAnchor="middle" dominantBaseline="central" fontSize={11.5} fontWeight={600} fill="var(--tg-text-1)" style={{ fontVariantNumeric: "tabular-nums" }}>
            {n}
          </text>
        );
      })}
    </>
  );
});

function ClockWidget() {
  const size = 112;
  const c = size / 2;
  const hRef = useRef<SVGLineElement>(null);
  const mRef = useRef<SVGLineElement>(null);
  const sRef = useRef<SVGGElement>(null);
  const [label, setLabel] = useState(() => dateLabel());

  useEffect(() => {
    const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
    let raf = 0;
    let timer: ReturnType<typeof setInterval> | undefined;
    const draw = () => {
      const { h, m, s } = baNow();
      const sec = reduce ? Math.floor(s) : s;
      hRef.current?.setAttribute("transform", `rotate(${((h % 12) + m / 60) * 30} ${c} ${c})`);
      mRef.current?.setAttribute("transform", `rotate(${(m + sec / 60) * 6} ${c} ${c})`);
      sRef.current?.setAttribute("transform", `rotate(${sec * 6} ${c} ${c})`);
    };
    const loop = () => {
      draw();
      raf = requestAnimationFrame(loop);
    };
    if (reduce) {
      draw();
      timer = setInterval(draw, 1000);
    } else loop();
    const lab = setInterval(() => setLabel(dateLabel()), 30_000);
    return () => {
      cancelAnimationFrame(raf);
      clearInterval(timer);
      clearInterval(lab);
    };
  }, [c]);

  return (
    <>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} role="img" aria-label={`Hora en Buenos Aires: ${label.time}`}>
        <ClockFace size={size} />
        <line ref={hRef} x1={c} y1={c + 6} x2={c} y2={c - 26} stroke="var(--tg-text-1)" strokeWidth={3.6} strokeLinecap="round" />
        <line ref={mRef} x1={c} y1={c + 8} x2={c} y2={c - 40} stroke="var(--tg-text-1)" strokeWidth={2.6} strokeLinecap="round" />
        <g ref={sRef}>
          <line x1={c} y1={c + 11} x2={c} y2={c - 44} stroke="#F97316" strokeWidth={1.2} strokeLinecap="round" />
          <circle cx={c} cy={c} r={3} fill="#F97316" />
        </g>
        <circle cx={c} cy={c} r={1.2} fill="var(--tg-content-bg)" />
      </svg>
      <div className="tw-clock-city">
        Buenos Aires
        <small>{label.date}</small>
      </div>
    </>
  );
}

function dateLabel() {
  const d = new Date();
  const date = new Intl.DateTimeFormat("es-AR", { timeZone: TZ, weekday: "short", day: "numeric", month: "short" }).format(d).replace(/\./g, "");
  const time = new Intl.DateTimeFormat("es-AR", { timeZone: TZ, hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(d);
  return { date: date.charAt(0).toUpperCase() + date.slice(1), time };
}

// ---- Sistema ------------------------------------------------------------------------

function SystemWidget() {
  const [m] = useLast();
  const info = useLive("system.info", undefined).data;
  const sessions = useLive("sessions.list", undefined).data;
  const loadedAt = useRef<number>(0);
  if (info && !loadedAt.current) loadedAt.current = Date.now();
  const uptime = info ? info.uptimeSec + (Date.now() - loadedAt.current) / 1000 : 0;
  const temp = m?.tempCpu ?? 0;
  return (
    <>
      <Head icon={Server} right={info?.hostname}>
        Sistema
      </Head>
      <div className="tw-sys">
        <div className="tw-sys-row">
          <Timer size={13} />
          <span>Encendido</span>
          <b>{info ? fmtDuration(uptime) : "—"}</b>
        </div>
        <div className="tw-sys-row">
          <Thermometer size={13} />
          <span>CPU</span>
          <b style={{ color: temp > 75 ? "var(--tg-crit)" : temp > 65 ? "var(--tg-warn)" : undefined }}>{fmtCpuTemp(m?.tempCpu)}</b>
        </div>
        <div className="tw-sys-row">
          <Fan size={13} />
          <span>Ventilador</span>
          <b>{fmtRpm(m?.fanRpm)}</b>
        </div>
        <div className="tw-sys-row">
          <Users size={13} />
          <span>Sesiones</span>
          <b>{sessions ? sessions.length : "—"}</b>
        </div>
      </div>
    </>
  );
}

// ---- Catálogo -----------------------------------------------------------------------

export const catalog: WidgetDef[] = [
  { id: "clock", name: "Reloj", description: "Hora de Buenos Aires", size: "small", icon: Clock3, className: "tw-clock", Component: ClockWidget },
  { id: "cpu", name: "CPU", description: "Uso por núcleo y temperatura", size: "small", icon: Cpu, app: MONITOR, Component: CpuWidget },
  { id: "net", name: "Red", description: "Bajada y subida en vivo", size: "medium", icon: Network, app: MONITOR, Component: NetworkWidget },
  { id: "storage", name: "Almacenamiento", description: "Volumen, salud y bahías", size: "medium", icon: HardDrive, app: STORAGE, Component: StorageWidget },
  { id: "mem", name: "Memoria", description: "RAM en uso y caché", size: "small", icon: MemoryStick, app: MONITOR, Component: MemoryWidget },
  { id: "system", name: "Sistema", description: "Encendido, temperatura y sesiones", size: "small", icon: Server, app: MONITOR, Component: SystemWidget },
  { id: "activity", name: "Actividad", description: "Tareas en curso", size: "medium", icon: Activity, Component: ActivityWidget },
];

export const sizeUnits: Record<WidgetSize, number> = { small: 1, medium: 2, large: 4 };
