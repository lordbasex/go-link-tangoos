import { useState } from "react";
import { AlertTriangle, ArrowDownToLine, ArrowUpFromLine, Bell, CheckCircle2, FolderOpen, HardDrive, HardDriveDownload, Info, Plus, ShieldCheck, Stethoscope, XOctagon } from "lucide-react";
import { fmtAgo, fmtBytes, fmtRate, useJobs, useLive, useMetrics, useTransport, fmtTemp, maxTemp, profileInfo } from "@tango/client";
import type { Disk, Volume } from "@tango/client";
import { AreaChart, Badge, Button, Progress, Ring, Status } from "@tango/ui";
import { openApp, toast } from "@tango/shell";
import { Chassis } from "./Chassis";
import { profileName, scrubAgo, useAvailable } from "./util";
import { taskLine } from "./Maintenance";

export function stateInfo(v: Volume): { tone: "ok" | "warn" | "crit" | "info"; text: string } {
  if (v.task === "rebuild") return { tone: "warn", text: "Reconstruyendo: todavía sin redundancia" };
  if (v.task === "reshape") return { tone: "info", text: "Ampliando" };
  if (v.task === "resync") return { tone: "info", text: "Sincronizando el array" };
  switch (v.state) {
    case "healthy":
      return { tone: "ok", text: "En orden" };
    case "degraded":
      return { tone: "crit", text: "Sin redundancia" };
    case "scrubbing":
      return { tone: "info", text: "Verificando" };
    case "building":
      return { tone: "info", text: "Sincronizando el array" };
    default:
      return { tone: "warn", text: "Solo lectura" };
  }
}

export function VolumeCard({ v, disks }: { v: Volume; disks: Disk[] }) {
  const s = stateInfo(v);
  const pct = v.size ? v.used / v.size : 0;
  const members = disks.filter((d) => v.disks.includes(d.id) || d.volume === v.id);
  return (
    <section className="tg-card ts-vol">
      <Ring value={pct} size={112} stroke={11} color={v.state === "degraded" ? "var(--tg-crit)" : pct > 0.85 ? "var(--tg-warn)" : "var(--tg-accent)"}>
        <div className="ts-ring-text">
          <strong className="tg-num">{Math.round(pct * 100)} %</strong>
          <small>usado</small>
        </div>
      </Ring>
      <div className="ts-vol-body">
        <div className="ts-vol-head">
          <h3>{v.name}</h3>
          <Badge>{profileName[v.profile]}</Badge>
          <Badge>Btrfs</Badge>
        </div>
        <small className="ts-vol-tolerance" data-none={v.profile === "basic" || undefined}>
          {profileInfo(v.profile).tolerance}
        </small>
        <div className="ts-vol-size tg-num">
          {fmtBytes(v.used)} de {fmtBytes(v.size)} · <span className="tg-muted">{fmtBytes(v.size - v.used)} libres</span>
        </div>
        <Status tone={s.tone}>{s.text}</Status>
        {(v.task || v.state === "scrubbing" || v.state === "building") && (
          <div className="ts-vol-progress">
            <Progress value={v.progress ?? 0} tone={v.task === "rebuild" ? "warn" : undefined} />
            <span className="tg-num tg-muted">{Math.round((v.progress ?? 0) * 100)} %</span>
          </div>
        )}
        {v.task && <small className="ts-vol-task tg-num">{taskLine(v)}</small>}
        <div className="ts-vol-meta tg-muted">
          {members.length} {members.length === 1 ? "disco" : "discos"} · {v.mount}
        </div>
      </div>
      {v.growth30d.some((g) => g > 0) && (
        <div className="ts-vol-growth">
          <small className="tg-muted">Uso, últimos 30 días</small>
          <AreaChart values={v.growth30d} max={1} width={180} height={46} />
        </div>
      )}
    </section>
  );
}

function Health({ vols, disks }: { vols: Volume[]; disks: Disk[] }) {
  const t = useTransport();
  const v = vols[0];
  const bad = disks.filter((d) => d.use === "failed" || d.smart === "crit");
  const warn = disks.filter((d) => d.smart === "warn");
  const scrub = async () => {
    if (!v) return;
    try {
      await t.call("storage.scrub.start", { volume: v.id });
      toast({ level: "info", title: `Verificando ${v.name}`, body: "Se leen todos los datos y se comparan con sus checksums." });
    } catch (e) {
      toast({ level: "warn", title: "No se pudo verificar", body: (e as Error).message });
    }
  };
  const tone = bad.length ? "crit" : warn.length ? "warn" : "ok";
  return (
    <section className="tg-card ts-health">
      <div className="ts-card-title">
        <ShieldCheck size={16} /> Salud
      </div>
      <Status tone={tone} size={16}>
        {bad.length ? `${bad.length} disco con falla` : warn.length ? `${warn.length} disco con advertencias` : "Todo en orden"}
      </Status>
      <dl className="ts-dl">
        <dt>Discos</dt>
        <dd>
          {disks.filter((d) => d.bay).length} en bahías · {disks.filter((d) => d.kind === "nvme").length} NVMe
        </dd>
        {v && (
          <>
            <dt>Última verificación</dt>
            <dd>{scrubAgo(v)}</dd>
          </>
        )}
        <dt>Temperatura máxima</dt>
        <dd className="tg-num">{fmtTemp(maxTemp(disks.map((d) => d.tempC)))}</dd>
      </dl>
      {v && (
        <Button onClick={scrub} disabled={v.state !== "healthy"}>
          {v.state === "scrubbing" ? "Verificando…" : "Verificar ahora"}
        </Button>
      )}
    </section>
  );
}

function Doctor({ disk, vol, disks, onDisk, onReplace, canReplace }: { disk: Disk; vol: Volume; disks: Disk[]; onDisk: () => void; onReplace: () => void; canReplace: boolean }) {
  const healthy = disks.filter((d) => d.volume === vol.id && d.use !== "failed");
  const where = healthy.length === 1 ? (healthy[0].bay ? `el disco de la bahía ${healthy[0].bay}` : "el otro disco") : "los otros discos";
  return (
    <section className="ts-doctor" role="alert">
      <div className="ts-doctor-icon">
        <Stethoscope size={22} />
      </div>
      <div className="ts-doctor-body">
        <h3>El disco de la bahía {disk.bay} dejó de responder</h3>
        <p>
          Tus datos de {vol.name} están completos en {where}, pero <strong>ahora no hay copia de respaldo</strong>: si {healthy.length === 1 ? "ese disco también falla" : "falla otro disco más"}, se pierden. Reemplazá el disco cuanto antes.
        </p>
        <ol>
          <li>Comprá un disco de {fmtBytes(disk.size, 0)} o más grande (cualquier marca sirve).</li>
          <li>Sacá la bandeja de la bahía {disk.bay} (la del LED rojo). No hace falta apagar el NAS.</li>
          <li>Poné el disco nuevo y tocá «Reemplazar disco…»: TangoOS copia los datos y el volumen recupera la redundancia.</li>
        </ol>
        <div className="ts-doctor-actions">
          <Button variant="primary" onClick={onReplace} disabled={!canReplace} title={canReplace ? undefined : "Este TangoOS todavía no permite reemplazar discos"}>
            Reemplazar disco…
          </Button>
          <Button onClick={onDisk}>Ver el disco</Button>
          <span className="tg-muted">
            {disk.model} · serie {disk.serial} · {disk.reallocated.toLocaleString("es-AR")} sectores reasignados
          </span>
        </div>
      </div>
    </section>
  );
}

function Migrate({ disks }: { disks: Disk[] }) {
  const t = useTransport();
  const jobs = useJobs();
  const foreign = disks.filter((d) => d.foreign);
  const f = foreign[0].foreign!;
  const [jobId, setJobId] = useState<string | null>(null);
  const [seen, setSeen] = useState(false);
  const job = jobs.find((j) => j.id === jobId);
  if (job && !seen) setSeen(true);
  const mounted = !!jobId && ((job && job.state === "succeeded") || (seen && !job));
  const mount = async () => {
    const r = await t.call("storage.foreign.mount", { label: f.label });
    setJobId(r.job);
  };
  return (
    <section className="ts-migrate">
      <div className="ts-migrate-icon">
        <HardDriveDownload size={26} />
      </div>
      <div className="ts-migrate-body">
        <h3>Encontramos discos de otro NAS</h3>
        <p className="ts-migrate-meta">
          {f.label} · {f.profile} · bahías {foreign.map((d) => d.bay).join(" y ")}
        </p>
        <p>
          La mayoría de los NAS comerciales usa mdadm + LVM + Btrfs, el mismo stack que TangoOS. Podés montar el volumen en <strong>solo lectura</strong> y revisar tus archivos: nada se escribe en los discos.
        </p>
        {mounted ? (
          <div className="ts-migrate-ok">
            <Status tone="ok">Montado en /import/volume_1 · solo lectura</Status>
            <div className="ts-row-actions">
              <Button variant="primary" onClick={() => openApp("tango.files", { path: "/" })}>
                <FolderOpen size={14} /> Abrir en Archivos
              </Button>
              <Button disabled title="Llega en M4">
                Importar… (M4)
              </Button>
            </div>
          </div>
        ) : jobId ? (
          <div className="ts-vol-progress">
            <Progress value={job?.progress ?? 0} />
            <span className="tg-num tg-muted">Ensamblando…</span>
          </div>
        ) : (
          <div className="ts-row-actions">
            <Button variant="primary" onClick={mount}>
              Montar en solo lectura
            </Button>
            <Button disabled title="Llega en M4">
              Importar… (M4)
            </Button>
          </div>
        )}
      </div>
    </section>
  );
}

const alertIcon = { crit: XOctagon, warn: AlertTriangle, info: Info } as const;

/** Alertas activas del almacenamiento (md, Btrfs, SMART, espacio). Las resueltas, colapsadas. */
function Alerts() {
  const available = useAvailable("alerts.list");
  const alerts = useLive("alerts.list", undefined, ["alerts.changed", "storage.changed"]).data ?? [];
  const [showOld, setShowOld] = useState(false);
  if (!available) return null;
  const active = alerts.filter((a) => !a.resolved);
  const old = alerts.filter((a) => a.resolved);
  if (!active.length && !old.length) return null;
  return (
    <section className="tg-card">
      <div className="ts-card-title">
        <Bell size={16} /> Alertas {active.length > 0 && <span className="tg-muted ts-card-sub">{active.length} activas</span>}
      </div>
      <div className="ts-alerts">
        {active.length === 0 && (
          <p className="tg-muted ts-snap-empty">
            <CheckCircle2 size={13} /> No hay alertas activas.
          </p>
        )}
        {[...active, ...(showOld ? old : [])].map((a) => {
          const I = alertIcon[a.level];
          return (
            <div key={a.id} className="ts-alert" data-level={a.level} data-resolved={a.resolved || undefined}>
              <I size={17} />
              <div className="ts-alert-body">
                <strong>{a.title}</strong>
                <p>{a.body}</p>
              </div>
              <time dateTime={a.at}>{a.resolved ? `Resuelta · ${fmtAgo(a.at)}` : fmtAgo(a.at)}</time>
            </div>
          );
        })}
        {old.length > 0 && (
          <div className="ts-alerts-more">
            <Button variant="plain" onClick={() => setShowOld((x) => !x)}>
              {showOld ? "Ocultar resueltas" : `Ver resueltas (${old.length})`}
            </Button>
          </div>
        )}
      </div>
    </section>
  );
}

function DiskIO() {
  const m = useMetrics();
  const last = m[m.length - 1];
  const read = m.map((f) => f.diskRead);
  const write = m.map((f) => f.diskWrite);
  return (
    <section className="tg-card ts-io">
      <div className="ts-card-title">
        <HardDrive size={16} /> Actividad de discos <span className="tg-muted ts-card-sub">últimos 60 s</span>
      </div>
      <div className="ts-io-legend tg-num">
        <span className="ts-io-read">
          <ArrowDownToLine size={13} /> Lectura {fmtRate(last?.diskRead ?? 0)}
        </span>
        <span className="ts-io-write">
          <ArrowUpFromLine size={13} /> Escritura {fmtRate(last?.diskWrite ?? 0)}
        </span>
      </div>
      <AreaChart values={read} secondary={{ values: write, color: "var(--tg-warn)" }} width={600} height={70} color="var(--tg-accent)" />
    </section>
  );
}

export function Overview({ disks, vols, onDisk, onCreate, onReplace }: { disks: Disk[]; vols: Volume[]; onDisk: (d: Disk) => void; onCreate: () => void; onReplace: (v: Volume) => void }) {
  const canReplace = useAvailable("storage.disks.replacePlan");
  const failed = disks.find((d) => d.use === "failed" && vols.some((v) => v.id === d.volume && v.state === "degraded" && v.task !== "rebuild"));
  const failedVol = failed ? vols.find((v) => v.id === failed.volume) : undefined;
  const hasForeign = disks.some((d) => d.foreign);
  return (
    <div className="ts-overview">
      {failed && failedVol && <Doctor disk={failed} vol={failedVol} disks={disks} onDisk={() => onDisk(failed)} onReplace={() => onReplace(failedVol)} canReplace={canReplace} />}
      <Alerts />
      <section className="tg-card ts-chassis-card">
        <Chassis disks={disks} vols={vols} onSelect={onDisk} />
      </section>
      {hasForeign && <Migrate disks={disks} />}
      {vols.length === 0 ? (
        <section className="ts-hero">
          <div className="ts-hero-art" aria-hidden="true">
            <HardDrive size={40} strokeWidth={1.3} />
            <Plus size={18} strokeWidth={2.4} className="ts-hero-plus" />
          </div>
          <h2>Creá tu primer volumen</h2>
          <p>Un volumen junta uno o más discos en un solo espacio protegido. Después creás carpetas compartidas adentro.</p>
          <Button variant="primary" size="lg" onClick={onCreate}>
            Crear volumen…
          </Button>
          <small className="tg-muted">
            <CheckCircle2 size={12} /> Ningún disco se borra sin que lo confirmes
          </small>
        </section>
      ) : (
        <div className="ts-grid">
          {vols.map((v) => (
            <VolumeCard key={v.id} v={v} disks={disks} />
          ))}
          <Health vols={vols} disks={disks} />
        </div>
      )}
      <DiskIO />
    </div>
  );
}
