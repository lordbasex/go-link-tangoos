import { useState } from "react";
import { AlertTriangle, ArrowDownToLine, ArrowUpFromLine, Bell, CheckCircle2, FolderOpen, HardDrive, HardDriveDownload, Info, Plus, ShieldCheck, Stethoscope, XOctagon } from "lucide-react";
import { fmtAgo, fmtBytes, fmtRate, useJobs, useLive, useMetrics, useTransport, fmtTemp, maxTemp, profileInfo, locale, t, tn, tr } from "@tango/client";
import type { Disk, Volume } from "@tango/client";
import { AreaChart, Badge, Button, Progress, Ring, Status } from "@tango/ui";
import { openApp, toast } from "@tango/shell";
import { Chassis } from "./Chassis";
import { fsName, profileName, scrubAgo, useAvailable } from "./util";
import { taskLine } from "./Maintenance";

export function stateInfo(v: Volume): { tone: "ok" | "warn" | "crit" | "info"; text: string } {
  if (v.task === "rebuild") return { tone: "warn", text: t("Reconstruyendo: todavía sin redundancia") };
  if (v.task === "reshape") return { tone: "info", text: t("Ampliando") };
  if (v.task === "resync") return { tone: "info", text: t("Sincronizando el array") };
  switch (v.state) {
    case "healthy":
      return { tone: "ok", text: t("En orden") };
    case "degraded":
      return { tone: "crit", text: t("Sin redundancia") };
    case "scrubbing":
      return { tone: "info", text: t("Verificando", undefined, "scrub") };
    case "building":
      return { tone: "info", text: t("Sincronizando el array") };
    case "locked":
      return { tone: "warn", text: t("Bloqueado: está cifrado y cerrado") };
    default:
      return { tone: "warn", text: t("Solo lectura") };
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
          <small>{t("usado")}</small>
        </div>
      </Ring>
      <div className="ts-vol-body">
        <div className="ts-vol-head">
          <h3>{v.name}</h3>
          <Badge>{profileName[v.profile]}</Badge>
          <Badge>{fsName[v.fs] ?? v.fs}</Badge>
          {v.encrypted && <Badge tone={v.locked ? "warn" : undefined}>{v.locked ? t("Bloqueado") : t("Cifrado", undefined, "estado")}</Badge>}
          {v.cache && <Badge>{t("Caché SSD")}{v.cache.hitRatio != null ? ` · ${Math.round(v.cache.hitRatio * 100)} %` : ""}</Badge>}
        </div>
        <small className="ts-vol-tolerance" data-none={v.profile === "basic" || undefined}>
          {profileInfo(v.profile).tolerance}
        </small>
        <div className="ts-vol-size tg-num">
          {t("{used} de {size}", { used: fmtBytes(v.used), size: fmtBytes(v.size) })} · <span className="tg-muted">{t("{free} libres", { free: fmtBytes(v.size - v.used) })}</span>
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
          {v.kind === "pool" ? tn(members.length, "En un grupo de {n} disco", "En un grupo de {n} discos") : tn(members.length, "{n} disco", "{n} discos")} · {v.mount || t("sin montar")}
        </div>
      </div>
      {v.growth30d.some((g) => g > 0) && (
        <div className="ts-vol-growth">
          <small className="tg-muted">{t("Uso, últimos 30 días")}</small>
          <AreaChart values={v.growth30d} max={1} width={180} height={46} />
        </div>
      )}
    </section>
  );
}

function Health({ vols, disks }: { vols: Volume[]; disks: Disk[] }) {
  const rpc = useTransport();
  const v = vols[0];
  const bad = disks.filter((d) => d.use === "failed" || d.smart === "crit");
  const warn = disks.filter((d) => d.smart === "warn");
  const scrub = async () => {
    if (!v) return;
    try {
      await rpc.call("storage.scrub.start", { volume: v.id });
      toast({ level: "info", title: t("Verificando {name}", { name: v.name }), body: t("Se leen todos los datos y se comparan con sus checksums.") });
    } catch (e) {
      toast({ level: "warn", title: t("No se pudo verificar"), body: (e as Error).message });
    }
  };
  const tone = bad.length ? "crit" : warn.length ? "warn" : "ok";
  return (
    <section className="tg-card ts-health">
      <div className="ts-card-title">
        <ShieldCheck size={16} /> {t("Salud")}
      </div>
      <Status tone={tone} size={16}>
        {bad.length ? tn(bad.length, "{n} disco con falla", "{n} discos con falla") : warn.length ? tn(warn.length, "{n} disco con advertencias", "{n} discos con advertencias") : t("Todo en orden")}
      </Status>
      <dl className="ts-dl">
        <dt>{t("Discos")}</dt>
        <dd>
          {t("{bays} en bahías · {nvme} NVMe", { bays: disks.filter((d) => d.bay).length, nvme: disks.filter((d) => d.kind === "nvme").length })}
        </dd>
        {v && (
          <>
            <dt>{t("Última verificación")}</dt>
            <dd>{scrubAgo(v)}</dd>
          </>
        )}
        <dt>{t("Temperatura máxima")}</dt>
        <dd className="tg-num">{fmtTemp(maxTemp(disks.map((d) => d.tempC)))}</dd>
      </dl>
      {v && (
        <Button onClick={scrub} disabled={v.state !== "healthy"}>
          {v.state === "scrubbing" ? t("Verificando…") : t("Verificar ahora")}
        </Button>
      )}
    </section>
  );
}

function Doctor({ disk, vol, disks, onDisk, onReplace, canReplace }: { disk: Disk; vol: Volume; disks: Disk[]; onDisk: () => void; onReplace: () => void; canReplace: boolean }) {
  const healthy = disks.filter((d) => d.volume === vol.id && d.use !== "failed");
  const where = healthy.length === 1 ? (healthy[0].bay ? t("el disco de la bahía {bay}", { bay: healthy[0].bay }) : t("el otro disco")) : t("los otros discos");
  return (
    <section className="ts-doctor" role="alert">
      <div className="ts-doctor-icon">
        <Stethoscope size={22} />
      </div>
      <div className="ts-doctor-body">
        <h3>{t("El disco de la bahía {bay} dejó de responder", { bay: disk.bay ?? "" })}</h3>
        <p>
          {t("Tus datos de {name} están completos en {where}, pero", { name: vol.name, where })} <strong>{t("ahora no hay copia de respaldo")}</strong>: {healthy.length === 1 ? t("si ese disco también falla, se pierden. Reemplazá el disco cuanto antes.") : t("si falla otro disco más, se pierden. Reemplazá el disco cuanto antes.")}
        </p>
        <ol>
          <li>{t("Comprá un disco de {size} o más grande (cualquier marca sirve).", { size: fmtBytes(disk.size, 0) })}</li>
          <li>{t("Sacá la bandeja de la bahía {bay} (la del LED rojo). No hace falta apagar el NAS.", { bay: disk.bay ?? "" })}</li>
          <li>{t("Poné el disco nuevo y tocá «Reemplazar disco…»: TangoOS copia los datos y el volumen recupera la redundancia.")}</li>
        </ol>
        <div className="ts-doctor-actions">
          <Button variant="primary" onClick={onReplace} disabled={!canReplace} title={canReplace ? undefined : t("Este TangoOS todavía no permite reemplazar discos")}>
            {t("Reemplazar disco…")}
          </Button>
          <Button onClick={onDisk}>{t("Ver el disco")}</Button>
          <span className="tg-muted">
            {disk.model} · {t("serie {serial} · {n} sectores reasignados", { serial: disk.serial, n: disk.reallocated.toLocaleString(locale()) })}
          </span>
        </div>
      </div>
    </section>
  );
}

function Migrate({ disks }: { disks: Disk[] }) {
  const rpc = useTransport();
  const jobs = useJobs();
  const foreign = disks.filter((d) => d.foreign);
  const f = foreign[0].foreign!;
  const [jobId, setJobId] = useState<string | null>(null);
  const [seen, setSeen] = useState(false);
  const job = jobs.find((j) => j.id === jobId);
  if (job && !seen) setSeen(true);
  const mounted = !!jobId && ((job && job.state === "succeeded") || (seen && !job));
  const mount = async () => {
    const r = await rpc.call("storage.foreign.mount", { label: f.label });
    setJobId(r.job);
  };
  return (
    <section className="ts-migrate">
      <div className="ts-migrate-icon">
        <HardDriveDownload size={26} />
      </div>
      <div className="ts-migrate-body">
        <h3>{t("Encontramos discos de otro NAS")}</h3>
        <p className="ts-migrate-meta">
          {f.label} · {f.profile} · {t("bahías {bays}", { bays: foreign.map((d) => d.bay).join(t(" y ")) })}
        </p>
        <p>
          {t("La mayoría de los NAS comerciales usa mdadm + LVM + Btrfs, el mismo stack que TangoOS. Podés montar el volumen en")} <strong>{t("solo lectura")}</strong> {t("y revisar tus archivos: nada se escribe en los discos.")}
        </p>
        {mounted ? (
          <div className="ts-migrate-ok">
            <Status tone="ok">{t("Montado en {path} · solo lectura", { path: "/import/volume_1" })}</Status>
            <div className="ts-row-actions">
              <Button variant="primary" onClick={() => openApp("tango.files", { path: "/" })}>
                <FolderOpen size={14} /> {t("Abrir en Archivos")}
              </Button>
              <Button disabled title={t("Llega en M4")}>
                {t("Importar… (M4)")}
              </Button>
            </div>
          </div>
        ) : jobId ? (
          <div className="ts-vol-progress">
            <Progress value={job?.progress ?? 0} />
            <span className="tg-num tg-muted">{t("Ensamblando…")}</span>
          </div>
        ) : (
          <div className="ts-row-actions">
            <Button variant="primary" onClick={mount}>
              {t("Montar en solo lectura")}
            </Button>
            <Button disabled title={t("Llega en M4")}>
              {t("Importar… (M4)")}
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
        <Bell size={16} /> {t("Alertas")} {active.length > 0 && <span className="tg-muted ts-card-sub">{tn(active.length, "{n} activa", "{n} activas")}</span>}
      </div>
      <div className="ts-alerts">
        {active.length === 0 && (
          <p className="tg-muted ts-snap-empty">
            <CheckCircle2 size={13} /> {t("No hay alertas activas.")}
          </p>
        )}
        {[...active, ...(showOld ? old : [])].map((a) => {
          const I = alertIcon[a.level];
          return (
            <div key={a.id} className="ts-alert" data-level={a.level} data-resolved={a.resolved || undefined}>
              <I size={17} />
              <div className="ts-alert-body">
                <strong>{tr(a.title)}</strong>
                <p>{tr(a.body)}</p>
              </div>
              <time dateTime={a.at}>{a.resolved ? t("Resuelta · {ago}", { ago: fmtAgo(a.at) }) : fmtAgo(a.at)}</time>
            </div>
          );
        })}
        {old.length > 0 && (
          <div className="ts-alerts-more">
            <Button variant="plain" onClick={() => setShowOld((x) => !x)}>
              {showOld ? t("Ocultar resueltas") : t("Ver resueltas ({n})", { n: old.length })}
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
        <HardDrive size={16} /> {t("Actividad de discos")} <span className="tg-muted ts-card-sub">{t("últimos 60 s")}</span>
      </div>
      <div className="ts-io-legend tg-num">
        <span className="ts-io-read">
          <ArrowDownToLine size={13} /> {t("Lectura {rate}", { rate: fmtRate(last?.diskRead ?? 0) })}
        </span>
        <span className="ts-io-write">
          <ArrowUpFromLine size={13} /> {t("Escritura {rate}", { rate: fmtRate(last?.diskWrite ?? 0) })}
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
          <h2>{t("Creá tu primer volumen")}</h2>
          <p>{t("Un volumen junta uno o más discos en un solo espacio protegido. Después creás carpetas compartidas adentro.")}</p>
          <Button variant="primary" size="lg" onClick={onCreate}>
            {t("Crear volumen…")}
          </Button>
          <small className="tg-muted">
            <CheckCircle2 size={12} /> {t("Ningún disco se borra sin que lo confirmes")}
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
