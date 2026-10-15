// Pools ZFS (protocol/README.md §18, ADR 0013): habilitar OpenZFS, crear e importar pools,
// vdevs con su estado, caché L2ARC y registro SLOG, scrub programado, volúmenes (datasets)
// con cifrado nativo y volúmenes de bloque (zvol) para iSCSI.
import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, Boxes, Check, CheckCircle2, Clock, Cpu, Database, Download, HardDrive, KeyRound, Lock, Plus, RefreshCw, RotateCcw, ShieldCheck, Trash2, Upload, XOctagon, Zap } from "lucide-react";
import { RpcError, ZFS_LAYOUTS, fmtAgo, fmtBytes, useJobs, useLive, useTransport, zfsCheck, zfsLayoutLabel, zfsSplit, zfsTolerance, zfsUsable } from "@tango/client";
import type { DeletePlan, Disk, Volume, ZfsImportable, ZfsLayout, ZfsLeaf, ZfsPlan, ZfsPool, ZfsVdev } from "@tango/client";
import { Badge, Button, EmptyState, Progress, Sheet, Status, TextInput, Toggle } from "@tango/ui";
import { toast } from "@tango/shell";
import { useCountdown } from "./CreateVolume";
import { ineligibleReason, volumeColor } from "./util";

const diskLabel = (d: Disk) => `${d.bay ? `Bahía ${d.bay}` : "Interno"} · ${d.model}`;
const errText = (e: unknown) => (e instanceof RpcError ? e.message : (e as Error).message);

export function zfsPoolState(p: ZfsPool): { tone: "ok" | "warn" | "crit" | "info"; text: string } {
  if (p.scan?.state === "scanning") return p.scan.function === "resilver" ? { tone: "warn", text: "Reconstruyendo (resilver)" } : { tone: "info", text: "Verificando" };
  switch (p.state) {
    case "degraded":
      return { tone: "crit", text: "Sin redundancia" };
    case "faulted":
      return { tone: "crit", text: `No disponible (${p.health})` };
    case "readonly":
      return { tone: "warn", text: "Solo lectura" };
  }
  const cksum = p.vdevs.reduce((a, v) => a + v.cksum + v.disks.reduce((b, l) => b + l.cksum, 0), 0);
  return cksum > 0 ? { tone: "warn", text: `${cksum} errores de checksum` } : { tone: "ok", text: "En orden" };
}

const leafTone = (l: ZfsLeaf): "ok" | "warn" | "crit" => (l.state === "ONLINE" ? (l.read + l.write + l.cksum > 0 ? "warn" : "ok") : l.state === "DEGRADED" ? "warn" : "crit");

// ---- Sección ----------------------------------------------------------------------------

export function ZfsSection({ disks, vols }: { disks: Disk[]; vols: Volume[] }) {
  const t = useTransport();
  const jobs = useJobs();
  const st = useLive("storage.zfs.status", undefined, ["storage.changed"]).data;
  const [creating, setCreating] = useState(false);
  const [importable, setImportable] = useState<ZfsImportable[] | null>(null);
  const [searching, setSearching] = useState(false);
  const enablingJob = jobs.find((j) => j.id === st?.enabling);

  if (!st) return <p className="tg-muted">Cargando…</p>;
  if (!st.enabled) return <EnableZfs enabling={st.enabling} job={enablingJob} />;

  const search = async () => {
    setSearching(true);
    try {
      setImportable(await t.call("storage.zfs.importable", undefined));
    } catch (e) {
      toast({ level: "warn", title: "No se pudieron buscar pools", body: errText(e) });
    } finally {
      setSearching(false);
    }
  };

  return (
    <div className="ts-stack">
      <div className="ts-zfs-bar">
        <span className="tg-muted">
          OpenZFS {st.version?.split("-")[0]} · {st.pools.length} {st.pools.length === 1 ? "pool" : "pools"}
        </span>
        <span className="ts-push" />
        <Button onClick={search} disabled={searching}>
          <Download size={14} /> {searching ? "Buscando…" : "Buscar pools para importar"}
        </Button>
        <Button variant="primary" onClick={() => setCreating(true)}>
          <Plus size={14} /> Crear pool ZFS
        </Button>
      </div>
      {importable && <ImportList pools={importable} disks={disks} onDone={() => setImportable(null)} />}
      {st.pools.length === 0 ? (
        <EmptyState icon={Boxes} title="Todavía no hay pools ZFS" action={<Button variant="primary" onClick={() => setCreating(true)}>Crear pool ZFS…</Button>}>
          Un pool junta discos en espejo o RAIDZ; adentro creás volúmenes, con cifrado si querés.
        </EmptyState>
      ) : (
        st.pools.map((p) => <ZfsPoolCard key={p.id} p={p} disks={disks} vols={vols} />)
      )}
      <ZfsWizard open={creating} onClose={() => setCreating(false)} disks={disks} vols={vols} pools={st.pools} />
    </div>
  );
}

function EnableZfs({ enabling, job }: { enabling?: string; job?: { progress: number; detail: string; state: string } }) {
  const t = useTransport();
  const [busy, setBusy] = useState(false);
  const enable = async () => {
    setBusy(true);
    try {
      await t.call("storage.zfs.enable", undefined);
    } catch (e) {
      toast({ level: "warn", title: "No se pudo habilitar ZFS", body: errText(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="tg-card ts-zfs-enable">
      <div className="ts-zfs-enable-head">
        <Boxes size={28} strokeWidth={1.5} />
        <div>
          <h3>ZFS</h3>
          <p className="tg-muted">Una alternativa a los grupos de almacenamiento: espejos y RAIDZ con checksums de punta a punta, instantáneas instantáneas, compresión y cifrado nativo.</p>
        </div>
      </div>
      <ul className="ts-zfs-points">
        <li>
          <Cpu size={14} /> Se instala a pedido: OpenZFS se compila para el kernel de este equipo (DKMS). Tarda unos minutos y ocupa unos 300 MB.
        </li>
        <li>
          <ShieldCheck size={14} /> Licencia: OpenZFS se distribuye con la licencia CDDL, compatible con el código abierto pero no con la GPL del kernel. Por eso no viene incluido: se descarga de Debian (componente contrib) en este equipo.
        </li>
        <li>
          <RefreshCw size={14} /> Con cada kernel nuevo, DKMS lo vuelve a compilar solo.
        </li>
      </ul>
      {enabling ? (
        <div className="ts-wiz-progress">
          <Progress value={job?.progress ?? 0} />
          <span className="tg-num">{Math.round((job?.progress ?? 0) * 100)} %</span>
          <span className="tg-muted ts-zfs-stage">{job?.detail || "Preparando…"}</span>
        </div>
      ) : (
        <div className="ts-row-actions">
          <Button variant="primary" disabled={busy} onClick={enable}>
            <Download size={14} /> Habilitar ZFS
          </Button>
        </div>
      )}
    </section>
  );
}

// ---- Tarjeta de un pool -----------------------------------------------------------------------

type PoolSheet = "volume" | "data" | "cache" | "log" | "replace" | "zvol" | "destroy" | "export";

function ZfsPoolCard({ p, disks, vols }: { p: ZfsPool; disks: Disk[]; vols: Volume[] }) {
  const t = useTransport();
  const s = zfsPoolState(p);
  const pvols = vols.filter((v) => v.pool === p.id);
  const [sheet, setSheet] = useState<PoolSheet | null>(null);
  const [replaceOld, setReplaceOld] = useState<ZfsLeaf | null>(null);
  const sched = useLive("storage.zfs.scrubSchedule.get", { pool: p.id }, ["storage.changed"]).data;
  const scanning = p.scan?.state === "scanning";
  const bad = p.vdevs.flatMap((v) => v.disks).find((l) => l.state !== "ONLINE");
  const used = pvols.reduce((a, v) => a + v.used, 0);
  const zvolBytes = p.zvols.reduce((a, z) => a + z.size, 0);

  const scrub = () =>
    t.call("storage.zfs.scrub", { pool: p.id }).then(
      () => toast({ level: "info", title: `Verificando ${p.name}`, body: "ZFS lee todos los bloques y los compara con sus checksums." }),
      (e) => toast({ level: "warn", title: "No se pudo verificar", body: errText(e) }),
    );
  const setSched = (enabled: boolean, days: number) =>
    t.call("storage.zfs.scrubSchedule.set", { pool: p.id, enabled, days }).catch((e) => toast({ level: "warn", title: "No se pudo guardar", body: errText(e) }));
  const removeAux = (kind: "cache" | "log", vdev: string) =>
    t.call("storage.zfs.auxRemove", { pool: p.id, kind, vdev }).catch((e) => toast({ level: "warn", title: "No se pudo quitar", body: errText(e) }));
  const deleteZvol = (name: string) => t.call("storage.zfs.zvols.delete", { pool: p.id, name }).catch((e) => toast({ level: "warn", title: "No se pudo borrar", body: errText(e) }));

  return (
    <section className="tg-card ts-zfs-pool" aria-label={`Pool ${p.name}`}>
      <div className="ts-pool-head">
        <Boxes size={18} />
        <h3>{p.name}</h3>
        <Badge>{zfsLayoutLabel(p.layout)}</Badge>
        {p.readonly && <Badge tone="warn">Solo lectura</Badge>}
        <Status tone={s.tone}>{s.text}</Status>
        <span className="ts-push tg-num tg-muted">
          {fmtBytes(p.allocated)} usados de {fmtBytes(p.size)}
        </span>
      </div>
      {scanning && (
        <div className="ts-vol-progress" style={{ marginTop: 8 }}>
          <Progress value={p.scan?.progress ?? 0} tone={p.scan?.function === "resilver" ? "warn" : undefined} />
          <span className="tg-num tg-muted">{Math.round((p.scan?.progress ?? 0) * 100)} %</span>
        </div>
      )}
      <div className="ts-alloc" aria-label={`Usado ${fmtBytes(p.allocated)}, libre ${fmtBytes(p.free)}`}>
        {pvols.map((v) => (
          <div key={v.id} style={{ flex: Math.max(v.used, p.size * 0.004), background: volumeColor(v.id, vols) }} title={`${v.name} · ${fmtBytes(v.used)}`} />
        ))}
        {zvolBytes > 0 && <div style={{ flex: zvolBytes, background: "var(--ts-zvol)" }} title={`Volúmenes de bloque · ${fmtBytes(zvolBytes)}`} />}
        <div style={{ flex: Math.max(p.size - used - zvolBytes, 0) }} />
      </div>
      <div className="ts-alloc-legend">
        {pvols.map((v) => (
          <span key={v.id}>
            <i style={{ background: volumeColor(v.id, vols) }} /> {v.name} <b>{fmtBytes(v.used)}</b>
          </span>
        ))}
        {zvolBytes > 0 && (
          <span>
            <i style={{ background: "var(--ts-zvol)" }} /> Bloque (iSCSI) <b>{fmtBytes(zvolBytes)}</b>
          </span>
        )}
        <span>
          <i style={{ background: "var(--tg-track)" }} /> Libre <b>{fmtBytes(p.free)}</b>
        </span>
      </div>

      <div className="ts-card-title" style={{ marginTop: 16 }}>
        Vdevs y discos
      </div>
      <VdevMap p={p} disks={disks} onReplace={(l) => (setReplaceOld(l), setSheet("replace"))} onRemove={removeAux} />

      <div className="ts-zfs-scan">
        <ShieldCheck size={14} />
        <span>
          {scanning
            ? p.scan?.function === "resilver"
              ? "Reconstruyendo los datos en el disco nuevo…"
              : "Verificando los checksums de todos los bloques…"
            : p.scan?.end
              ? `Última ${p.scan.function === "scrub" ? "verificación" : "reconstrucción"} ${fmtAgo(p.scan.end)}${p.scan.errors ? ` · ${p.scan.errors} errores` : " · sin errores"}`
              : "Nunca se verificó"}
        </span>
        <span className="ts-push" />
        {sched && (
          <label className="ts-zfs-sched">
            <Toggle checked={sched.enabled} onChange={(on) => setSched(on, sched.days)} label="Verificación automática" />
            Verificar cada
            <select value={sched.days} disabled={!sched.enabled} onChange={(e) => setSched(true, Number(e.target.value))} aria-label="Días entre verificaciones">
              {[7, 14, 30, 60, 90].map((d) => (
                <option key={d} value={d}>
                  {d} días
                </option>
              ))}
            </select>
          </label>
        )}
        {scanning ? (
          p.scan?.function === "scrub" && (
            <Button variant="plain" onClick={() => t.call("storage.zfs.scrub", { pool: p.id, stop: true })}>
              Detener
            </Button>
          )
        ) : (
          <Button disabled={p.readonly || p.state !== "healthy"} onClick={scrub}>
            Verificar ahora
          </Button>
        )}
      </div>

      {(pvols.length > 0 || p.zvols.length > 0 || p.other.length > 0) && (
        <div className="ts-pool-vols">
          {pvols.map((v) => (
            <div key={v.id} className="ts-pool-vol">
              {v.locked ? <Lock size={14} /> : v.encrypted ? <KeyRound size={14} /> : <Database size={14} />}
              <strong>{v.name}</strong>
              <span className="tg-muted">
                {v.mount || "sin montar"} · {fmtBytes(v.used)} usados{v.locked ? " · bloqueado" : v.encrypted ? " · cifrado (AES-256-GCM)" : ""}
              </span>
            </div>
          ))}
          {p.zvols.map((z) => (
            <div key={z.name} className="ts-pool-vol">
              <HardDrive size={14} />
              <strong>{z.name}</strong>
              <span className="tg-muted">
                Volumen de bloque · {fmtBytes(z.size)} · {z.device}
              </span>
              <span className="ts-push">
                <Button variant="plain" onClick={() => deleteZvol(z.name)} aria-label={`Borrar ${z.name}`}>
                  <Trash2 size={13} />
                </Button>
              </span>
            </div>
          ))}
          {p.other.map((o) => (
            <div key={o.name} className="ts-pool-vol">
              <Database size={14} />
              <strong>{o.name}</strong>
              <span className="tg-muted">
                {o.mounted ? o.mountpoint : "sin montar"} · {fmtBytes(o.used)} · de otro equipo
              </span>
            </div>
          ))}
        </div>
      )}

      <div className="ts-row-actions">
        <Button variant="primary" disabled={p.readonly} onClick={() => setSheet("volume")}>
          <Plus size={14} /> Crear volumen…
        </Button>
        {bad && (
          <Button variant="primary" onClick={() => (setReplaceOld(bad), setSheet("replace"))}>
            <RefreshCw size={14} /> Reemplazar disco…
          </Button>
        )}
        <Button disabled={p.readonly || p.layout === "mixed" || p.state !== "healthy"} onClick={() => setSheet("data")} title="Sumar un vdev con la misma forma">
          <Plus size={14} /> Sumar discos…
        </Button>
        <Button disabled={p.readonly} onClick={() => setSheet("cache")}>
          <Zap size={14} /> Caché o registro SSD…
        </Button>
        <Button disabled={p.readonly} onClick={() => setSheet("zvol")}>
          <HardDrive size={14} /> Volumen de bloque…
        </Button>
        <Button onClick={() => setSheet("export")}>
          <Upload size={14} /> Exportar…
        </Button>
        <Button variant="danger" onClick={() => setSheet("destroy")}>
          <Trash2 size={14} /> Destruir…
        </Button>
      </div>

      <ZfsVolumeSheet open={sheet === "volume"} pool={p} vols={vols} onClose={() => setSheet(null)} />
      <ZfsAddSheet open={sheet === "data" || sheet === "cache" || sheet === "log"} initial={sheet === "data" ? "data" : "cache"} pool={p} disks={disks} vols={vols} onClose={() => setSheet(null)} />
      <ZfsReplaceSheet open={sheet === "replace"} pool={p} old={replaceOld} disks={disks} vols={vols} onClose={() => setSheet(null)} />
      <ZvolSheet open={sheet === "zvol"} pool={p} onClose={() => setSheet(null)} />
      <ZfsDropSheet open={sheet === "destroy" || sheet === "export"} mode={sheet === "export" ? "export" : "destroy"} pool={p} onClose={() => setSheet(null)} />
    </section>
  );
}

function VdevMap({ p, disks, onReplace, onRemove }: { p: ZfsPool; disks: Disk[]; onReplace: (l: ZfsLeaf) => void; onRemove: (kind: "cache" | "log", vdev: string) => void }) {
  const row = (v: ZfsVdev | null, kind: "data" | "log" | "cache", leaves: ZfsLeaf[], label: string) => (
    <div className="ts-vdev" data-kind={kind} key={`${kind}-${v?.name ?? leaves[0]?.name}`}>
      <div className="ts-vdev-label">
        <strong>{label}</strong>
        <small>{v ? (v.type === "disk" ? "Disco suelto" : `${zfsLayoutLabel(v.type)} · tolera ${zfsTolerance(v.type as ZfsLayout, v.disks.length)}`) : kind === "cache" ? "Caché de lectura" : ""}</small>
      </div>
      <div className="ts-vdev-disks">
        {leaves.map((l) => {
          const d = disks.find((x) => x.id === l.id);
          const tone = leafTone(l);
          const errs = l.read + l.write + l.cksum;
          return (
            <button key={l.guid || l.name} type="button" className="ts-vdev-disk" data-tone={tone} title={`${l.name} · ${l.state}`} onClick={() => kind === "data" && tone !== "ok" && onReplace(l)}>
              <HardDrive size={14} strokeWidth={1.6} />
              <span>
                <b>{d ? diskLabel(d) : l.state === "ONLINE" ? l.name : "Disco que falta"}</b>
                <small>
                  {d ? fmtBytes(d.size) : `GUID ${l.guid}`} · {l.resilvering ? "recibiendo datos" : l.state === "ONLINE" ? "en línea" : l.state.toLowerCase()}
                  {errs > 0 && ` · L${l.read} E${l.write} C${l.cksum}`}
                </small>
              </span>
            </button>
          );
        })}
      </div>
      {kind !== "data" && (
        <Button variant="plain" onClick={() => onRemove(kind === "cache" ? "cache" : "log", v?.name ?? leaves[0].name)} aria-label="Quitar">
          <Trash2 size={13} />
        </Button>
      )}
    </div>
  );
  return (
    <div className="ts-vdevs">
      {p.vdevs.map((v, i) => row(v, "data", v.disks, p.vdevs.length > 1 ? `Vdev ${i + 1}` : "Datos"))}
      {p.logs.map((v) => row(v, "log", v.disks, "Registro (SLOG)"))}
      {p.cache.map((l) => row(null, "cache", [l], "Caché (L2ARC)"))}
    </div>
  );
}

// ---- Importar ---------------------------------------------------------------------------------

function ImportList({ pools, disks, onDone }: { pools: ZfsImportable[]; disks: Disk[]; onDone: () => void }) {
  const t = useTransport();
  const imp = (p: ZfsImportable, readonly: boolean) =>
    t.call("storage.zfs.import", { pool: p.guid || p.name, readonly }).then(
      () => {
        toast({ level: "info", title: `Importando ${p.name}`, body: readonly ? `Queda en solo lectura en /import/${p.name}: nada se escribe en los discos.` : "Sus volúmenes se montan al terminar." });
        onDone();
      },
      (e) => toast({ level: "warn", title: "No se pudo importar", body: errText(e) }),
    );
  if (!pools.length)
    return (
      <section className="tg-card">
        <p className="tg-muted">No se encontraron pools ZFS sin importar en los discos conectados.</p>
      </section>
    );
  return (
    <section className="tg-card">
      <div className="ts-card-title">Pools encontrados en los discos</div>
      {pools.map((p) => (
        <div key={p.guid} className="ts-zfs-import">
          <Boxes size={18} />
          <div>
            <strong>{p.name}</strong>
            <small className="tg-muted">
              {p.state} · {p.disks.map((id) => disks.find((d) => d.id === id)).filter(Boolean).map((d) => diskLabel(d!)).join(", ") || `${p.disks.length} discos`}
              {p.status ? ` · ${p.status}` : ""}
            </small>
          </div>
          <span className="ts-push" />
          <Button onClick={() => imp(p, true)}>Ver en solo lectura</Button>
          <Button variant="primary" onClick={() => imp(p, false)}>
            Importar
          </Button>
        </div>
      ))}
      <p className="ts-note">«Ver en solo lectura» es lo más seguro con un pool de otro equipo: se monta en /import y nada se escribe en sus discos.</p>
    </section>
  );
}

// ---- Asistente: crear un pool -------------------------------------------------------------------

const STEPS = ["Discos", "Forma", "Nombre", "Revisión"];

/** Diagrama: cada disco partido en datos, redundancia y lo que no se usa. */
export function ZfsDiagram({ layout, disks }: { layout: ZfsLayout; disks: { id: string; label: string; size: number }[] }) {
  const split = zfsSplit(layout, disks.map((d) => d.size));
  const max = Math.max(...disks.map((d) => d.size), 1);
  return (
    <div className="ts-zfs-diagram" aria-label="Reparto de cada disco">
      {disks.map((d, i) => {
        const s = split[i];
        return (
          <div key={d.id} className="ts-zfs-diag-row">
            <small>{d.label}</small>
            <div className="ts-zfs-diag-bar" style={{ width: `${(d.size / max) * 100}%` }}>
              <i data-part="data" style={{ flex: s.data }} title={`Datos · ${fmtBytes(s.data)}`} />
              {s.redundancy > 0 && <i data-part="red" style={{ flex: s.redundancy }} title={`${layout === "mirror" ? "Copias" : "Paridad"} · ${fmtBytes(s.redundancy)}`} />}
              {s.unused > 0 && <i data-part="unused" style={{ flex: s.unused }} title={`Sin usar · ${fmtBytes(s.unused)}`} />}
            </div>
            <small className="tg-num">{fmtBytes(d.size)}</small>
          </div>
        );
      })}
      <div className="ts-zfs-diag-legend">
        <span>
          <i data-part="data" /> Datos
        </span>
        <span>
          <i data-part="red" /> {layout === "mirror" ? "Copias" : "Paridad"}
        </span>
        <span>
          <i data-part="unused" /> Sin usar
        </span>
      </div>
    </div>
  );
}

function usePlanJob() {
  const jobs = useJobs();
  const [jobId, setJobId] = useState<string | null>(null);
  const [seen, setSeen] = useState(false);
  const job = jobs.find((j) => j.id === jobId);
  useEffect(() => {
    if (job) setSeen(true);
  }, [job]);
  const failed = !!job && (job.state === "failed" || job.state === "interrupted" || job.state === "cancelled");
  const done = !!jobId && !failed && ((job && job.state === "succeeded") || (seen && !job));
  return { jobId, setJobId: (id: string | null) => (setJobId(id), setSeen(false)), job, failed, done };
}

function JobView({ title, doneTitle, failTitle, doneBody, body, pj, onClose }: { title: string; doneTitle: string; failTitle: string; doneBody: string; body: string; pj: ReturnType<typeof usePlanJob>; onClose: () => void }) {
  return (
    <div className="ts-wiz-done">
      {pj.done ? <CheckCircle2 size={44} className="ts-ok-icon" /> : pj.failed ? <XOctagon size={44} className="ts-crit-icon" /> : <Boxes size={40} className="ts-muted-icon" />}
      <h2 className="tg-h2">{pj.done ? doneTitle : pj.failed ? failTitle : title}</h2>
      <p className="tg-muted">{pj.done ? doneBody : pj.failed ? `La tarea falló. ${pj.job?.detail ?? ""}` : pj.job?.detail || body}</p>
      {!pj.done && !pj.failed && (
        <div className="ts-wiz-progress">
          <Progress value={pj.job?.progress ?? 0} />
          <span className="tg-num">{Math.round((pj.job?.progress ?? 0) * 100)} %</span>
        </div>
      )}
      <Button variant="primary" onClick={onClose}>
        {pj.done || pj.failed ? "Cerrar" : "Seguir en segundo plano"}
      </Button>
    </div>
  );
}

function DiskPicker({ disks, vols, sel, setSel, ssdOnly }: { disks: Disk[]; vols: Volume[]; sel: string[]; setSel: (f: (s: string[]) => string[]) => void; ssdOnly?: boolean }) {
  const candidates = disks.filter((d) => d.use !== "system" && (!ssdOnly || d.kind !== "hdd"));
  return (
    <div className="ts-pick" data-many={candidates.length > 6 || undefined}>
      {candidates.map((d) => {
        const reason = ineligibleReason(d, vols);
        const on = sel.includes(d.id);
        return (
          <button key={d.id} type="button" className="ts-pick-disk" aria-pressed={on} disabled={!!reason} onClick={() => setSel((s) => (on ? s.filter((x) => x !== d.id) : [...s, d.id]))}>
            <span className="ts-pick-check">{on && <Check size={12} strokeWidth={3} />}</span>
            <HardDrive size={22} strokeWidth={1.5} />
            <span className="ts-pick-text">
              <strong>{diskLabel(d)}</strong>
              <small>
                {fmtBytes(d.size)} · {d.kind === "hdd" ? "HDD" : d.kind === "ssd" ? "SSD" : "NVMe"} · serie {d.serial}
              </small>
              {reason && <em>{reason}</em>}
            </span>
          </button>
        );
      })}
    </div>
  );
}

function PlanReview({ plan, disks, confirm, setConfirm, layout }: { plan: ZfsPlan; disks: Disk[]; confirm: string; setConfirm: (s: string) => void; layout?: ZfsLayout }) {
  const left = useCountdown(plan.expires);
  return (
    <>
      <div className="ts-danger">
        <AlertTriangle size={18} />
        <div>
          <strong>Se van a borrar estos discos</strong>
          <p>Todo lo que contienen se pierde. TangoOS vuelve a verificar la identidad de cada disco justo antes de empezar.</p>
        </div>
      </div>
      <div className="ts-erase">
        {plan.summary.disks.map((d) => {
          const local = disks.find((x) => x.id === d.id);
          return (
            <div key={d.id} className="ts-erase-row">
              <HardDrive size={18} strokeWidth={1.6} />
              <div>
                <strong>
                  {local?.bay ? `Bahía ${local.bay}` : "Disco"} · {d.model}
                </strong>
                <small>
                  Serie {d.serial} · {fmtBytes(d.size)}
                </small>
                <small className="ts-erase-content">Contenido: {d.content}</small>
              </div>
            </div>
          );
        })}
      </div>
      {layout && <ZfsDiagram layout={layout} disks={plan.summary.disks.map((d) => ({ id: d.id, label: d.model, size: d.size }))} />}
      {plan.summary.notes.map((n) => (
        <p key={n} className="ts-note">
          {n}
        </p>
      ))}
      <div className="ts-summary">
        {plan.summary.kind !== "replace" && plan.summary.kind !== "cache" && plan.summary.kind !== "log" && (
          <span>
            <Boxes size={13} /> {zfsLayoutLabel(plan.summary.layout)} · ≈ {fmtBytes(plan.summary.usable)} útiles
          </span>
        )}
        {left !== null && (
          <span className="ts-plan-exp" data-soon={left < 60 || undefined}>
            <Clock size={12} /> Este plan vence en {Math.floor(left / 60)}:{String(left % 60).padStart(2, "0")}
          </span>
        )}
      </div>
      <label className="ts-field">
        <span>
          Para confirmar, escribí <code>{plan.summary.name}</code>
        </span>
        <TextInput value={confirm} onChange={(e) => setConfirm(e.target.value)} aria-label={`Escribí ${plan.summary.name} para confirmar`} autoComplete="off" />
      </label>
    </>
  );
}

function ZfsWizard({ open, onClose, disks, vols, pools }: { open: boolean; onClose: () => void; disks: Disk[]; vols: Volume[]; pools: ZfsPool[] }) {
  const t = useTransport();
  const pj = usePlanJob();
  const [step, setStep] = useState(0);
  const [sel, setSel] = useState<string[]>([]);
  const [layout, setLayout] = useState<ZfsLayout>("mirror");
  const [name, setName] = useState("");
  const [confirm, setConfirm] = useState("");
  const [plan, setPlan] = useState<ZfsPlan | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setStep(0);
    setSel([]);
    setName(pools.some((p) => p.name === "tanque") ? `tanque${pools.length + 1}` : "tanque");
    setConfirm("");
    setPlan(null);
    setError(null);
    pj.setJobId(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  useEffect(() => {
    const n = sel.length;
    setLayout(n === 1 ? "stripe" : n === 2 ? "mirror" : n < 6 ? "raidz1" : "raidz2");
  }, [sel.length]);

  const chosen = disks.filter((d) => sel.includes(d.id));
  const nameOk = /^[a-z][a-z0-9_]{0,31}$/.test(name) && !/^(mirror|raidz|draid|log|cache|spare)/.test(name) && !pools.some((p) => p.name === name) && !vols.some((v) => v.name === name);
  const canNext = step === 0 ? sel.length > 0 : step === 1 ? zfsCheck(layout, sel.length) === null : step === 2 ? nameOk : !!plan && confirm === name;

  const makePlan = async () => {
    setPlan(null);
    setError(null);
    try {
      setPlan(await t.call("storage.zfs.plan", { name, layout, disks: sel }));
    } catch (e) {
      setError(errText(e));
    }
  };
  useEffect(() => {
    if (step === 3) void makePlan();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);

  const create = async () => {
    if (!plan) return;
    setBusy(true);
    setError(null);
    try {
      const r = await t.call("storage.zfs.create", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm });
      pj.setJobId(r.job);
    } catch (e) {
      setError(errText(e));
      if (e instanceof RpcError && e.code === "PLAN_EXPIRED") void makePlan();
    } finally {
      setBusy(false);
    }
  };

  if (pj.jobId)
    return (
      <Sheet open={open} onClose={onClose} width={520}>
        <JobView title={`Creando el pool ${name}…`} doneTitle={`El pool ${name} está listo`} failTitle={`No se pudo crear el pool ${name}`} doneBody="Creá un volumen dentro: es donde viven las carpetas compartidas." body="Se preparan los discos y se crea el pool." pj={pj} onClose={onClose} />
      </Sheet>
    );

  return (
    <Sheet
      open={open}
      onClose={onClose}
      width={660}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
              <Button variant="plain" onClick={() => void makePlan()}>
                <RotateCcw size={12} /> Volver a revisar
              </Button>
            </span>
          )}
          <Button onClick={step === 0 ? onClose : () => setStep(step - 1)}>{step === 0 ? "Cancelar" : "Atrás"}</Button>
          {step < 3 ? (
            <Button variant="primary" disabled={!canNext} onClick={() => setStep(step + 1)}>
              Continuar
            </Button>
          ) : (
            <Button variant="danger" disabled={!canNext || busy} onClick={create}>
              Borrar discos y crear el pool
            </Button>
          )}
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Crear pool ZFS</h2>
        <ol className="ts-steps" aria-label="Pasos">
          {STEPS.map((s, i) => (
            <li key={s} data-state={i < step ? "done" : i === step ? "current" : "todo"} aria-current={i === step ? "step" : undefined}>
              <span>{i < step ? <Check size={11} strokeWidth={3} /> : i + 1}</span>
              {s}
            </li>
          ))}
        </ol>
      </div>
      {step === 0 && (
        <>
          <p className="tg-muted ts-wiz-lead">Elegí los discos del pool. Conviene que sean del mismo tamaño: en un vdev, ZFS usa en cada disco lo que tiene el más chico.</p>
          <DiskPicker disks={disks} vols={vols} sel={sel} setSel={setSel} />
        </>
      )}
      {step === 1 && (
        <>
          <div className="ts-profiles ts-profiles-pool" role="radiogroup" aria-label="Forma">
            {ZFS_LAYOUTS.map((info) => {
              const why = zfsCheck(info.id, sel.length);
              return (
                <button key={info.id} type="button" role="radio" aria-checked={layout === info.id} className="ts-profile" disabled={!!why} onClick={() => setLayout(info.id)}>
                  <strong>{info.label}</strong>
                  <small>{info.description}</small>
                  <small className="ts-tolerance" data-none={info.id === "stripe" || undefined}>
                    {zfsTolerance(info.id, sel.length) === 0 ? "No soporta la falla de ningún disco." : `Soporta la falla de ${zfsTolerance(info.id, sel.length)} ${zfsTolerance(info.id, sel.length) === 1 ? "disco" : "discos"}.`}
                  </small>
                  {why && <em>{why}</em>}
                </button>
              );
            })}
          </div>
          {zfsCheck(layout, sel.length) === null && (
            <>
              <ZfsDiagram layout={layout} disks={chosen.map((d) => ({ id: d.id, label: diskLabel(d), size: d.size }))} />
              <p className="ts-note">
                Capacidad útil estimada: <b className="tg-num">≈ {fmtBytes(zfsUsable(layout, chosen.map((d) => d.size)) * 0.97)}</b> (ZFS reserva alrededor de un 3 %). Compresión LZ4 activada: suele dar más lugar del que dice.
              </p>
            </>
          )}
        </>
      )}
      {step === 2 && (
        <div className="ts-form">
          <label className="ts-field">
            <span>Nombre del pool</span>
            <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-label="Nombre del pool" />
            <small>{nameOk || !name ? "Minúsculas, números o _ (hasta 32). Los volúmenes del pool llevan su propio nombre." : "Ese nombre no está disponible o no es válido."}</small>
          </label>
        </div>
      )}
      {step === 3 && (plan ? <PlanReview plan={plan} disks={disks} confirm={confirm} setConfirm={setConfirm} layout={layout} /> : !error && <p className="tg-muted ts-wiz-lead">Armando el plan con el estado actual de los discos…</p>)}
    </Sheet>
  );
}

// ---- Sumar vdev / caché / registro ---------------------------------------------------------------

function ZfsAddSheet({ open, initial, pool, disks, vols, onClose }: { open: boolean; initial: "data" | "cache"; pool: ZfsPool; disks: Disk[]; vols: Volume[]; onClose: () => void }) {
  const t = useTransport();
  const pj = usePlanJob();
  const [kind, setKind] = useState<"data" | "cache" | "log">(initial);
  const [sel, setSel] = useState<string[]>([]);
  const [plan, setPlan] = useState<ZfsPlan | null>(null);
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!open) return;
    setKind(initial);
    setSel([]);
    setPlan(null);
    setConfirm("");
    setError(null);
    pj.setJobId(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const review = async () => {
    setError(null);
    try {
      setPlan(await t.call("storage.zfs.addPlan", { pool: pool.id, kind, disks: sel }));
    } catch (e) {
      setError(errText(e));
    }
  };
  const go = async () => {
    if (!plan) return;
    try {
      pj.setJobId((await t.call("storage.zfs.add", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm })).job);
    } catch (e) {
      setError(errText(e));
    }
  };
  if (pj.jobId)
    return (
      <Sheet open={open} onClose={onClose} width={520}>
        <JobView title={`Ampliando ${pool.name}…`} doneTitle={`Listo: ${pool.name}`} failTitle="No se pudo ampliar el pool" doneBody={kind === "data" ? "El vdev nuevo ya suma espacio." : "El SSD ya está en uso."} body="" pj={pj} onClose={onClose} />
      </Sheet>
    );
  const need = kind === "data" ? zfsCheck(pool.layout === "mixed" ? "stripe" : pool.layout, sel.length) : sel.length < 1 || sel.length > 2 ? "Elegí uno o dos SSD" : null;
  return (
    <Sheet
      open={open}
      onClose={onClose}
      width={640}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={plan ? () => setPlan(null) : onClose}>{plan ? "Atrás" : "Cancelar"}</Button>
          {plan ? (
            <Button variant="danger" disabled={confirm !== pool.name} onClick={go}>
              Borrar discos y sumarlos
            </Button>
          ) : (
            <Button variant="primary" disabled={!!need} onClick={review}>
              Revisar
            </Button>
          )}
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Sumar a {pool.name}</h2>
      </div>
      {plan ? (
        <PlanReview plan={plan} disks={disks} confirm={confirm} setConfirm={setConfirm} layout={kind === "data" ? (pool.layout as ZfsLayout) : undefined} />
      ) : (
        <>
          <div className="ts-profiles ts-profiles-pool" role="radiogroup" aria-label="Qué sumar">
            {(
              [
                ["data", "Más espacio", `Un vdev ${zfsLayoutLabel(pool.layout)} nuevo. No se puede quitar después.`],
                ["cache", "Caché de lectura (L2ARC)", "Un SSD que guarda lo que más se lee. Si falla no se pierde nada."],
                ["log", "Registro de escritura (SLOG)", "Acelera escrituras sincrónicas (NFS, iSCSI, bases de datos). Mejor dos SSD en espejo."],
              ] as const
            ).map(([k, label, desc]) => (
              <button key={k} type="button" role="radio" aria-checked={kind === k} className="ts-profile" disabled={k === "data" && pool.layout === "mixed"} onClick={() => (setKind(k), setSel([]))}>
                <strong>{label}</strong>
                <small>{desc}</small>
              </button>
            ))}
          </div>
          <DiskPicker disks={disks} vols={vols} sel={sel} setSel={setSel} ssdOnly={kind !== "data"} />
          {need && sel.length > 0 && <p className="ts-note">{need}</p>}
        </>
      )}
    </Sheet>
  );
}

// ---- Reemplazar un disco ------------------------------------------------------------------------

function ZfsReplaceSheet({ open, pool, old, disks, vols, onClose }: { open: boolean; pool: ZfsPool; old: ZfsLeaf | null; disks: Disk[]; vols: Volume[]; onClose: () => void }) {
  const t = useTransport();
  const pj = usePlanJob();
  const leaves = pool.vdevs.flatMap((v) => v.disks);
  const [target, setTarget] = useState<string>("");
  const [sel, setSel] = useState<string[]>([]);
  const [plan, setPlan] = useState<ZfsPlan | null>(null);
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!open) return;
    setTarget(old?.guid || old?.name || leaves[0]?.guid || "");
    setSel([]);
    setPlan(null);
    setConfirm("");
    setError(null);
    pj.setJobId(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const review = async () => {
    setError(null);
    const l = leaves.find((x) => x.guid === target || x.name === target);
    try {
      setPlan(await t.call("storage.zfs.replacePlan", { pool: pool.id, old: l?.id ? l.name : (l?.guid ?? target), replacement: sel[0] }));
    } catch (e) {
      setError(errText(e));
    }
  };
  const go = async () => {
    if (!plan) return;
    try {
      pj.setJobId((await t.call("storage.zfs.replace", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm })).job);
    } catch (e) {
      setError(errText(e));
    }
  };
  if (pj.jobId)
    return (
      <Sheet open={open} onClose={onClose} width={520}>
        <JobView title="Reconstruyendo (resilver)…" doneTitle={`${pool.name} recuperó la redundancia`} failTitle="No se pudo reemplazar el disco" doneBody="ZFS copió los datos al disco nuevo." body="ZFS copia solo los bloques usados: suele ser mucho más rápido que un RAID." pj={pj} onClose={onClose} />
      </Sheet>
    );
  return (
    <Sheet
      open={open}
      onClose={onClose}
      width={640}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={plan ? () => setPlan(null) : onClose}>{plan ? "Atrás" : "Cancelar"}</Button>
          {plan ? (
            <Button variant="danger" disabled={confirm !== pool.name} onClick={go}>
              Reemplazar
            </Button>
          ) : (
            <Button variant="primary" disabled={!target || sel.length !== 1} onClick={review}>
              Revisar
            </Button>
          )}
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Reemplazar un disco de {pool.name}</h2>
      </div>
      {plan ? (
        <PlanReview plan={plan} disks={disks} confirm={confirm} setConfirm={setConfirm} />
      ) : (
        <>
          <label className="ts-field">
            <span>Disco a reemplazar</span>
            <select className="tg-input" value={target} onChange={(e) => setTarget(e.target.value)} aria-label="Disco a reemplazar">
              {leaves.map((l) => {
                const d = disks.find((x) => x.id === l.id);
                return (
                  <option key={l.guid} value={l.guid}>
                    {d ? diskLabel(d) : `Disco que falta (${l.guid})`} · {l.state.toLowerCase()}
                  </option>
                );
              })}
            </select>
          </label>
          <p className="tg-muted ts-wiz-lead">Elegí el disco nuevo (igual o más grande).</p>
          <DiskPicker disks={disks} vols={vols} sel={sel} setSel={(f) => setSel((s) => f(s).slice(-1))} />
        </>
      )}
    </Sheet>
  );
}

// ---- Volumen, zvol, exportar y destruir --------------------------------------------------------------

function ZfsVolumeSheet({ open, pool, vols, onClose }: { open: boolean; pool: ZfsPool; vols: Volume[]; onClose: () => void }) {
  const t = useTransport();
  const pj = usePlanJob();
  const [name, setName] = useState("");
  const [quota, setQuota] = useState("");
  const [enc, setEnc] = useState(false);
  const [pass, setPass] = useState("");
  const [pass2, setPass2] = useState("");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!open) return;
    let n = vols.length + 1;
    while (vols.some((v) => v.name === `volume${n}`)) n++;
    setName(`volume${n}`);
    setQuota("");
    setEnc(false);
    setPass("");
    setPass2("");
    setError(null);
    pj.setJobId(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const q = quota ? Math.round(Number(quota) * 1024 ** 3) : 0;
  const ok = /^[a-z][a-z0-9]{0,31}$/.test(name) && !vols.some((v) => v.name === name) && (!enc || (pass.length >= 8 && pass === pass2)) && !(quota && !(Number(quota) > 0));
  const go = async () => {
    setError(null);
    try {
      pj.setJobId((await t.call("storage.zfs.volumes.create", { pool: pool.id, name, quota: q || undefined, encryption: enc ? { passphrase: pass } : null })).job);
      setPass("");
      setPass2("");
    } catch (e) {
      setError(errText(e));
    }
  };
  if (pj.jobId)
    return (
      <Sheet open={open} onClose={onClose} width={520}>
        <JobView title={`Creando ${name}…`} doneTitle={`${name} está listo`} failTitle={`No se pudo crear ${name}`} doneBody="Ya podés crear carpetas compartidas en él." body="" pj={pj} onClose={onClose} />
      </Sheet>
    );
  return (
    <Sheet
      open={open}
      onClose={onClose}
      width={520}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="primary" disabled={!ok} onClick={go}>
            Crear volumen
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Volumen en {pool.name}</h2>
      </div>
      <div className="ts-form">
        <label className="ts-field">
          <span>Nombre</span>
          <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-label="Nombre del volumen" />
          <small>Queda en /{name || "…"}. Comparte el espacio libre del pool ({fmtBytes(pool.free)}).</small>
        </label>
        <label className="ts-field">
          <span>Cuota (GB, opcional)</span>
          <TextInput value={quota} inputMode="decimal" placeholder="Sin límite" onChange={(e) => setQuota(e.target.value.replace(",", "."))} aria-label="Cuota en GB" />
        </label>
        <label className="ts-field ts-field-inline">
          <Toggle checked={enc} onChange={setEnc} label="Cifrar el volumen" />
          <span>Cifrar (AES-256-GCM, nativo de ZFS)</span>
        </label>
        {enc && (
          <>
            <label className="ts-field">
              <span>Frase</span>
              <TextInput type="password" value={pass} onChange={(e) => setPass(e.target.value)} aria-label="Frase de cifrado" autoComplete="new-password" />
              <small>Al menos 8 caracteres. Sin la frase, los datos no se pueden recuperar: guardala en un lugar seguro.</small>
            </label>
            <label className="ts-field">
              <span>Repetí la frase</span>
              <TextInput type="password" value={pass2} onChange={(e) => setPass2(e.target.value)} aria-label="Repetir la frase" autoComplete="new-password" />
            </label>
          </>
        )}
      </div>
    </Sheet>
  );
}

function ZvolSheet({ open, pool, onClose }: { open: boolean; pool: ZfsPool; onClose: () => void }) {
  const t = useTransport();
  const [name, setName] = useState("lun1");
  const [gb, setGb] = useState("64");
  const [sparse, setSparse] = useState(true);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) setError(null);
  }, [open]);
  const size = Math.round(Number(gb) * 1024 ** 3 / (1024 ** 2)) * 1024 ** 2;
  const go = async () => {
    try {
      await t.call("storage.zfs.zvols.create", { pool: pool.id, name, size, sparse });
      toast({ level: "ok", title: `Volumen de bloque ${name} creado`, body: "Usalo como LUN en iSCSI." });
      onClose();
    } catch (e) {
      setError(errText(e));
    }
  };
  return (
    <Sheet
      open={open}
      onClose={onClose}
      width={480}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="primary" disabled={!/^[a-z][a-z0-9_-]{0,31}$/.test(name) || !(Number(gb) >= 0.0625)} onClick={go}>
            Crear
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Volumen de bloque en {pool.name}</h2>
      </div>
      <p className="tg-muted ts-wiz-lead">Un disco virtual (zvol) para usar como LUN de iSCSI: máquinas virtuales, servidores, Windows.</p>
      <div className="ts-form">
        <label className="ts-field">
          <span>Nombre</span>
          <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-label="Nombre del volumen de bloque" />
        </label>
        <label className="ts-field">
          <span>Tamaño (GB)</span>
          <TextInput value={gb} inputMode="decimal" onChange={(e) => setGb(e.target.value.replace(",", "."))} aria-label="Tamaño en GB" />
        </label>
        <label className="ts-field ts-field-inline">
          <Toggle checked={sparse} onChange={setSparse} label="Provisión fina" />
          <span>Provisión fina: ocupa a medida que se escribe</span>
        </label>
      </div>
    </Sheet>
  );
}

function ZfsDropSheet({ open, mode, pool, onClose }: { open: boolean; mode: "export" | "destroy"; pool: ZfsPool; onClose: () => void }) {
  const t = useTransport();
  const [plan, setPlan] = useState<DeletePlan | null>(null);
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!open) return;
    setPlan(null);
    setConfirm("");
    setError(null);
    if (mode === "destroy") t.call("storage.zfs.destroyPlan", { pool: pool.id }).then(setPlan, (e) => setError(errText(e)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, mode]);
  const go = async () => {
    setError(null);
    try {
      if (mode === "export") await t.call("storage.zfs.export", { pool: pool.id });
      else if (plan) await t.call("storage.zfs.destroy", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm });
      toast({ level: "info", title: mode === "export" ? `Exportando ${pool.name}` : `Destruyendo ${pool.name}`, body: "Podés ver el progreso en Actividad." });
      onClose();
    } catch (e) {
      setError(errText(e));
    }
  };
  const vols = useMemo(() => pool.volumes.length, [pool.volumes]);
  return (
    <Sheet
      open={open}
      onClose={onClose}
      width={520}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant={mode === "destroy" ? "danger" : "primary"} disabled={confirm !== pool.name || (mode === "destroy" && !plan)} onClick={go}>
            {mode === "export" ? "Exportar" : "Destruir el pool"}
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">{mode === "export" ? `Exportar ${pool.name}` : `Destruir ${pool.name}`}</h2>
      </div>
      {mode === "export" ? (
        <p className="tg-muted ts-wiz-lead">
          Se desmontan sus {vols} volúmenes y el pool queda listo para desconectar sus discos y llevarlos a otro equipo. Los datos no se tocan; podés volver a importarlo desde «Buscar pools para importar».
        </p>
      ) : (
        <div className="ts-danger">
          <AlertTriangle size={18} />
          <div>
            <strong>Se borra todo lo que hay en el pool</strong>
            <p>
              {vols} volúmenes{plan?.summary.shares.length ? ` (${plan.summary.shares.join(", ")})` : ""}, {fmtBytes(plan?.summary.used ?? pool.allocated)} de datos. Los {plan?.summary.disks.length ?? pool.disks.length} discos quedan libres.
            </p>
          </div>
        </div>
      )}
      <label className="ts-field">
        <span>
          Para confirmar, escribí <code>{pool.name}</code>
        </span>
        <TextInput value={confirm} onChange={(e) => setConfirm(e.target.value)} aria-label={`Escribí ${pool.name} para confirmar`} autoComplete="off" />
      </label>
    </Sheet>
  );
}

// ---- Detalle de un volumen ZFS (Volúmenes) ---------------------------------------------------------

export function ZfsVolumeLayers({ v }: { v: Volume }) {
  const st = useLive("storage.zfs.status", undefined, ["storage.changed"]).data;
  const p = st?.pools.find((x) => x.id === v.pool);
  return (
    <div className="ts-layers">
      <div className="ts-layer">
        <small>Dataset</small>
        <strong>
          {p?.name ?? "?"}/{v.name} · {v.mount || `/${v.name} (sin montar)`}
        </strong>
      </div>
      {v.encrypted && (
        <div className="ts-layer">
          <small>Cifrado</small>
          <strong>Nativo de ZFS (AES-256-GCM) · {v.locked ? "clave cerrada" : "clave cargada"}</strong>
        </div>
      )}
      <div className="ts-layer">
        <small>Pool</small>
        <strong>
          {p?.name ?? "—"} · {p ? zfsLayoutLabel(p.layout) : ""} · {fmtBytes(p?.free ?? 0)} libres · compresión LZ4
        </strong>
      </div>
    </div>
  );
}
