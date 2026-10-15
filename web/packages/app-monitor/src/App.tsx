import "./monitor.css";
import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Activity, ArrowDown, ArrowUp, Cable, Cpu, Fan, FolderOpen, Globe, HardDrive, Laptop, MemoryStick, Monitor as MonitorIcon, Network, Radio, SquareTerminal, Thermometer } from "lucide-react";
import { fmtAgo, fmtBytes, fmtPct, fmtRate, useLive, useMetrics, useTransport, fmtMem, fmtTemp, useIsReal } from "@tango/client";
import type { Disk, MetricsFrame, RtcStats, Session } from "@tango/client";
import { WindowToolbar } from "@tango/shell";
import type { AppProps } from "@tango/shell";
import { Badge, DemoBadge, Segmented, Status } from "@tango/ui";
import { Chart, Spark } from "./Chart";

type Tab = "resumen" | "cpu" | "memoria" | "red" | "discos" | "conexiones" | "webrtc";

const TABS: { value: Tab; label: string }[] = [
  { value: "resumen", label: "Resumen" },
  { value: "cpu", label: "CPU" },
  { value: "memoria", label: "Memoria" },
  { value: "red", label: "Red" },
  { value: "discos", label: "Discos" },
  { value: "conexiones", label: "Conexiones" },
  { value: "webrtc", label: "WebRTC" },
];

const C = {
  cpu: "var(--tg-accent)",
  mem: "#8b5cf6",
  cache: "#c4b5fd",
  rx: "#22c55e",
  tx: "#f97316",
  read: "#2f7cf6",
  write: "#ec4899",
};

const pct = (v: number) => fmtPct(v);
const gb = (n: number) => fmtMem(n);

/** Series derivadas de las métricas: se recalculan solo cuando llega un frame nuevo. */
function useSeries(frames: MetricsFrame[]) {
  return useMemo(() => {
    const pick = (f: (m: MetricsFrame) => number) => frames.map(f);
    return {
      times: pick((m) => m.t),
      cpu: pick((m) => m.cpu),
      mem: pick((m) => m.memUsed),
      cache: pick((m) => m.memUsed + m.memCache),
      rx: pick((m) => m.netRx),
      tx: pick((m) => m.netTx),
      read: pick((m) => m.diskRead),
      write: pick((m) => m.diskWrite),
      cores: (frames[0]?.perCore ?? []).map((_, i) => frames.map((m) => m.perCore[i] ?? 0)),
    };
  }, [frames]);
}

export default function App({ win }: AppProps) {
  const realData = useIsReal("rtc.stats");
  const [tab, setTab] = useState<Tab>(TABS.some((t) => t.value === win.args.tab) ? (win.args.tab as Tab) : "resumen");
  useEffect(() => {
    if (TABS.some((t) => t.value === win.args.tab)) setTab(win.args.tab as Tab);
  }, [win.args.tab]);

  const frames = useMetrics();
  const s = useSeries(frames);
  const last = frames[frames.length - 1];

  return (
    <div className="tg-app">
      <WindowToolbar win={win}>
        <span className="tg-tb-title">Monitor</span>
        <span className="tg-tb-spacer" />
        <div data-no-drag>
          <Segmented label="Sección" value={tab} onChange={setTab} options={TABS} />
        </div>
        <span className="tg-tb-spacer" />
        {!realData && <DemoBadge />}
      </WindowToolbar>
      <div className="tg-app-main">
        <div className="tg-app-content">
          {!last ? (
            <div className="tm-root tg-muted">Esperando las primeras métricas del canal «metrics»…</div>
          ) : (
            <div className="tm-root">
              {tab === "resumen" && <Overview s={s} last={last} />}
              {tab === "cpu" && <CpuTab s={s} last={last} />}
              {tab === "memoria" && <MemTab s={s} last={last} />}
              {tab === "red" && <NetTab s={s} last={last} />}
              {tab === "discos" && <DisksTab s={s} last={last} />}
              {tab === "conexiones" && <SessionsTab />}
              {tab === "webrtc" && <RtcTab />}
            </div>
          )}
        </div>
        <div className="tg-app-foot">
          <Activity size={12} />
          Canal «metrics» · 4 muestras por segundo · últimos 60 s
          <span style={{ flex: 1 }} />
          {last && `Carga ${last.load.map((l) => l.toFixed(2)).join(" · ")}`}
        </div>
      </div>
    </div>
  );
}

type S = ReturnType<typeof useSeries>;

function Card({ icon, title, children, right }: { icon: ReactNode; title: string; children: ReactNode; right?: ReactNode }) {
  return (
    <section className="tm-card">
      <div className="tm-card-head">
        <h3>
          {icon}
          {title}
        </h3>
        <span className="tm-spacer" />
        {right}
      </div>
      {children}
    </section>
  );
}

function Pair({ items }: { items: { label: string; value: string; color?: string; icon?: ReactNode }[] }) {
  return (
    <div className="tm-pair">
      {items.map((i) => (
        <div key={i.label}>
          <span>
            {i.color && <i className="tm-legend" style={{ background: i.color }} />}
            {i.icon}
            {i.label}
          </span>
          <b>{i.value}</b>
        </div>
      ))}
    </div>
  );
}

function Stat({ label, value, unit }: { label: string; value: string; unit?: string }) {
  return (
    <div className="tm-stat">
      <span>{label}</span>
      <b>{value}</b>
      {unit && <small>{unit}</small>}
    </div>
  );
}

// ---- Resumen ------------------------------------------------------------------------

function Overview({ s, last }: { s: S; last: MetricsFrame }) {
  const { data: info } = useLive("system.info", undefined);
  return (
    <>
      <div className="tm-grid4">
        <Card icon={<Cpu size={13} />} title="CPU" right={<span className="tg-muted" style={{ fontSize: 11 }}>{info ? `${info.cpu} · ${info.cores} núcleos` : ""}</span>}>
          <div className="tm-big">{pct(last.cpu)}</div>
          <Chart series={[{ label: "CPU", values: s.cpu, color: C.cpu }]} max={1} format={pct} times={s.times} height={86} />
        </Card>
        <Card icon={<MemoryStick size={13} />} title="Memoria">
          <div className="tm-big">
            {gb(last.memUsed)}
            <small>de {gb(last.memTotal)}</small>
          </div>
          <Chart series={[{ label: "Usada", values: s.mem, color: C.mem }]} max={last.memTotal} format={gb} times={s.times} height={86} />
        </Card>
        <Card icon={<Network size={13} />} title="Red">
          <Pair items={[{ label: "Recibido", value: fmtRate(last.netRx), color: C.rx }, { label: "Enviado", value: fmtRate(last.netTx), color: C.tx }]} />
          <Chart series={[{ label: "Recibido", values: s.rx, color: C.rx }, { label: "Enviado", values: s.tx, color: C.tx }]} format={fmtRate} times={s.times} height={86} />
        </Card>
        <Card icon={<HardDrive size={13} />} title="Disco">
          <Pair items={[{ label: "Lectura", value: fmtRate(last.diskRead), color: C.read }, { label: "Escritura", value: fmtRate(last.diskWrite), color: C.write }]} />
          <Chart series={[{ label: "Lectura", values: s.read, color: C.read }, { label: "Escritura", values: s.write, color: C.write }]} format={fmtRate} times={s.times} height={86} />
        </Card>
      </div>
      <div className="tm-stats">
        <div className="tm-stat">
          <span>
            <Thermometer size={11} style={{ verticalAlign: -1 }} /> Temperatura de CPU
          </span>
          <b>{last.tempCpu > 0 ? Math.round(last.tempCpu) : "—"}</b>
          {last.tempCpu > 0 && <small>°C</small>}
          <div style={{ marginTop: 4, display: last.tempCpu > 0 ? undefined : "none" }}>
            <Status tone={last.tempCpu > 80 ? "crit" : last.tempCpu > 68 ? "warn" : "ok"} size={12}>
              <span style={{ fontSize: 11 }}>{last.tempCpu > 80 ? "Alta" : last.tempCpu > 68 ? "Elevada" : "Normal"}</span>
            </Status>
          </div>
        </div>
        <div className="tm-stat">
          <span>
            <Fan size={11} style={{ verticalAlign: -1 }} /> Ventilador
          </span>
          <b>{last.fanRpm > 0 ? last.fanRpm.toLocaleString("es-AR") : "—"}</b>
          {last.fanRpm > 0 && <small>rpm</small>}
        </div>
        <Stat label="Carga (1 min)" value={last.load[0].toFixed(2)} />
        <Stat label="Carga (5 / 15 min)" value={`${last.load[1].toFixed(2)} · ${last.load[2].toFixed(2)}`} />
        <Stat label="Memoria en caché" value={gb(last.memCache)} />
      </div>
    </>
  );
}

// ---- CPU -------------------------------------------------------------------------------

function CpuTab({ s, last }: { s: S; last: MetricsFrame }) {
  const { data: info } = useLive("system.info", undefined);
  return (
    <>
      <Card icon={<Cpu size={13} />} title="Uso total" right={<span className="tm-big" style={{ fontSize: 20 }}>{pct(last.cpu)}</span>}>
        <Chart series={[{ label: "CPU", values: s.cpu, color: C.cpu }]} max={1} format={pct} times={s.times} height={160} />
      </Card>
      <div className="tm-head">
        <h2>Por núcleo</h2>
        <span className="tm-sub">{info?.cpu}</span>
      </div>
      <div className="tm-cores">
        {s.cores.map((vals, i) => (
          <div key={i} className="tm-core">
            <header>
              Núcleo {i}
              <b>{pct(last.perCore[i] ?? 0)}</b>
            </header>
            <Chart series={[{ label: `Núcleo ${i}`, values: vals, color: C.cpu }]} max={1} format={pct} times={s.times} height={54} compact />
          </div>
        ))}
      </div>
      <div className="tm-stats">
        <Stat label="Carga 1 min" value={last.load[0].toFixed(2)} />
        <Stat label="Carga 5 min" value={last.load[1].toFixed(2)} />
        <Stat label="Carga 15 min" value={last.load[2].toFixed(2)} />
        <Stat label="Temperatura" value={last.tempCpu > 0 ? String(Math.round(last.tempCpu)) : "—"} unit={last.tempCpu > 0 ? "°C" : undefined} />
      </div>
    </>
  );
}

// ---- Memoria ---------------------------------------------------------------------------

function MemTab({ s, last }: { s: S; last: MetricsFrame }) {
  const free = Math.max(0, last.memTotal - last.memUsed - last.memCache);
  const parts = [
    { label: "Usada por servicios", v: last.memUsed, color: C.mem },
    { label: "Caché de archivos", v: last.memCache, color: C.cache },
    { label: "Libre", v: free, color: "transparent" },
  ];
  return (
    <>
      <Card icon={<MemoryStick size={13} />} title="Uso de memoria" right={<span className="tg-muted tg-num">{gb(last.memTotal)} en total</span>}>
        <div className="tm-stack" role="img" aria-label={`Usada ${gb(last.memUsed)}, caché ${gb(last.memCache)}, libre ${gb(free)}`}>
          {parts.map((p) => (
            <div key={p.label} style={{ width: `${(p.v / last.memTotal) * 100}%`, background: p.color }} />
          ))}
        </div>
        <div className="tm-stack-legend">
          {parts.map((p) => (
            <span key={p.label}>
              <i className="tm-legend" style={{ background: p.color === "transparent" ? "var(--tg-track)" : p.color }} />
              {p.label} <b>{gb(p.v)}</b>
            </span>
          ))}
        </div>
      </Card>
      <Card icon={<Activity size={13} />} title="Últimos 60 segundos">
        <Chart
          series={[
            { label: "Usada + caché", values: s.cache, color: C.cache },
            { label: "Usada", values: s.mem, color: C.mem },
          ]}
          max={last.memTotal}
          format={gb}
          times={s.times}
          height={170}
        />
      </Card>
      <p className="tg-muted" style={{ margin: 0, fontSize: 12, maxWidth: "70ch", lineHeight: 1.5 }}>
        La caché de archivos acelera las lecturas repetidas desde los volúmenes. Linux la libera sola cuando un servicio necesita memoria, así que no cuenta como ocupada.
      </p>
    </>
  );
}

// ---- Red -------------------------------------------------------------------------------

function NetTab({ s, last }: { s: S; last: MetricsFrame }) {
  const { data: info } = useLive("system.info", undefined);
  const peakRx = Math.max(...s.rx);
  const peakTx = Math.max(...s.tx);
  return (
    <>
      <Card icon={<Network size={13} />} title="Tráfico">
        <Pair
          items={[
            { label: "Recibido", value: fmtRate(last.netRx), color: C.rx, icon: <ArrowDown size={11} /> },
            { label: "Enviado", value: fmtRate(last.netTx), color: C.tx, icon: <ArrowUp size={11} /> },
          ]}
        />
        <Chart series={[{ label: "Recibido", values: s.rx, color: C.rx }, { label: "Enviado", values: s.tx, color: C.tx }]} format={fmtRate} times={s.times} height={180} />
      </Card>
      <div className="tm-stats">
        <Stat label="Interfaz" value="enp1s0" />
        <Stat label="Dirección IP" value={info?.ip ?? "—"} />
        <Stat label="Enlace" value="1 Gb/s" unit="full dúplex" />
        <Stat label="Pico recibido (60 s)" value={fmtRate(peakRx)} />
        <Stat label="Pico enviado (60 s)" value={fmtRate(peakTx)} />
        <Stat label="MTU" value="1500" />
      </div>
    </>
  );
}

// ---- Discos ----------------------------------------------------------------------------

/** Reparto demo y determinístico del I/O total entre discos (RAID1: lecturas balanceadas, escrituras duplicadas). */
function splitIO(disks: Disk[], read: number, write: number, salt: number) {
  const members = disks.filter((d) => d.use === "member");
  return disks.map((d, i) => {
    const wobble = 0.9 + 0.2 * Math.abs(Math.sin(salt / 900 + i * 1.7));
    if (d.use === "system") return { r: read * 0.03 + 120e3, w: write * 0.05 + 80e3 };
    if (d.use === "member") return { r: (read / Math.max(1, members.length)) * wobble, w: write * wobble };
    return { r: 0, w: 0 };
  });
}

function DisksTab({ s, last }: { s: S; last: MetricsFrame }) {
  const disks = useLive("storage.disks.list", undefined, ["storage.changed"]).data ?? [];
  const io = splitIO(disks, last.diskRead, last.diskWrite, last.t);
  return (
    <>
      <Card icon={<HardDrive size={13} />} title="Total de todos los discos">
        <Pair items={[{ label: "Lectura", value: fmtRate(last.diskRead), color: C.read }, { label: "Escritura", value: fmtRate(last.diskWrite), color: C.write }]} />
        <Chart series={[{ label: "Lectura", values: s.read, color: C.read }, { label: "Escritura", values: s.write, color: C.write }]} format={fmtRate} times={s.times} height={150} />
      </Card>
      <div className="tm-table-wrap">
        <table className="tg-table">
          <thead>
            <tr>
              <th>Disco</th>
              <th>Ubicación</th>
              <th>Estado</th>
              <th className="num">Lectura</th>
              <th className="num">Escritura</th>
              <th className="num">Temperatura</th>
            </tr>
          </thead>
          <tbody>
            {disks.map((d, i) => (
              <tr key={d.id}>
                <td>{d.model}</td>
                <td className="tg-muted">{d.bay ? `Bahía ${d.bay}` : "Interno (sistema)"}</td>
                <td>
                  {d.use === "failed" ? (
                    <Status tone="crit" size={12}>Sin respuesta</Status>
                  ) : d.use === "member" ? (
                    <Status tone={d.smart === "warn" || d.smart === "crit" ? "warn" : "ok"} size={12}>En {d.volume === "vol1" ? "volume1" : d.volume}</Status>
                  ) : d.use === "system" ? (
                    <Badge>Sistema</Badge>
                  ) : (
                    <Badge>Sin usar</Badge>
                  )}
                </td>
                <td className="num">{d.use === "failed" ? "—" : fmtRate(io[i]?.r ?? 0)}</td>
                <td className="num">{d.use === "failed" ? "—" : fmtRate(io[i]?.w ?? 0)}</td>
                <td className="num">{d.use === "failed" ? "—" : fmtTemp(d.tempC)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

// ---- Conexiones -------------------------------------------------------------------------

const clients: Record<Session["client"], { label: string; icon: ReactNode; via: string }> = {
  web: { label: "Navegador", icon: <Globe size={14} />, via: "WebRTC" },
  "desk-win": { label: "App de Windows", icon: <MonitorIcon size={14} />, via: "WebRTC" },
  "desk-linux": { label: "App de Linux", icon: <Laptop size={14} />, via: "WebRTC" },
  smb: { label: "Carpeta compartida", icon: <FolderOpen size={14} />, via: "SMB3" },
  ssh: { label: "Terminal", icon: <SquareTerminal size={14} />, via: "SSH" },
  remote: { label: "Navegador remoto", icon: <Globe size={14} />, via: "TangoConnect" },
  guest: { label: "Enlace público", icon: <Globe size={14} />, via: "TangoConnect" },
};

function SessionsTab() {
  const { data: sessions = [] } = useLive("sessions.list", undefined);
  const { data: users = [] } = useLive("users.list", undefined);
  return (
    <>
      <div className="tm-head">
        <h2>Conexiones activas</h2>
        <span className="tm-sub">{sessions.length} en este momento</span>
      </div>
      <div className="tm-table-wrap">
        <table className="tg-table">
          <thead>
            <tr>
              <th>Usuario</th>
              <th>Desde</th>
              <th>Protocolo</th>
              <th>Dirección IP</th>
              <th>Conectado</th>
            </tr>
          </thead>
          <tbody>
            {sessions.map((x) => {
              const c = clients[x.client];
              return (
                <tr key={x.id}>
                  <td>
                    {users.find((u) => u.name === x.user)?.full ?? x.user}
                    {x.current && (
                      <span className="tm-pill" style={{ marginLeft: 8 }}>
                        Esta sesión
                      </span>
                    )}
                  </td>
                  <td>
                    <span className="tm-client">
                      {c.icon}
                      {c.label}
                    </span>
                  </td>
                  <td>
                    <span className="tm-pill">{c.via}</span>
                  </td>
                  <td className="tg-num">{x.ip}</td>
                  <td className="tg-muted">{fmtAgo(x.since)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

// ---- WebRTC ------------------------------------------------------------------------------

const HIST = 60;

function RtcTab() {
  const t = useTransport();
  const [stats, setStats] = useState<RtcStats>();
  const hist = useRef(new Map<string, number[]>());
  const rtt = useRef<number[]>([]);
  const [, bump] = useState(0);

  useEffect(() => {
    let alive = true;
    const poll = () =>
      t.call("rtc.stats", undefined).then((st) => {
        if (!alive) return;
        for (const ch of st.channels) {
          const h = hist.current.get(ch.label) ?? [];
          h.push(ch.rate);
          if (h.length > HIST) h.shift();
          hist.current.set(ch.label, h);
        }
        rtt.current = [...rtt.current, st.rttMs].slice(-HIST);
        setStats(st);
        bump((n) => n + 1);
      });
    poll();
    const i = setInterval(poll, 1000);
    return () => {
      alive = false;
      clearInterval(i);
    };
  }, [t]);

  if (!stats) return <div className="tg-muted">Leyendo estadísticas de la conexión…</div>;
  const ok = stats.ice === "connected" || stats.ice === "completed";

  return (
    <>
      <div className="tm-rtc-hero">
        <div className="tm-rtc-pulse">
          <Radio size={22} />
        </div>
        <div>
          <h2>Toda la administración viaja por un único RTCPeerConnection cifrado</h2>
          <p>Archivos, eventos, métricas y la terminal comparten una conexión DTLS directa entre este navegador y el NAS. No hay API REST ni sondeos: el servidor empuja cada cambio en el momento.</p>
        </div>
      </div>

      <div className="tm-stats">
        <div className="tm-stat">
          <span>Estado ICE</span>
          <div style={{ marginTop: 3 }}>
            <Status tone={ok ? "ok" : "warn"}>{ok ? "Conectado" : stats.ice}</Status>
          </div>
        </div>
        <div className="tm-stat">
          <span>Latencia (RTT)</span>
          <b>{stats.rttMs.toFixed(1)}</b>
          <small>ms</small>
          <div style={{ marginTop: 4 }}>
            <Spark values={rtt.current} color="var(--tg-accent)" width={110} height={18} />
          </div>
        </div>
        <div className="tm-stat">
          <span>Tipo de candidato</span>
          <b>{stats.pair.type === "host" ? "Directo (LAN)" : stats.pair.type === "srflx" ? "STUN" : "TURN"}</b>
          <small>{stats.pair.protocol.toUpperCase()}</small>
        </div>
        <div className="tm-stat">
          <span>Cifrado</span>
          <b style={{ fontSize: 13 }}>{stats.dtls}</b>
        </div>
      </div>

      <Card icon={<Cable size={13} />} title="Par de candidatos seleccionado">
        <div className="tm-path">
          <span>Este equipo</span>
          <code>{stats.pair.local}</code>
          <span className="tm-wire" aria-hidden="true" />
          <code>{stats.pair.remote}</code>
          <span>tango.local</span>
        </div>
      </Card>

      <div className="tm-head">
        <h2>DataChannels</h2>
        <span className="tm-sub">{stats.channels.length} canales abiertos</span>
      </div>
      <div className="tm-table-wrap">
        <table className="tg-table">
          <thead>
            <tr>
              <th>Canal</th>
              <th>Entrega</th>
              <th className="num">En buffer</th>
              <th className="num">Tráfico</th>
              <th>Últimos 60 s</th>
              <th className="num">Mensajes</th>
            </tr>
          </thead>
          <tbody>
            {stats.channels.map((ch) => (
              <tr key={ch.label}>
                <td>
                  <code style={{ fontFamily: "ui-monospace, Menlo, monospace", fontSize: 12 }}>{ch.label}</code>
                </td>
                <td>
                  <span className="tm-pill">{ch.ordered ? "Ordenada, confiable" : "Sin orden, sin reintentos"}</span>
                </td>
                <td className="num">{fmtBytes(ch.buffered)}</td>
                <td className="num">{fmtRate(ch.rate)}</td>
                <td>
                  <Spark values={hist.current.get(ch.label) ?? []} color={ch.ordered ? "var(--tg-accent)" : C.rx} />
                </td>
                <td className="num">{ch.messages.toLocaleString("es-AR")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
