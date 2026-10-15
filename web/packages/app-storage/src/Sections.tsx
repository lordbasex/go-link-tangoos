import { useState } from "react";
import { DeleteVolume } from "./DeleteVolume";
import { CalendarClock, Camera, Fingerprint, HardDrive, Lightbulb, Lock, Maximize2, RefreshCw, ShieldCheck, Trash2, X } from "lucide-react";
import { RpcError, fmtAgo, fmtBytes, fmtDate, useLive, useTransport, fmtTemp } from "@tango/client";
import type { Disk, Snapshot, Volume } from "@tango/client";
import { Badge, Button, EmptyState, IconButton, Sheet, Status } from "@tango/ui";
import { toast, openApp } from "@tango/shell";
import { VolumeCard, stateInfo } from "./Overview";
import { profileHelp, profileName, smartLabel, smartTone, scrubAgo, useAvailable } from "./util";
import { ScheduleSheet, scheduleSummary } from "./Schedule";

// ---- Discos -----------------------------------------------------------------------

const useLabel = (d: Disk, vols: Volume[]) =>
  d.use === "system" ? "Disco del sistema" : d.use === "failed" ? "Falló" : d.foreign ? "Synology (ajeno)" : d.use === "member" ? vols.find((v) => v.id === d.volume)?.name ?? "En uso" : "Sin usar";

function smartRows(d: Disk) {
  const failed = d.use === "failed";
  return [
    { id: 1, name: "Tasa de errores de lectura", value: failed ? "1 284" : "0", ok: !failed },
    { id: 5, name: "Sectores reasignados", value: d.reallocated.toLocaleString("es-AR"), ok: d.reallocated === 0 },
    { id: 9, name: "Horas encendido", value: d.powerOnHours == null ? "—" : d.powerOnHours.toLocaleString("es-AR"), ok: true },
    { id: 12, name: "Ciclos de encendido", value: d.powerOnHours == null ? "—" : String(Math.round(d.powerOnHours / 410)), ok: true },
    { id: 187, name: "Errores no corregibles", value: failed ? "212" : "0", ok: !failed },
    { id: 194, name: "Temperatura", value: failed ? "—" : fmtTemp(d.tempC), ok: d.tempC == null || d.tempC < 50 },
    { id: 197, name: "Sectores pendientes", value: String(d.pending), ok: d.pending === 0 },
    { id: 199, name: "Errores CRC del cable", value: "0", ok: true },
  ];
}

function Inspector({ d, vols, onClose }: { d: Disk; vols: Volume[]; onClose: () => void }) {
  const t = useTransport();
  const canLocate = useAvailable("storage.disks.locate");
  const locate = () =>
    t.call("storage.disks.locate", { disk: d.id }).then(
      () => toast({ level: "ok", title: `Identificando la bahía ${d.bay ?? ""}`.trim(), body: "El LED de la bandeja parpadea durante 2 minutos para que la encuentres." }),
      (e) =>
        e instanceof RpcError && e.code === "NOT_SUPPORTED"
          ? toast({ level: "info", title: "Sin LED de identificación", body: "Este equipo no permite encender el LED de identificación. Guiate por el número de bahía y la serie." })
          : toast({ level: "warn", title: "No se pudo identificar el disco", body: (e as Error).message }),
    );
  return (
    <aside className="ts-inspector" aria-label={`Detalle de ${d.model}`}>
      <div className="ts-insp-head">
        <HardDrive size={28} strokeWidth={1.4} />
        <div>
          <strong>{d.model}</strong>
          <small>{d.bay ? `Bahía ${d.bay}` : "NVMe interno"}</small>
        </div>
        <IconButton icon={X} label="Cerrar detalle" onClick={onClose} />
      </div>
      <div className="ts-insp-actions">
        <Status tone={smartTone(d.smart)}>SMART: {smartLabel[d.smart]}</Status>
        <Button onClick={locate} disabled={!canLocate || d.use === "system"} title={canLocate ? "Hace parpadear el LED de la bandeja" : "Este TangoOS todavía no permite identificar discos"}>
          <Lightbulb size={13} /> Identificar
        </Button>
      </div>
      <div className="ts-insp-sec">
        <div className="ts-card-title">
          <Fingerprint size={14} /> Identidad
        </div>
        <dl className="ts-dl">
          <dt>WWN / ID</dt>
          <dd className="ts-mono">{d.id}</dd>
          <dt>Serie</dt>
          <dd className="ts-mono">{d.serial}</dd>
          <dt>Firmware</dt>
          <dd>{d.firmware}</dd>
          <dt>Capacidad</dt>
          <dd>{fmtBytes(d.size)}</dd>
          <dt>Tipo</dt>
          <dd>{d.kind === "nvme" ? "NVMe" : d.kind === "ssd" ? "SSD" : `HDD ${d.rpm ?? ""} rpm`}</dd>
          <dt>Uso</dt>
          <dd>{useLabel(d, vols)}</dd>
        </dl>
        <p className="ts-note">
          TangoOS identifica cada disco por WWN y número de serie. El nombre del kernel (hoy <code>/dev/{d.dev}</code>) puede cambiar entre reinicios y nunca se usa para operar.
        </p>
      </div>
      <div className="ts-insp-sec">
        <div className="ts-card-title">
          <ShieldCheck size={14} /> Atributos SMART
        </div>
        <table className="tg-table ts-smart">
          <tbody>
            {smartRows(d).map((r) => (
              <tr key={r.id}>
                <td className="tg-muted tg-num">{r.id}</td>
                <td>{r.name}</td>
                <td className="num" data-bad={!r.ok || undefined}>
                  {r.value}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </aside>
  );
}

export function Disks({ disks, vols, selected, onSelect }: { disks: Disk[]; vols: Volume[]; selected: string | null; onSelect: (id: string | null) => void }) {
  const sel = disks.find((d) => d.id === selected);
  const sorted = [...disks].sort((a, b) => (a.bay ?? 99) - (b.bay ?? 99));
  return (
    <div className="ts-disks">
      <div className="ts-disks-table">
        <table className="tg-table">
          <thead>
            <tr>
              <th>Bahía</th>
              <th>Modelo</th>
              <th>Serie</th>
              <th className="num">Capacidad</th>
              <th className="num">Temp.</th>
              <th className="num">Horas</th>
              <th>SMART</th>
              <th>Uso</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((d) => (
              <tr key={d.id} aria-selected={d.id === selected} onClick={() => onSelect(d.id === selected ? null : d.id)} className="ts-click">
                <td>{d.bay ?? (d.use === "system" ? "Sistema" : "Interno")}</td>
                <td>{d.model}</td>
                <td className="ts-mono">{d.serial}</td>
                <td className="num">{fmtBytes(d.size)}</td>
                <td className="num">{d.use === "failed" ? "—" : fmtTemp(d.tempC)}</td>
                <td className="num">{d.powerOnHours == null ? "—" : d.powerOnHours.toLocaleString("es-AR")}</td>
                <td>{d.id === selected ? smartLabel[d.smart] : <Status tone={smartTone(d.smart)}>{smartLabel[d.smart]}</Status>}</td>
                <td>{useLabel(d, vols)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {sel && <Inspector d={sel} vols={vols} onClose={() => onSelect(null)} />}
    </div>
  );
}

// ---- Volúmenes --------------------------------------------------------------------

export function Volumes({ disks, vols, onCreate, onReplace, onExpand }: { disks: Disk[]; vols: Volume[]; onCreate: () => void; onReplace: (v: Volume) => void; onExpand: (v: Volume) => void }) {
  const t = useTransport();
  const canReplace = useAvailable("storage.disks.replacePlan");
  const canExpand = useAvailable("storage.volumes.expandPlan");
  const [deleting, setDeleting] = useState<Volume | null>(null);
  if (!vols.length)
    return (
      <EmptyState icon={HardDrive} title="Todavía no hay volúmenes" action={<Button variant="primary" onClick={onCreate}>Crear volumen…</Button>}>
        Creá un volumen para empezar a guardar archivos.
      </EmptyState>
    );
  return (
    <div className="ts-stack">
      <DeleteVolume vol={deleting} disks={disks} onClose={() => setDeleting(null)} />
      {vols.map((v) => {
        const members = disks.filter((d) => v.disks.includes(d.id) || d.volume === v.id);
        const s = stateInfo(v);
        return (
          <div key={v.id} className="ts-stack">
            <VolumeCard v={v} disks={disks} />
            <section className="tg-card">
              <div className="ts-card-title">Cómo está armado</div>
              <p className="ts-note">
                {profileName[v.profile]}: {profileHelp[v.profile]} Estado: {s.text.toLowerCase()}.
              </p>
              <div className="ts-layers">
                <div className="ts-layer">
                  <small>Sistema de archivos</small>
                  <strong>Btrfs · {v.mount}</strong>
                </div>
                <div className="ts-layer">
                  <small>Array</small>
                  <strong>{v.profile === "basic" ? "Sin array: Btrfs directo en el disco" : `/dev/md/${v.name} · ${profileName[v.profile]} (mdadm)`}</strong>
                </div>
                <div className="ts-layer ts-layer-disks">
                  {members.map((d) => (
                    <div key={d.id} className="ts-member" data-failed={d.use === "failed" || undefined}>
                      <HardDrive size={14} />
                      <span>
                        {d.bay ? `Bahía ${d.bay}` : "Disco interno"} · {d.model} · {d.serial}
                      </span>
                      <Status tone={d.use === "failed" ? "crit" : smartTone(d.smart)}>{d.use === "failed" ? "Falló" : smartLabel[d.smart]}</Status>
                    </div>
                  ))}
                </div>
              </div>
              <div className="ts-row-actions">
                <Button
                  disabled={v.state !== "healthy"}
                  onClick={() => t.call("storage.scrub.start", { volume: v.id }).catch((e: Error) => toast({ level: "warn", title: "No se pudo verificar", body: e.message }))}
                >
                  <ShieldCheck size={14} /> Verificar
                </Button>
                {(v.state === "degraded" || members.some((d) => d.use === "failed")) && !v.task && v.profile !== "basic" && (
                  <Button variant="primary" disabled={!canReplace} title={canReplace ? undefined : "Este TangoOS todavía no permite reemplazar discos"} onClick={() => onReplace(v)}>
                    <RefreshCw size={14} /> Reemplazar disco…
                  </Button>
                )}
                <Button
                  disabled={!canExpand || !["raid5", "raid6", "raid10"].includes(v.profile) || v.state !== "healthy" || !!v.task}
                  title={
                    !canExpand
                      ? "Este TangoOS todavía no permite ampliar volúmenes"
                      : !["raid5", "raid6", "raid10"].includes(v.profile)
                        ? `Un ${profileName[v.profile]} no se amplía sumando discos`
                        : v.state !== "healthy" || v.task
                          ? "Esperá a que el volumen esté en orden y sin tareas"
                          : "Sumar discos libres al array"
                  }
                  onClick={() => onExpand(v)}
                >
                  <Maximize2 size={14} /> Ampliar volumen…
                </Button>
                <Button variant="danger" disabled={v.state === "building" || v.state === "scrubbing" || !!v.task} title={v.state === "building" || v.state === "scrubbing" || v.task ? "Esperá a que termine la tarea en curso" : undefined} onClick={() => setDeleting(v)}>
                  <Trash2 size={14} /> Eliminar volumen…
                </Button>
                <span className="tg-muted ts-push">Última verificación {scrubAgo(v)}</span>
              </div>
            </section>
          </div>
        );
      })}
    </div>
  );
}

// ---- Instantáneas -------------------------------------------------------------------

function ShareSnapshots({ share, snaps, onSchedule }: { share: { id: string; name: string; color: string; snapshots: boolean }; snaps: Snapshot[]; onSchedule: () => void }) {
  const t = useTransport();
  const canSchedule = useAvailable("snapshots.schedule.get");
  const canDelete = useAvailable("snapshots.delete");
  const sched = useLive("snapshots.schedule.get", { share: share.id }, ["snapshots.changed"]);
  const [expanded, setExpanded] = useState(false);
  const [deleting, setDeleting] = useState<Snapshot | null>(null);
  const [busy, setBusy] = useState(false);
  const create = () =>
    t.call("snapshots.create", { share: share.id }).then(
      () => toast({ level: "info", title: "Creando instantánea", body: "Es instantánea y no ocupa espacio hasta que los archivos cambian." }),
      (e: Error) => toast({ level: "warn", title: "No se pudo crear", body: e.message }),
    );
  const remove = async () => {
    if (!deleting) return;
    setBusy(true);
    try {
      await t.call("snapshots.delete", { id: deleting.id });
      toast({ level: "ok", title: "Instantánea eliminada", body: `${share.name} · ${fmtDate(deleting.at)}` });
      setDeleting(null);
    } catch (e) {
      toast({ level: "warn", title: "No se pudo eliminar", body: e instanceof RpcError && e.code === "SNAPSHOT_LOCKED" ? "La instantánea está protegida." : (e as Error).message });
    } finally {
      setBusy(false);
    }
  };
  const summary = canSchedule && sched.data ? scheduleSummary(sched.data) : null;
  return (
    <section className="tg-card ts-snapshare">
      <Sheet
        open={!!deleting}
        onClose={() => setDeleting(null)}
        width={440}
        footer={
          <>
            <Button onClick={() => setDeleting(null)}>Cancelar</Button>
            <Button variant="danger" disabled={busy} onClick={remove}>
              Eliminar instantánea
            </Button>
          </>
        }
      >
        <h2 className="tg-h2">¿Eliminar esta instantánea?</h2>
        <p className="tg-muted ts-wiz-lead">
          {share.name} · {deleting ? `${fmtDate(deleting.at)} (${fmtAgo(deleting.at)})` : ""}. Las versiones de archivos que solo existen en ella dejan de poder restaurarse. Los archivos actuales no se tocan.
        </p>
      </Sheet>
      <div className="ts-snap-head">
        <span className="ts-dot" style={{ background: share.color }} />
        <div>
          <strong>{share.name}</strong>
          {summary && <div className="ts-sched-summary">{summary}</div>}
        </div>
        {share.snapshots ? <Badge>{snaps.length} instantáneas</Badge> : <Badge tone="warn">Desactivadas</Badge>}
        <span className="ts-push" />
        {snaps.length > 3 && (
          <Button variant="plain" onClick={() => setExpanded((x) => !x)}>
            {expanded ? "Ocultar" : "Ver todas"}
          </Button>
        )}
        <Button onClick={onSchedule} disabled={!canSchedule || !share.snapshots} title={canSchedule ? "Programar instantáneas automáticas" : "Este TangoOS todavía no programa instantáneas"}>
          <CalendarClock size={14} /> Programar…
        </Button>
        <Button onClick={create} disabled={!share.snapshots}>
          <Camera size={14} /> Crear instantánea
        </Button>
      </div>
      {snaps.length === 0 && <p className="tg-muted ts-snap-empty">Sin instantáneas todavía.</p>}
      {snaps.length > 0 && (
        <table className="tg-table">
          <tbody>
            {(expanded ? snaps : snaps.slice(0, 3)).map((s) => (
              <tr key={s.id}>
                <td>{fmtDate(s.at)}</td>
                <td className="tg-muted">{fmtAgo(s.at)}</td>
                <td>{s.kind === "manual" ? "Manual" : "Programada"}</td>
                <td className="num">{s.size ? fmtBytes(s.size) : "—"}</td>
                <td style={{ width: 120 }}>
                  {s.locked ? (
                    <span className="ts-locked">
                      <Lock size={12} /> Protegida
                    </span>
                  ) : (
                    canDelete && <IconButton icon={Trash2} label={`Eliminar la instantánea del ${fmtDate(s.at)}`} onClick={() => setDeleting(s)} />
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

export function Snapshots() {
  const shares = useLive("shares.list", undefined, ["shares.changed"]).data ?? [];
  const snaps = useLive("snapshots.list", {}, ["snapshots.changed", "shares.changed"]).data ?? [];
  const [editing, setEditing] = useState<{ id: string; name: string } | null>(null);
  if (!shares.length)
    return (
      <EmptyState icon={Camera} title="Sin carpetas compartidas" action={<Button onClick={() => openApp("tango.settings", { section: "shares" })}>Crear carpeta compartida</Button>}>
        Las instantáneas se toman por carpeta compartida. Creá una carpeta en Ajustes para empezar.
      </EmptyState>
    );
  return (
    <div className="ts-stack">
      <ScheduleSheet share={editing} onClose={() => setEditing(null)} />
      <section className="tg-card ts-policy">
        <Camera size={18} />
        <div>
          <strong>Instantáneas: puntos de restauración por carpeta</strong>
          <small className="tg-muted">Son locales, no un respaldo: viven en los mismos discos. Programalas por carpeta y TangoOS borra las viejas según cuántas conservar.</small>
        </div>
      </section>
      {shares.map((sh) => (
        <ShareSnapshots key={sh.id} share={sh} snaps={snaps.filter((s) => s.share === sh.id)} onSchedule={() => setEditing({ id: sh.id, name: sh.name })} />
      ))}
    </div>
  );
}
