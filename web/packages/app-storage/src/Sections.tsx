import { useState } from "react";
import { DeleteVolume } from "./DeleteVolume";
import { CalendarClock, Camera, Fingerprint, HardDrive, KeyRound, Lightbulb, Lock, Maximize2, RefreshCw, ShieldCheck, Trash2, Unlock, Usb, X, Zap } from "lucide-react";
import { RpcError, fmtAgo, fmtBytes, fmtDate, locale, t, tn, useLive, useTransport, fmtTemp } from "@tango/client";
import type { Disk, Pool, Snapshot, Volume } from "@tango/client";
import { Badge, Button, EmptyState, IconButton, Sheet, Status } from "@tango/ui";
import { toast, openApp } from "@tango/shell";
import { VolumeCard, stateInfo } from "./Overview";
import { diskUse, fsName, profileHelp, profileName, smartLabel, smartTone, scrubAgo, useAvailable } from "./util";
import { CacheSheet, CacheStats, KeyDeviceSheet, LockedCard, PassphraseSheet, UnlockSheet } from "./Security";
import { ResizeVolume } from "./PoolVolume";
import { ScheduleSheet, scheduleSummary } from "./Schedule";
import { ZfsVolumeLayers } from "./Zfs";

// ---- Discos -----------------------------------------------------------------------

const useLabel = (d: Disk, vols: Volume[], pools: Pool[] = []) => diskUse(d, vols, pools);

function smartRows(d: Disk) {
  const failed = d.use === "failed";
  return [
    { id: 1, name: t("Tasa de errores de lectura"), value: failed ? "1 284" : "0", ok: !failed },
    { id: 5, name: t("Sectores reasignados"), value: d.reallocated.toLocaleString(locale()), ok: d.reallocated === 0 },
    { id: 9, name: t("Horas encendido"), value: d.powerOnHours == null ? "—" : d.powerOnHours.toLocaleString(locale()), ok: true },
    { id: 12, name: t("Ciclos de encendido"), value: d.powerOnHours == null ? "—" : String(Math.round(d.powerOnHours / 410)), ok: true },
    { id: 187, name: t("Errores no corregibles"), value: failed ? "212" : "0", ok: !failed },
    { id: 194, name: t("Temperatura"), value: failed ? "—" : fmtTemp(d.tempC), ok: d.tempC == null || d.tempC < 50 },
    { id: 197, name: t("Sectores pendientes"), value: String(d.pending), ok: d.pending === 0 },
    { id: 199, name: t("Errores CRC del cable"), value: "0", ok: true },
  ];
}

function Inspector({ d, vols, pools, onClose }: { d: Disk; vols: Volume[]; pools: Pool[]; onClose: () => void }) {
  const rpc = useTransport();
  const canLocate = useAvailable("storage.disks.locate");
  const locate = () =>
    rpc.call("storage.disks.locate", { disk: d.id }).then(
      () => toast({ level: "ok", title: (d.bay ? t("Identificando la bahía {bay}", { bay: d.bay }) : t("Identificando la bahía")).trim(), body: t("El LED de la bandeja parpadea durante 2 minutos para que la encuentres.") }),
      (e) =>
        e instanceof RpcError && e.code === "NOT_SUPPORTED"
          ? toast({ level: "info", title: t("Sin LED de identificación"), body: t("Este equipo no permite encender el LED de identificación. Guiate por el número de bahía y la serie.") })
          : toast({ level: "warn", title: t("No se pudo identificar el disco"), body: (e as Error).message }),
    );
  return (
    <aside className="ts-inspector" aria-label={t("Detalle de {name}", { name: d.model })}>
      <div className="ts-insp-head">
        <HardDrive size={28} strokeWidth={1.4} />
        <div>
          <strong>{d.model}</strong>
          <small>{d.bay ? t("Bahía {n}", { n: d.bay }) : t("NVMe interno")}</small>
        </div>
        <IconButton icon={X} label={t("Cerrar detalle")} onClick={onClose} />
      </div>
      <div className="ts-insp-actions">
        <Status tone={smartTone(d.smart)}>SMART: {smartLabel[d.smart]}</Status>
        <Button onClick={locate} disabled={!canLocate || d.use === "system"} title={canLocate ? t("Hace parpadear el LED de la bandeja") : t("Este TangoOS todavía no permite identificar discos")}>
          <Lightbulb size={13} /> {t("Identificar")}
        </Button>
      </div>
      <div className="ts-insp-sec">
        <div className="ts-card-title">
          <Fingerprint size={14} /> {t("Identidad")}
        </div>
        <dl className="ts-dl">
          <dt>WWN / ID</dt>
          <dd className="ts-mono">{d.id}</dd>
          <dt>{t("Serie")}</dt>
          <dd className="ts-mono">{d.serial}</dd>
          <dt>Firmware</dt>
          <dd>{d.firmware}</dd>
          <dt>{t("Capacidad")}</dt>
          <dd>{fmtBytes(d.size)}</dd>
          <dt>{t("Tipo")}</dt>
          <dd>{d.kind === "nvme" ? "NVMe" : d.kind === "ssd" ? "SSD" : `HDD ${d.rpm ?? ""} rpm`}</dd>
          <dt>{t("Uso")}</dt>
          <dd>{useLabel(d, vols, pools)}</dd>
        </dl>
        <p className="ts-note">
          {t("TangoOS identifica cada disco por WWN y número de serie. El nombre del kernel (hoy")} <code>/dev/{d.dev}</code>
          {t(") puede cambiar entre reinicios y nunca se usa para operar.")}
        </p>
      </div>
      <div className="ts-insp-sec">
        <div className="ts-card-title">
          <ShieldCheck size={14} /> {t("Atributos SMART")}
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

export function Disks({ disks, vols, pools = [], selected, onSelect }: { disks: Disk[]; vols: Volume[]; pools?: Pool[]; selected: string | null; onSelect: (id: string | null) => void }) {
  const sel = disks.find((d) => d.id === selected);
  const sorted = [...disks].sort((a, b) => (a.bay ?? 99) - (b.bay ?? 99));
  return (
    <div className="ts-disks">
      <div className="ts-disks-table">
        <table className="tg-table">
          <thead>
            <tr>
              <th>{t("Bahía")}</th>
              <th>{t("Modelo")}</th>
              <th>{t("Serie")}</th>
              <th className="num">{t("Capacidad")}</th>
              <th className="num">{t("Temp.")}</th>
              <th className="num">{t("Horas")}</th>
              <th>SMART</th>
              <th>{t("Uso")}</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((d) => (
              <tr key={d.id} aria-selected={d.id === selected} onClick={() => onSelect(d.id === selected ? null : d.id)} className="ts-click">
                <td>{d.bay ?? (d.use === "system" ? t("Sistema") : t("Interno"))}</td>
                <td>{d.model}</td>
                <td className="ts-mono">{d.serial}</td>
                <td className="num">{fmtBytes(d.size)}</td>
                <td className="num">{d.use === "failed" ? "—" : fmtTemp(d.tempC)}</td>
                <td className="num">{d.powerOnHours == null ? "—" : d.powerOnHours.toLocaleString(locale())}</td>
                <td>{d.id === selected ? smartLabel[d.smart] : <Status tone={smartTone(d.smart)}>{smartLabel[d.smart]}</Status>}</td>
                <td>{useLabel(d, vols, pools)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {sel && <Inspector d={sel} vols={vols} pools={pools} onClose={() => onSelect(null)} />}
    </div>
  );
}

// ---- Volúmenes --------------------------------------------------------------------

export function Volumes({ disks, vols, pools = [], onCreate, onReplace, onExpand }: { disks: Disk[]; vols: Volume[]; pools?: Pool[]; onCreate: () => void; onReplace: (v: Volume) => void; onExpand: (v: Volume) => void }) {
  const rpc = useTransport();
  const canReplace = useAvailable("storage.disks.replacePlan");
  const canExpand = useAvailable("storage.volumes.expandPlan");
  const [deleting, setDeleting] = useState<Volume | null>(null);
  const [sheet, setSheet] = useState<{ kind: "resize" | "unlock" | "pass" | "key" | "cache"; v: Volume } | null>(null);
  const of = (k: NonNullable<typeof sheet>["kind"]) => (sheet?.kind === k ? sheet.v : null);
  if (!vols.length)
    return (
      <EmptyState icon={HardDrive} title={t("Todavía no hay volúmenes")} action={<Button variant="primary" onClick={onCreate}>{t("Crear volumen…")}</Button>}>
        {t("Creá un volumen para empezar a guardar archivos.")}
      </EmptyState>
    );
  return (
    <div className="ts-stack">
      <DeleteVolume vol={deleting} disks={disks} onClose={() => setDeleting(null)} />
      <ResizeVolume vol={of("resize")} pools={pools} onClose={() => setSheet(null)} />
      <UnlockSheet vol={of("unlock")} onClose={() => setSheet(null)} />
      <PassphraseSheet vol={of("pass")} onClose={() => setSheet(null)} />
      <KeyDeviceSheet vol={of("key")} onClose={() => setSheet(null)} />
      <CacheSheet vol={of("cache")} disks={disks} vols={vols} pools={pools} onClose={() => setSheet(null)} />
      {vols.map((v) => {
        if (v.kind === "zfs") return <ZfsVolumeDetail key={v.id} v={v} disks={disks} onSheet={(kind) => setSheet({ kind, v })} />;
        if (v.kind === "pool")
          return <PoolVolumeDetail key={v.id} v={v} disks={disks} pools={pools} onSheet={(kind) => setSheet({ kind, v })} onDelete={() => setDeleting(v)} />;
        const members = disks.filter((d) => v.disks.includes(d.id) || d.volume === v.id);
        const s = stateInfo(v);
        return (
          <div key={v.id} className="ts-stack">
            <VolumeCard v={v} disks={disks} />
            <section className="tg-card">
              <div className="ts-card-title">{t("Cómo está armado")}</div>
              <p className="ts-note">
                {profileName[v.profile]}: {profileHelp[v.profile]} {t("Estado: {state}.", { state: s.text.toLowerCase() })}
              </p>
              <div className="ts-layers">
                <div className="ts-layer">
                  <small>{t("Sistema de archivos")}</small>
                  <strong>Btrfs · {v.mount}</strong>
                </div>
                <div className="ts-layer">
                  <small>Array</small>
                  <strong>{v.profile === "basic" ? t("Sin array: Btrfs directo en el disco") : `/dev/md/${v.name} · ${profileName[v.profile]} (mdadm)`}</strong>
                </div>
                <div className="ts-layer ts-layer-disks">
                  {members.map((d) => (
                    <div key={d.id} className="ts-member" data-failed={d.use === "failed" || undefined}>
                      <HardDrive size={14} />
                      <span>
                        {d.bay ? t("Bahía {n}", { n: d.bay }) : t("Disco interno")} · {d.model} · {d.serial}
                      </span>
                      <Status tone={d.use === "failed" ? "crit" : smartTone(d.smart)}>{d.use === "failed" ? t("Falló") : smartLabel[d.smart]}</Status>
                    </div>
                  ))}
                </div>
              </div>
              <div className="ts-row-actions">
                <Button
                  disabled={v.state !== "healthy"}
                  onClick={() => rpc.call("storage.scrub.start", { volume: v.id }).catch((e: Error) => toast({ level: "warn", title: t("No se pudo verificar"), body: e.message }))}
                >
                  <ShieldCheck size={14} /> {t("Verificar")}
                </Button>
                {(v.state === "degraded" || members.some((d) => d.use === "failed")) && !v.task && v.profile !== "basic" && (
                  <Button variant="primary" disabled={!canReplace} title={canReplace ? undefined : t("Este TangoOS todavía no permite reemplazar discos")} onClick={() => onReplace(v)}>
                    <RefreshCw size={14} /> {t("Reemplazar disco…")}
                  </Button>
                )}
                <Button
                  disabled={!canExpand || !["raid5", "raid6", "raid10"].includes(v.profile) || v.state !== "healthy" || !!v.task}
                  title={
                    !canExpand
                      ? t("Este TangoOS todavía no permite ampliar volúmenes")
                      : !["raid5", "raid6", "raid10"].includes(v.profile)
                        ? t("Un {profile} no se amplía sumando discos", { profile: profileName[v.profile] })
                        : v.state !== "healthy" || v.task
                          ? t("Esperá a que el volumen esté en orden y sin tareas")
                          : t("Sumar discos libres al array")
                  }
                  onClick={() => onExpand(v)}
                >
                  <Maximize2 size={14} /> {t("Ampliar volumen…")}
                </Button>
                <Button variant="danger" disabled={v.state === "building" || v.state === "scrubbing" || !!v.task} title={v.state === "building" || v.state === "scrubbing" || v.task ? t("Esperá a que termine la tarea en curso") : undefined} onClick={() => setDeleting(v)}>
                  <Trash2 size={14} /> {t("Eliminar volumen…")}
                </Button>
                <span className="tg-muted ts-push">{t("Última verificación {ago}", { ago: scrubAgo(v) })}</span>
              </div>
            </section>
          </div>
        );
      })}
    </div>
  );
}

// ---- Volumen ZFS (§18) ---------------------------------------------------------------------

function ZfsVolumeDetail({ v, disks, onSheet }: { v: Volume; disks: Disk[]; onSheet: (k: "unlock" | "pass" | "key") => void }) {
  const rpc = useTransport();
  const lock = () =>
    rpc.call("storage.volumes.lock", { volume: v.id }).then(
      () => toast({ level: "ok", title: t("{name} bloqueado", { name: v.name }), body: t("Se desmontó y se descargó la clave: hace falta la frase para volver a abrirlo.") }),
      (e: Error) => toast({ level: "warn", title: t("No se pudo bloquear"), body: e.message }),
    );
  return (
    <div className="ts-stack">
      {v.locked ? <LockedCard v={v} onUnlock={() => onSheet("unlock")} /> : <VolumeCard v={v} disks={disks} />}
      <section className="tg-card">
        <div className="ts-card-title">{t("Cómo está armado")}</div>
        <ZfsVolumeLayers v={v} />
        <div className="ts-row-actions">
          <Button disabled={v.locked || v.state !== "healthy"} onClick={() => rpc.call("storage.scrub.start", { volume: v.id }).catch((e: Error) => toast({ level: "warn", title: t("No se pudo verificar"), body: e.message }))}>
            <ShieldCheck size={14} /> {t("Verificar el pool")}
          </Button>
          {v.encrypted &&
            (v.locked ? (
              <Button variant="primary" onClick={() => onSheet("unlock")}>
                <Unlock size={14} /> {t("Desbloquear…")}
              </Button>
            ) : (
              <Button onClick={lock}>
                <Lock size={14} /> {t("Bloquear")}
              </Button>
            ))}
          {v.encrypted && (
            <Button onClick={() => onSheet("pass")}>
              <KeyRound size={14} /> {t("Cambiar frase…")}
            </Button>
          )}
          {v.encrypted && (
            <Button onClick={() => onSheet("key")}>
              <Usb size={14} /> {v.keyDevice ? t("Llave USB…") : t("Agregar llave USB…")}
            </Button>
          )}
          <Button onClick={() => openApp("tango.storage", { section: "zfs" })}>{t("Ver el pool…")}</Button>
          <span className="tg-muted ts-push">{t("Última verificación {ago}", { ago: scrubAgo(v) })}</span>
        </div>
      </section>
    </div>
  );
}

// ---- Volumen de un grupo (§16) ---------------------------------------------------------

function PoolVolumeDetail({ v, disks, pools, onSheet, onDelete }: { v: Volume; disks: Disk[]; pools: Pool[]; onSheet: (k: "resize" | "unlock" | "pass" | "key" | "cache") => void; onDelete: () => void }) {
  const rpc = useTransport();
  const p = pools.find((x) => x.id === v.pool);
  const s = stateInfo(v);
  const busy = v.state === "building" || v.state === "scrubbing" || !!v.task;
  const lock = () =>
    rpc.call("storage.volumes.lock", { volume: v.id }).then(
      () => toast({ level: "ok", title: t("{name} bloqueado", { name: v.name }), body: t("Se desmontó y se cerró: hace falta la frase (o la llave) para volver a abrirlo.") }),
      (e: Error) => toast({ level: "warn", title: t("No se pudo bloquear"), body: e.message }),
    );
  const removeCache = () =>
    rpc.call("storage.cache.remove", { volume: v.id }).then(
      () => toast({ level: "info", title: t("Quitando la caché de {name}", { name: v.name }), body: t("Los bloques pendientes bajan a los discos y las SSD quedan libres.") }),
      (e: Error) => toast({ level: "warn", title: t("No se pudo quitar la caché"), body: e.message }),
    );
  return (
    <div className="ts-stack">
      {v.locked ? <LockedCard v={v} onUnlock={() => onSheet("unlock")} /> : <VolumeCard v={v} disks={disks} />}
      <section className="tg-card">
        <div className="ts-card-title">{t("Cómo está armado")}</div>
        <p className="ts-note">
          {v.encrypted
            ? t("Volumen {fs} cifrado en el grupo {pool} ({profile}).", { fs: fsName[v.fs] ?? v.fs, pool: p?.name ?? "—", profile: profileName[v.profile] })
            : t("Volumen {fs} en el grupo {pool} ({profile}).", { fs: fsName[v.fs] ?? v.fs, pool: p?.name ?? "—", profile: profileName[v.profile] })}{" "}
          {t("Estado: {state}.", { state: s.text.toLowerCase() })}
        </p>
        <div className="ts-layers">
          <div className="ts-layer">
            <small>{t("Sistema de archivos")}</small>
            <strong>
              {fsName[v.fs] ?? v.fs} · {v.mount || t("/{name} (sin montar)", { name: v.name })}
            </strong>
          </div>
          {v.encrypted && (
            <div className="ts-layer">
              <small>{t("Cifrado")}</small>
              <strong>
                LUKS2 (argon2id) · {v.locked ? t("cerrado") : t("abierto")}
                {v.keyDevice ? ` · ${t("llave USB {name}", { name: v.keyDevice })}` : ""}
              </strong>
            </div>
          )}
          {v.cache && (
            <div className="ts-layer">
              <small>{t("Caché SSD")}</small>
              <strong>{v.cache.mode === "read" ? t("Solo lectura (writethrough)") : t("Lectura y escritura (writeback, SSD en RAID 1)")}</strong>
            </div>
          )}
          <div className="ts-layer">
            <small>{t("Volumen lógico")}</small>
            <strong>
              /dev/{p?.name ?? "?"}/{v.name} · {fmtBytes(v.size)}
            </strong>
          </div>
          <div className="ts-layer">
            <small>{t("Grupo", undefined, "almacenamiento")}</small>
            <strong>
              {p?.name ?? "—"} · {profileName[v.profile]} · {t("{free} libres", { free: fmtBytes(p?.free ?? 0) })}
            </strong>
          </div>
        </div>
        {v.cache && <CacheStats v={v} />}
        <div className="ts-row-actions">
          {v.fs === "btrfs" && !v.locked && (
            <Button disabled={v.state !== "healthy"} onClick={() => rpc.call("storage.scrub.start", { volume: v.id }).catch((e: Error) => toast({ level: "warn", title: t("No se pudo verificar"), body: e.message }))}>
              <ShieldCheck size={14} /> {t("Verificar")}
            </Button>
          )}
          <Button disabled={v.locked || busy || (p?.free ?? 0) < 8 * 1024 ** 2} title={v.locked ? t("Desbloquealo primero") : undefined} onClick={() => onSheet("resize")}>
            <Maximize2 size={14} /> {t("Agrandar…")}
          </Button>
          {v.encrypted &&
            (v.locked ? (
              <Button variant="primary" onClick={() => onSheet("unlock")}>
                <Unlock size={14} /> {t("Desbloquear…")}
              </Button>
            ) : (
              <Button onClick={lock}>
                <Lock size={14} /> {t("Bloquear")}
              </Button>
            ))}
          {v.encrypted && (
            <Button onClick={() => onSheet("pass")}>
              <KeyRound size={14} /> {t("Cambiar frase…")}
            </Button>
          )}
          {v.encrypted && (
            <Button onClick={() => onSheet("key")}>
              <Usb size={14} /> {t("Llave USB…")}
            </Button>
          )}
          {v.cache ? (
            <Button disabled={busy} onClick={removeCache}>
              <Zap size={14} /> {t("Quitar caché")}
            </Button>
          ) : (
            <Button disabled={busy} onClick={() => onSheet("cache")}>
              <Zap size={14} /> {t("Caché SSD…")}
            </Button>
          )}
          <Button variant="danger" disabled={busy} onClick={onDelete}>
            <Trash2 size={14} /> {t("Eliminar volumen…")}
          </Button>
          {!v.locked && v.fs === "btrfs" && <span className="tg-muted ts-push">{t("Última verificación {ago}", { ago: scrubAgo(v) })}</span>}
        </div>
      </section>
    </div>
  );
}

// ---- Instantáneas -------------------------------------------------------------------

function ShareSnapshots({ share, snaps, onSchedule }: { share: { id: string; name: string; color: string; snapshots: boolean }; snaps: Snapshot[]; onSchedule: () => void }) {
  const rpc = useTransport();
  const canSchedule = useAvailable("snapshots.schedule.get");
  const canDelete = useAvailable("snapshots.delete");
  const sched = useLive("snapshots.schedule.get", { share: share.id }, ["snapshots.changed"]);
  const [expanded, setExpanded] = useState(false);
  const [deleting, setDeleting] = useState<Snapshot | null>(null);
  const [busy, setBusy] = useState(false);
  const create = () =>
    rpc.call("snapshots.create", { share: share.id }).then(
      () => toast({ level: "info", title: t("Creando instantánea"), body: t("Es instantánea y no ocupa espacio hasta que los archivos cambian.") }),
      (e: Error) => toast({ level: "warn", title: t("No se pudo crear"), body: e.message }),
    );
  const remove = async () => {
    if (!deleting) return;
    setBusy(true);
    try {
      await rpc.call("snapshots.delete", { id: deleting.id });
      toast({ level: "ok", title: t("Instantánea eliminada"), body: `${share.name} · ${fmtDate(deleting.at)}` });
      setDeleting(null);
    } catch (e) {
      toast({ level: "warn", title: t("No se pudo eliminar"), body: e instanceof RpcError && e.code === "SNAPSHOT_LOCKED" ? t("La instantánea está protegida.") : (e as Error).message });
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
            <Button onClick={() => setDeleting(null)}>{t("Cancelar")}</Button>
            <Button variant="danger" disabled={busy} onClick={remove}>
              {t("Eliminar instantánea")}
            </Button>
          </>
        }
      >
        <h2 className="tg-h2">{t("¿Eliminar esta instantánea?")}</h2>
        <p className="tg-muted ts-wiz-lead">
          {share.name} · {deleting ? `${fmtDate(deleting.at)} (${fmtAgo(deleting.at)})` : ""}. {t("Las versiones de archivos que solo existen en ella dejan de poder restaurarse. Los archivos actuales no se tocan.")}
        </p>
      </Sheet>
      <div className="ts-snap-head">
        <span className="ts-dot" style={{ background: share.color }} />
        <div>
          <strong>{share.name}</strong>
          {summary && <div className="ts-sched-summary">{summary}</div>}
        </div>
        {share.snapshots ? <Badge>{tn(snaps.length, "{n} instantánea", "{n} instantáneas")}</Badge> : <Badge tone="warn">{t("Desactivadas")}</Badge>}
        <span className="ts-push" />
        {snaps.length > 3 && (
          <Button variant="plain" onClick={() => setExpanded((x) => !x)}>
            {expanded ? t("Ocultar") : t("Ver todas")}
          </Button>
        )}
        <Button onClick={onSchedule} disabled={!canSchedule || !share.snapshots} title={canSchedule ? t("Programar instantáneas automáticas") : t("Este TangoOS todavía no programa instantáneas")}>
          <CalendarClock size={14} /> {t("Programar…")}
        </Button>
        <Button onClick={create} disabled={!share.snapshots}>
          <Camera size={14} /> {t("Crear instantánea")}
        </Button>
      </div>
      {snaps.length === 0 && <p className="tg-muted ts-snap-empty">{t("Sin instantáneas todavía.")}</p>}
      {snaps.length > 0 && (
        <table className="tg-table">
          <tbody>
            {(expanded ? snaps : snaps.slice(0, 3)).map((s) => (
              <tr key={s.id}>
                <td>{fmtDate(s.at)}</td>
                <td className="tg-muted">{fmtAgo(s.at)}</td>
                <td>{s.kind === "manual" ? t("Manual") : t("Programada")}</td>
                <td className="num">{s.size ? fmtBytes(s.size) : "—"}</td>
                <td style={{ width: 120 }}>
                  {s.locked ? (
                    <span className="ts-locked">
                      <Lock size={12} /> {t("Protegida")}
                    </span>
                  ) : (
                    canDelete && <IconButton icon={Trash2} label={t("Eliminar la instantánea del {date}", { date: fmtDate(s.at) })} onClick={() => setDeleting(s)} />
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
      <EmptyState icon={Camera} title={t("Sin carpetas compartidas")} action={<Button onClick={() => openApp("tango.settings", { section: "shares" })}>{t("Crear carpeta compartida")}</Button>}>
        {t("Las instantáneas se toman por carpeta compartida. Creá una carpeta en Ajustes para empezar.")}
      </EmptyState>
    );
  return (
    <div className="ts-stack">
      <ScheduleSheet share={editing} onClose={() => setEditing(null)} />
      <section className="tg-card ts-policy">
        <Camera size={18} />
        <div>
          <strong>{t("Instantáneas: puntos de restauración por carpeta")}</strong>
          <small className="tg-muted">{t("Son locales, no un respaldo: viven en los mismos discos. Programalas por carpeta y TangoOS borra las viejas según cuántas conservar.")}</small>
        </div>
      </section>
      {shares.map((sh) => (
        <ShareSnapshots key={sh.id} share={sh} snaps={snaps.filter((s) => s.share === sh.id)} onSchedule={() => setEditing({ id: sh.id, name: sh.name })} />
      ))}
    </div>
  );
}
