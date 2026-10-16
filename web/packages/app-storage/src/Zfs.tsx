// Pools ZFS (protocol/README.md §18, ADR 0013): habilitar OpenZFS, crear e importar pools,
// vdevs con su estado, caché L2ARC y registro SLOG, scrub programado, volúmenes (datasets)
// con cifrado nativo y volúmenes de bloque (zvol) para iSCSI.
import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, Boxes, Check, CheckCircle2, Clock, Cpu, Database, Download, HardDrive, KeyRound, Lock, Plus, RefreshCw, RotateCcw, ShieldCheck, Trash2, Upload, XOctagon, Zap } from "lucide-react";
import { RpcError, ZFS_LAYOUTS, fmtAgo, fmtBytes, t, tn, tr, useJobs, useLive, useTransport, zfsCheck, zfsLayoutLabel, zfsSplit, zfsTolerance, zfsUsable } from "@tango/client";
import type { DeletePlan, Disk, Volume, ZfsImportable, ZfsLayout, ZfsLeaf, ZfsPlan, ZfsPool, ZfsVdev } from "@tango/client";
import { Badge, Button, EmptyState, Progress, Sheet, Status, TextInput, Toggle } from "@tango/ui";
import { toast } from "@tango/shell";
import { useCountdown } from "./CreateVolume";
import { ineligibleReason, volumeColor } from "./util";

const diskLabel = (d: Disk) => `${d.bay ? t("Bahía {n}", { n: d.bay }) : t("Interno")} · ${d.model}`;
const errText = (e: unknown) => (e instanceof RpcError ? e.message : (e as Error).message);

export function zfsPoolState(p: ZfsPool): { tone: "ok" | "warn" | "crit" | "info"; text: string } {
  if (p.scan?.state === "scanning") return p.scan.function === "resilver" ? { tone: "warn", text: t("Reconstruyendo (resilver)") } : { tone: "info", text: t("Verificando") };
  switch (p.state) {
    case "degraded":
      return { tone: "crit", text: t("Sin redundancia") };
    case "faulted":
      return { tone: "crit", text: t("No disponible ({health})", { health: p.health }) };
    case "readonly":
      return { tone: "warn", text: t("Solo lectura") };
  }
  const cksum = p.vdevs.reduce((a, v) => a + v.cksum + v.disks.reduce((b, l) => b + l.cksum, 0), 0);
  return cksum > 0 ? { tone: "warn", text: tn(cksum, "{n} error de checksum", "{n} errores de checksum") } : { tone: "ok", text: t("En orden") };
}

const leafTone = (l: ZfsLeaf): "ok" | "warn" | "crit" => (l.state === "ONLINE" ? (l.read + l.write + l.cksum > 0 ? "warn" : "ok") : l.state === "DEGRADED" ? "warn" : "crit");

// ---- Sección ----------------------------------------------------------------------------

export function ZfsSection({ disks, vols }: { disks: Disk[]; vols: Volume[] }) {
  const rpc = useTransport();
  const jobs = useJobs();
  const st = useLive("storage.zfs.status", undefined, ["storage.changed"]).data;
  const [creating, setCreating] = useState(false);
  const [importable, setImportable] = useState<ZfsImportable[] | null>(null);
  const [searching, setSearching] = useState(false);
  const enablingJob = jobs.find((j) => j.id === st?.enabling);

  if (!st) return <p className="tg-muted">{t("Cargando…")}</p>;
  if (!st.enabled) return <EnableZfs enabling={st.enabling} job={enablingJob} />;

  const search = async () => {
    setSearching(true);
    try {
      setImportable(await rpc.call("storage.zfs.importable", undefined));
    } catch (e) {
      toast({ level: "warn", title: t("No se pudieron buscar pools"), body: errText(e) });
    } finally {
      setSearching(false);
    }
  };

  return (
    <div className="ts-stack">
      <div className="ts-zfs-bar">
        <span className="tg-muted">
          OpenZFS {st.version?.split("-")[0]} · {tn(st.pools.length, "{n} pool", "{n} pools")}
        </span>
        <span className="ts-push" />
        <Button onClick={search} disabled={searching}>
          <Download size={14} /> {searching ? t("Buscando…") : t("Buscar pools para importar")}
        </Button>
        <Button variant="primary" onClick={() => setCreating(true)}>
          <Plus size={14} /> {t("Crear pool ZFS")}
        </Button>
      </div>
      {importable && <ImportList pools={importable} disks={disks} onDone={() => setImportable(null)} />}
      {st.pools.length === 0 ? (
        <EmptyState icon={Boxes} title={t("Todavía no hay pools ZFS")} action={<Button variant="primary" onClick={() => setCreating(true)}>{t("Crear pool ZFS…")}</Button>}>
          {t("Un pool junta discos en espejo o RAIDZ; adentro creás volúmenes, con cifrado si querés.")}
        </EmptyState>
      ) : (
        st.pools.map((p) => <ZfsPoolCard key={p.id} p={p} disks={disks} vols={vols} />)
      )}
      <ZfsWizard open={creating} onClose={() => setCreating(false)} disks={disks} vols={vols} pools={st.pools} />
    </div>
  );
}

function EnableZfs({ enabling, job }: { enabling?: string; job?: { progress: number; detail: string; state: string } }) {
  const rpc = useTransport();
  const [busy, setBusy] = useState(false);
  const enable = async () => {
    setBusy(true);
    try {
      await rpc.call("storage.zfs.enable", undefined);
    } catch (e) {
      toast({ level: "warn", title: t("No se pudo habilitar ZFS"), body: errText(e) });
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
          <p className="tg-muted">{t("Una alternativa a los grupos de almacenamiento: espejos y RAIDZ con checksums de punta a punta, instantáneas instantáneas, compresión y cifrado nativo.")}</p>
        </div>
      </div>
      <ul className="ts-zfs-points">
        <li>
          <Cpu size={14} /> {t("Se instala a pedido: OpenZFS se compila para el kernel de este equipo (DKMS). Tarda unos minutos y ocupa unos 300 MB.")}
        </li>
        <li>
          <ShieldCheck size={14} /> {t("Licencia: OpenZFS se distribuye con la licencia CDDL, compatible con el código abierto pero no con la GPL del kernel. Por eso no viene incluido: se descarga de Debian (componente contrib) en este equipo.")}
        </li>
        <li>
          <RefreshCw size={14} /> {t("Con cada kernel nuevo, DKMS lo vuelve a compilar solo.")}
        </li>
      </ul>
      {enabling ? (
        <div className="ts-wiz-progress">
          <Progress value={job?.progress ?? 0} />
          <span className="tg-num">{Math.round((job?.progress ?? 0) * 100)} %</span>
          <span className="tg-muted ts-zfs-stage">{job?.detail ? tr(job.detail) : t("Preparando…")}</span>
        </div>
      ) : (
        <div className="ts-row-actions">
          <Button variant="primary" disabled={busy} onClick={enable}>
            <Download size={14} /> {t("Habilitar ZFS")}
          </Button>
        </div>
      )}
    </section>
  );
}

// ---- Tarjeta de un pool -----------------------------------------------------------------------

type PoolSheet = "volume" | "data" | "cache" | "log" | "replace" | "zvol" | "destroy" | "export";

function ZfsPoolCard({ p, disks, vols }: { p: ZfsPool; disks: Disk[]; vols: Volume[] }) {
  const rpc = useTransport();
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
    rpc.call("storage.zfs.scrub", { pool: p.id }).then(
      () => toast({ level: "info", title: t("Verificando {name}", { name: p.name }), body: t("ZFS lee todos los bloques y los compara con sus checksums.") }),
      (e) => toast({ level: "warn", title: t("No se pudo verificar"), body: errText(e) }),
    );
  const setSched = (enabled: boolean, days: number) =>
    rpc.call("storage.zfs.scrubSchedule.set", { pool: p.id, enabled, days }).catch((e) => toast({ level: "warn", title: t("No se pudo guardar"), body: errText(e) }));
  const removeAux = (kind: "cache" | "log", vdev: string) =>
    rpc.call("storage.zfs.auxRemove", { pool: p.id, kind, vdev }).catch((e) => toast({ level: "warn", title: t("No se pudo quitar"), body: errText(e) }));
  const deleteZvol = (name: string) => rpc.call("storage.zfs.zvols.delete", { pool: p.id, name }).catch((e) => toast({ level: "warn", title: t("No se pudo borrar"), body: errText(e) }));

  return (
    <section className="tg-card ts-zfs-pool" aria-label={t("Pool {name}", { name: p.name })}>
      <div className="ts-pool-head">
        <Boxes size={18} />
        <h3>{p.name}</h3>
        <Badge>{zfsLayoutLabel(p.layout)}</Badge>
        {p.readonly && <Badge tone="warn">{t("Solo lectura")}</Badge>}
        <Status tone={s.tone}>{s.text}</Status>
        <span className="ts-push tg-num tg-muted">
          {t("{used} usados de {size}", { used: fmtBytes(p.allocated), size: fmtBytes(p.size) })}
        </span>
      </div>
      {scanning && (
        <div className="ts-vol-progress" style={{ marginTop: 8 }}>
          <Progress value={p.scan?.progress ?? 0} tone={p.scan?.function === "resilver" ? "warn" : undefined} />
          <span className="tg-num tg-muted">{Math.round((p.scan?.progress ?? 0) * 100)} %</span>
        </div>
      )}
      <div className="ts-alloc" aria-label={t("Usado {used}, libre {free}", { used: fmtBytes(p.allocated), free: fmtBytes(p.free) })}>
        {pvols.map((v) => (
          <div key={v.id} style={{ flex: Math.max(v.used, p.size * 0.004), background: volumeColor(v.id, vols) }} title={`${v.name} · ${fmtBytes(v.used)}`} />
        ))}
        {zvolBytes > 0 && <div style={{ flex: zvolBytes, background: "var(--ts-zvol)" }} title={t("Volúmenes de bloque · {size}", { size: fmtBytes(zvolBytes) })} />}
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
            <i style={{ background: "var(--ts-zvol)" }} /> {t("Bloque (iSCSI)")} <b>{fmtBytes(zvolBytes)}</b>
          </span>
        )}
        <span>
          <i style={{ background: "var(--tg-track)" }} /> {t("Libre")} <b>{fmtBytes(p.free)}</b>
        </span>
      </div>

      <div className="ts-card-title" style={{ marginTop: 16 }}>
        {t("Vdevs y discos")}
      </div>
      <VdevMap p={p} disks={disks} onReplace={(l) => (setReplaceOld(l), setSheet("replace"))} onRemove={removeAux} />

      <div className="ts-zfs-scan">
        <ShieldCheck size={14} />
        <span>
          {scanning
            ? p.scan?.function === "resilver"
              ? t("Reconstruyendo los datos en el disco nuevo…")
              : t("Verificando los checksums de todos los bloques…")
            : p.scan?.end
              ? `${p.scan.function === "scrub" ? t("Última verificación {ago}", { ago: fmtAgo(p.scan.end) }) : t("Última reconstrucción {ago}", { ago: fmtAgo(p.scan.end) })} · ${p.scan.errors ? tn(p.scan.errors, "{n} error", "{n} errores") : t("sin errores")}`
              : t("Nunca se verificó")}
        </span>
        <span className="ts-push" />
        {sched && (
          <label className="ts-zfs-sched">
            <Toggle checked={sched.enabled} onChange={(on) => setSched(on, sched.days)} label={t("Verificación automática")} />
            {t("Verificar cada")}
            <select value={sched.days} disabled={!sched.enabled} onChange={(e) => setSched(true, Number(e.target.value))} aria-label={t("Días entre verificaciones")}>
              {[7, 14, 30, 60, 90].map((d) => (
                <option key={d} value={d}>
                  {t("{n} días", { n: d })}
                </option>
              ))}
            </select>
          </label>
        )}
        {scanning ? (
          p.scan?.function === "scrub" && (
            <Button variant="plain" onClick={() => rpc.call("storage.zfs.scrub", { pool: p.id, stop: true })}>
              {t("Detener")}
            </Button>
          )
        ) : (
          <Button disabled={p.readonly || p.state !== "healthy"} onClick={scrub}>
            {t("Verificar ahora")}
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
                {v.mount || t("sin montar")} · {t("{size} usados", { size: fmtBytes(v.used) })}
                {v.locked ? ` · ${t("bloqueado")}` : v.encrypted ? ` · ${t("cifrado (AES-256-GCM)")}` : ""}
              </span>
            </div>
          ))}
          {p.zvols.map((z) => (
            <div key={z.name} className="ts-pool-vol">
              <HardDrive size={14} />
              <strong>{z.name}</strong>
              <span className="tg-muted">
                {t("Volumen de bloque")} · {fmtBytes(z.size)} · {z.device}
              </span>
              <span className="ts-push">
                <Button variant="plain" onClick={() => deleteZvol(z.name)} aria-label={t("Borrar {name}", { name: z.name })}>
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
                {o.mounted ? o.mountpoint : t("sin montar")} · {fmtBytes(o.used)} · {t("de otro equipo")}
              </span>
            </div>
          ))}
        </div>
      )}

      <div className="ts-row-actions">
        <Button variant="primary" disabled={p.readonly} onClick={() => setSheet("volume")}>
          <Plus size={14} /> {t("Crear volumen…")}
        </Button>
        {bad && (
          <Button variant="primary" onClick={() => (setReplaceOld(bad), setSheet("replace"))}>
            <RefreshCw size={14} /> {t("Reemplazar disco…")}
          </Button>
        )}
        <Button disabled={p.readonly || p.layout === "mixed" || p.state !== "healthy"} onClick={() => setSheet("data")} title={t("Sumar un vdev con la misma forma")}>
          <Plus size={14} /> {t("Sumar discos…")}
        </Button>
        <Button disabled={p.readonly} onClick={() => setSheet("cache")}>
          <Zap size={14} /> {t("Caché o registro SSD…")}
        </Button>
        <Button disabled={p.readonly} onClick={() => setSheet("zvol")}>
          <HardDrive size={14} /> {t("Volumen de bloque…")}
        </Button>
        <Button onClick={() => setSheet("export")}>
          <Upload size={14} /> {t("Exportar…")}
        </Button>
        <Button variant="danger" onClick={() => setSheet("destroy")}>
          <Trash2 size={14} /> {t("Destruir…")}
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
        <small>{v ? (v.type === "disk" ? t("Disco suelto") : t("{layout} · tolera {n}", { layout: zfsLayoutLabel(v.type), n: zfsTolerance(v.type as ZfsLayout, v.disks.length) })) : kind === "cache" ? t("Caché de lectura") : ""}</small>
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
                <b>{d ? diskLabel(d) : l.state === "ONLINE" ? l.name : t("Disco que falta")}</b>
                <small>
                  {d ? fmtBytes(d.size) : `GUID ${l.guid}`} · {l.resilvering ? t("recibiendo datos") : l.state === "ONLINE" ? t("en línea") : l.state.toLowerCase()}
                  {errs > 0 && ` · L${l.read} E${l.write} C${l.cksum}`}
                </small>
              </span>
            </button>
          );
        })}
      </div>
      {kind !== "data" && (
        <Button variant="plain" onClick={() => onRemove(kind === "cache" ? "cache" : "log", v?.name ?? leaves[0].name)} aria-label={t("Quitar")}>
          <Trash2 size={13} />
        </Button>
      )}
    </div>
  );
  return (
    <div className="ts-vdevs">
      {p.vdevs.map((v, i) => row(v, "data", v.disks, p.vdevs.length > 1 ? `Vdev ${i + 1}` : t("Datos")))}
      {p.logs.map((v) => row(v, "log", v.disks, t("Registro (SLOG)")))}
      {p.cache.map((l) => row(null, "cache", [l], t("Caché (L2ARC)")))}
    </div>
  );
}

// ---- Importar ---------------------------------------------------------------------------------

function ImportList({ pools, disks, onDone }: { pools: ZfsImportable[]; disks: Disk[]; onDone: () => void }) {
  const rpc = useTransport();
  const imp = (p: ZfsImportable, readonly: boolean) =>
    rpc.call("storage.zfs.import", { pool: p.guid || p.name, readonly }).then(
      () => {
        toast({ level: "info", title: t("Importando {name}", { name: p.name }), body: readonly ? t("Queda en solo lectura en /import/{name}: nada se escribe en los discos.", { name: p.name }) : t("Sus volúmenes se montan al terminar.") });
        onDone();
      },
      (e) => toast({ level: "warn", title: t("No se pudo importar"), body: errText(e) }),
    );
  if (!pools.length)
    return (
      <section className="tg-card">
        <p className="tg-muted">{t("No se encontraron pools ZFS sin importar en los discos conectados.")}</p>
      </section>
    );
  return (
    <section className="tg-card">
      <div className="ts-card-title">{t("Pools encontrados en los discos")}</div>
      {pools.map((p) => (
        <div key={p.guid} className="ts-zfs-import">
          <Boxes size={18} />
          <div>
            <strong>{p.name}</strong>
            <small className="tg-muted">
              {p.state} · {p.disks.map((id) => disks.find((d) => d.id === id)).filter(Boolean).map((d) => diskLabel(d!)).join(", ") || tn(p.disks.length, "{n} disco", "{n} discos")}
              {p.status ? ` · ${tr(p.status)}` : ""}
            </small>
          </div>
          <span className="ts-push" />
          <Button onClick={() => imp(p, true)}>{t("Ver en solo lectura")}</Button>
          <Button variant="primary" onClick={() => imp(p, false)}>
            {t("Importar")}
          </Button>
        </div>
      ))}
      <p className="ts-note">{t("«Ver en solo lectura» es lo más seguro con un pool de otro equipo: se monta en /import y nada se escribe en sus discos.")}</p>
    </section>
  );
}

// ---- Asistente: crear un pool -------------------------------------------------------------------

const STEPS = [t("Discos"), t("Forma"), t("Nombre"), t("Revisión")];

/** Diagrama: cada disco partido en datos, redundancia y lo que no se usa. */
export function ZfsDiagram({ layout, disks }: { layout: ZfsLayout; disks: { id: string; label: string; size: number }[] }) {
  const split = zfsSplit(layout, disks.map((d) => d.size));
  const max = Math.max(...disks.map((d) => d.size), 1);
  return (
    <div className="ts-zfs-diagram" aria-label={t("Reparto de cada disco")}>
      {disks.map((d, i) => {
        const s = split[i];
        return (
          <div key={d.id} className="ts-zfs-diag-row">
            <small>{d.label}</small>
            <div className="ts-zfs-diag-bar" style={{ width: `${(d.size / max) * 100}%` }}>
              <i data-part="data" style={{ flex: s.data }} title={t("Datos · {size}", { size: fmtBytes(s.data) })} />
              {s.redundancy > 0 && <i data-part="red" style={{ flex: s.redundancy }} title={layout === "mirror" ? t("Copias · {size}", { size: fmtBytes(s.redundancy) }) : t("Paridad · {size}", { size: fmtBytes(s.redundancy) })} />}
              {s.unused > 0 && <i data-part="unused" style={{ flex: s.unused }} title={t("Sin usar · {size}", { size: fmtBytes(s.unused) })} />}
            </div>
            <small className="tg-num">{fmtBytes(d.size)}</small>
          </div>
        );
      })}
      <div className="ts-zfs-diag-legend">
        <span>
          <i data-part="data" /> {t("Datos")}
        </span>
        <span>
          <i data-part="red" /> {layout === "mirror" ? t("Copias") : t("Paridad")}
        </span>
        <span>
          <i data-part="unused" /> {t("Sin usar")}
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
      <p className="tg-muted">{pj.done ? doneBody : pj.failed ? `${t("La tarea falló.")} ${tr(pj.job?.detail)}` : pj.job?.detail ? tr(pj.job.detail) : body}</p>
      {!pj.done && !pj.failed && (
        <div className="ts-wiz-progress">
          <Progress value={pj.job?.progress ?? 0} />
          <span className="tg-num">{Math.round((pj.job?.progress ?? 0) * 100)} %</span>
        </div>
      )}
      <Button variant="primary" onClick={onClose}>
        {pj.done || pj.failed ? t("Cerrar") : t("Seguir en segundo plano")}
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
                {fmtBytes(d.size)} · {d.kind === "hdd" ? "HDD" : d.kind === "ssd" ? "SSD" : "NVMe"} · {t("serie {serial}", { serial: d.serial })}
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
          <strong>{t("Se van a borrar estos discos")}</strong>
          <p>{t("Todo lo que contienen se pierde. TangoOS vuelve a verificar la identidad de cada disco justo antes de empezar.")}</p>
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
                  {local?.bay ? t("Bahía {n}", { n: local.bay }) : t("Disco")} · {d.model}
                </strong>
                <small>
                  {t("Serie {serial} · {size}", { serial: d.serial, size: fmtBytes(d.size) })}
                </small>
                <small className="ts-erase-content">{t("Contenido: {content}", { content: tr(d.content) })}</small>
              </div>
            </div>
          );
        })}
      </div>
      {layout && <ZfsDiagram layout={layout} disks={plan.summary.disks.map((d) => ({ id: d.id, label: d.model, size: d.size }))} />}
      {plan.summary.notes.map((n) => (
        <p key={n} className="ts-note">
          {tr(n)}
        </p>
      ))}
      <div className="ts-summary">
        {plan.summary.kind !== "replace" && plan.summary.kind !== "cache" && plan.summary.kind !== "log" && (
          <span>
            <Boxes size={13} /> {zfsLayoutLabel(plan.summary.layout)} · ≈ {t("{size} útiles", { size: fmtBytes(plan.summary.usable) })}
          </span>
        )}
        {left !== null && (
          <span className="ts-plan-exp" data-soon={left < 60 || undefined}>
            <Clock size={12} /> {t("Este plan vence en {time}", { time: `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}` })}
          </span>
        )}
      </div>
      <label className="ts-field">
        <span>
          {t("Para confirmar, escribí")} <code>{plan.summary.name}</code>
        </span>
        <TextInput value={confirm} onChange={(e) => setConfirm(e.target.value)} aria-label={t("Escribí {name} para confirmar", { name: plan.summary.name })} autoComplete="off" />
      </label>
    </>
  );
}

function ZfsWizard({ open, onClose, disks, vols, pools }: { open: boolean; onClose: () => void; disks: Disk[]; vols: Volume[]; pools: ZfsPool[] }) {
  const rpc = useTransport();
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
      setPlan(await rpc.call("storage.zfs.plan", { name, layout, disks: sel }));
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
      const r = await rpc.call("storage.zfs.create", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm });
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
        <JobView title={t("Creando el pool {name}…", { name })} doneTitle={t("El pool {name} está listo", { name })} failTitle={t("No se pudo crear el pool {name}", { name })} doneBody={t("Creá un volumen dentro: es donde viven las carpetas compartidas.")} body={t("Se preparan los discos y se crea el pool.")} pj={pj} onClose={onClose} />
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
                <RotateCcw size={12} /> {t("Volver a revisar")}
              </Button>
            </span>
          )}
          <Button onClick={step === 0 ? onClose : () => setStep(step - 1)}>{step === 0 ? t("Cancelar") : t("Atrás")}</Button>
          {step < 3 ? (
            <Button variant="primary" disabled={!canNext} onClick={() => setStep(step + 1)}>
              {t("Continuar")}
            </Button>
          ) : (
            <Button variant="danger" disabled={!canNext || busy} onClick={create}>
              {t("Borrar discos y crear el pool")}
            </Button>
          )}
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">{t("Crear pool ZFS")}</h2>
        <ol className="ts-steps" aria-label={t("Pasos")}>
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
          <p className="tg-muted ts-wiz-lead">{t("Elegí los discos del pool. Conviene que sean del mismo tamaño: en un vdev, ZFS usa en cada disco lo que tiene el más chico.")}</p>
          <DiskPicker disks={disks} vols={vols} sel={sel} setSel={setSel} />
        </>
      )}
      {step === 1 && (
        <>
          <div className="ts-profiles ts-profiles-pool" role="radiogroup" aria-label={t("Forma")}>
            {ZFS_LAYOUTS.map((info) => {
              const why = zfsCheck(info.id, sel.length);
              return (
                <button key={info.id} type="button" role="radio" aria-checked={layout === info.id} className="ts-profile" disabled={!!why} onClick={() => setLayout(info.id)}>
                  <strong>{info.label}</strong>
                  <small>{info.description}</small>
                  <small className="ts-tolerance" data-none={info.id === "stripe" || undefined}>
                    {zfsTolerance(info.id, sel.length) === 0 ? t("No soporta la falla de ningún disco.") : tn(zfsTolerance(info.id, sel.length), "Soporta la falla de {n} disco.", "Soporta la falla de {n} discos.")}
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
                {t("Capacidad útil estimada:")} <b className="tg-num">≈ {fmtBytes(zfsUsable(layout, chosen.map((d) => d.size)) * 0.97)}</b> {t("(ZFS reserva alrededor de un 3 %). Compresión LZ4 activada: suele dar más lugar del que dice.")}
              </p>
            </>
          )}
        </>
      )}
      {step === 2 && (
        <div className="ts-form">
          <label className="ts-field">
            <span>{t("Nombre del pool")}</span>
            <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-label={t("Nombre del pool")} />
            <small>{nameOk || !name ? t("Minúsculas, números o _ (hasta 32). Los volúmenes del pool llevan su propio nombre.") : t("Ese nombre no está disponible o no es válido.")}</small>
          </label>
        </div>
      )}
      {step === 3 && (plan ? <PlanReview plan={plan} disks={disks} confirm={confirm} setConfirm={setConfirm} layout={layout} /> : !error && <p className="tg-muted ts-wiz-lead">{t("Armando el plan con el estado actual de los discos…")}</p>)}
    </Sheet>
  );
}

// ---- Sumar vdev / caché / registro ---------------------------------------------------------------

function ZfsAddSheet({ open, initial, pool, disks, vols, onClose }: { open: boolean; initial: "data" | "cache"; pool: ZfsPool; disks: Disk[]; vols: Volume[]; onClose: () => void }) {
  const rpc = useTransport();
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
      setPlan(await rpc.call("storage.zfs.addPlan", { pool: pool.id, kind, disks: sel }));
    } catch (e) {
      setError(errText(e));
    }
  };
  const go = async () => {
    if (!plan) return;
    try {
      pj.setJobId((await rpc.call("storage.zfs.add", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm })).job);
    } catch (e) {
      setError(errText(e));
    }
  };
  if (pj.jobId)
    return (
      <Sheet open={open} onClose={onClose} width={520}>
        <JobView title={t("Ampliando {name}…", { name: pool.name })} doneTitle={t("Listo: {name}", { name: pool.name })} failTitle={t("No se pudo ampliar el pool")} doneBody={kind === "data" ? t("El vdev nuevo ya suma espacio.") : t("El SSD ya está en uso.")} body="" pj={pj} onClose={onClose} />
      </Sheet>
    );
  const need = kind === "data" ? zfsCheck(pool.layout === "mixed" ? "stripe" : pool.layout, sel.length) : sel.length < 1 || sel.length > 2 ? t("Elegí uno o dos SSD") : null;
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
          <Button onClick={plan ? () => setPlan(null) : onClose}>{plan ? t("Atrás") : t("Cancelar")}</Button>
          {plan ? (
            <Button variant="danger" disabled={confirm !== pool.name} onClick={go}>
              {t("Borrar discos y sumarlos")}
            </Button>
          ) : (
            <Button variant="primary" disabled={!!need} onClick={review}>
              {t("Revisar")}
            </Button>
          )}
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">{t("Sumar a {name}", { name: pool.name })}</h2>
      </div>
      {plan ? (
        <PlanReview plan={plan} disks={disks} confirm={confirm} setConfirm={setConfirm} layout={kind === "data" ? (pool.layout as ZfsLayout) : undefined} />
      ) : (
        <>
          <div className="ts-profiles ts-profiles-pool" role="radiogroup" aria-label={t("Qué sumar")}>
            {(
              [
                ["data", t("Más espacio"), t("Un vdev {layout} nuevo. No se puede quitar después.", { layout: zfsLayoutLabel(pool.layout) })],
                ["cache", t("Caché de lectura (L2ARC)"), t("Un SSD que guarda lo que más se lee. Si falla no se pierde nada.")],
                ["log", t("Registro de escritura (SLOG)"), t("Acelera escrituras sincrónicas (NFS, iSCSI, bases de datos). Mejor dos SSD en espejo.")],
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
  const rpc = useTransport();
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
      setPlan(await rpc.call("storage.zfs.replacePlan", { pool: pool.id, old: l?.id ? l.name : (l?.guid ?? target), replacement: sel[0] }));
    } catch (e) {
      setError(errText(e));
    }
  };
  const go = async () => {
    if (!plan) return;
    try {
      pj.setJobId((await rpc.call("storage.zfs.replace", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm })).job);
    } catch (e) {
      setError(errText(e));
    }
  };
  if (pj.jobId)
    return (
      <Sheet open={open} onClose={onClose} width={520}>
        <JobView title={t("Reconstruyendo (resilver)…")} doneTitle={t("{name} recuperó la redundancia", { name: pool.name })} failTitle={t("No se pudo reemplazar el disco")} doneBody={t("ZFS copió los datos al disco nuevo.")} body={t("ZFS copia solo los bloques usados: suele ser mucho más rápido que un RAID.")} pj={pj} onClose={onClose} />
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
          <Button onClick={plan ? () => setPlan(null) : onClose}>{plan ? t("Atrás") : t("Cancelar")}</Button>
          {plan ? (
            <Button variant="danger" disabled={confirm !== pool.name} onClick={go}>
              {t("Reemplazar")}
            </Button>
          ) : (
            <Button variant="primary" disabled={!target || sel.length !== 1} onClick={review}>
              {t("Revisar")}
            </Button>
          )}
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">{t("Reemplazar un disco de {name}", { name: pool.name })}</h2>
      </div>
      {plan ? (
        <PlanReview plan={plan} disks={disks} confirm={confirm} setConfirm={setConfirm} />
      ) : (
        <>
          <label className="ts-field">
            <span>{t("Disco a reemplazar")}</span>
            <select className="tg-input" value={target} onChange={(e) => setTarget(e.target.value)} aria-label={t("Disco a reemplazar")}>
              {leaves.map((l) => {
                const d = disks.find((x) => x.id === l.id);
                return (
                  <option key={l.guid} value={l.guid}>
                    {d ? diskLabel(d) : t("Disco que falta ({guid})", { guid: l.guid })} · {l.state.toLowerCase()}
                  </option>
                );
              })}
            </select>
          </label>
          <p className="tg-muted ts-wiz-lead">{t("Elegí el disco nuevo (igual o más grande).")}</p>
          <DiskPicker disks={disks} vols={vols} sel={sel} setSel={(f) => setSel((s) => f(s).slice(-1))} />
        </>
      )}
    </Sheet>
  );
}

// ---- Volumen, zvol, exportar y destruir --------------------------------------------------------------

function ZfsVolumeSheet({ open, pool, vols, onClose }: { open: boolean; pool: ZfsPool; vols: Volume[]; onClose: () => void }) {
  const rpc = useTransport();
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
      pj.setJobId((await rpc.call("storage.zfs.volumes.create", { pool: pool.id, name, quota: q || undefined, encryption: enc ? { passphrase: pass } : null })).job);
      setPass("");
      setPass2("");
    } catch (e) {
      setError(errText(e));
    }
  };
  if (pj.jobId)
    return (
      <Sheet open={open} onClose={onClose} width={520}>
        <JobView title={t("Creando {name}…", { name })} doneTitle={t("{name} está listo", { name })} failTitle={t("No se pudo crear {name}", { name })} doneBody={t("Ya podés crear carpetas compartidas en él.")} body="" pj={pj} onClose={onClose} />
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
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={!ok} onClick={go}>
            {t("Crear volumen")}
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">{t("Volumen en {name}", { name: pool.name })}</h2>
      </div>
      <div className="ts-form">
        <label className="ts-field">
          <span>{t("Nombre")}</span>
          <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-label={t("Nombre del volumen")} />
          <small>{t("Queda en /{name}. Comparte el espacio libre del pool ({free}).", { name: name || "…", free: fmtBytes(pool.free) })}</small>
        </label>
        <label className="ts-field">
          <span>{t("Cuota (GB, opcional)")}</span>
          <TextInput value={quota} inputMode="decimal" placeholder={t("Sin límite")} onChange={(e) => setQuota(e.target.value.replace(",", "."))} aria-label={t("Cuota en GB")} />
        </label>
        <label className="ts-field ts-field-inline">
          <Toggle checked={enc} onChange={setEnc} label={t("Cifrar el volumen")} />
          <span>{t("Cifrar (AES-256-GCM, nativo de ZFS)")}</span>
        </label>
        {enc && (
          <>
            <label className="ts-field">
              <span>{t("Frase")}</span>
              <TextInput type="password" value={pass} onChange={(e) => setPass(e.target.value)} aria-label={t("Frase de cifrado")} autoComplete="new-password" />
              <small>{t("Al menos 8 caracteres. Sin la frase, los datos no se pueden recuperar: guardala en un lugar seguro.")}</small>
            </label>
            <label className="ts-field">
              <span>{t("Repetí la frase")}</span>
              <TextInput type="password" value={pass2} onChange={(e) => setPass2(e.target.value)} aria-label={t("Repetir la frase")} autoComplete="new-password" />
            </label>
          </>
        )}
      </div>
    </Sheet>
  );
}

function ZvolSheet({ open, pool, onClose }: { open: boolean; pool: ZfsPool; onClose: () => void }) {
  const rpc = useTransport();
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
      await rpc.call("storage.zfs.zvols.create", { pool: pool.id, name, size, sparse });
      toast({ level: "ok", title: t("Volumen de bloque {name} creado", { name }), body: t("Usalo como LUN en iSCSI.") });
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
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={!/^[a-z][a-z0-9_-]{0,31}$/.test(name) || !(Number(gb) >= 0.0625)} onClick={go}>
            {t("Crear")}
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">{t("Volumen de bloque en {name}", { name: pool.name })}</h2>
      </div>
      <p className="tg-muted ts-wiz-lead">{t("Un disco virtual (zvol) para usar como LUN de iSCSI: máquinas virtuales, servidores, Windows.")}</p>
      <div className="ts-form">
        <label className="ts-field">
          <span>{t("Nombre")}</span>
          <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-label={t("Nombre del volumen de bloque")} />
        </label>
        <label className="ts-field">
          <span>{t("Tamaño (GB)")}</span>
          <TextInput value={gb} inputMode="decimal" onChange={(e) => setGb(e.target.value.replace(",", "."))} aria-label={t("Tamaño en GB")} />
        </label>
        <label className="ts-field ts-field-inline">
          <Toggle checked={sparse} onChange={setSparse} label={t("Provisión fina")} />
          <span>{t("Provisión fina: ocupa a medida que se escribe")}</span>
        </label>
      </div>
    </Sheet>
  );
}

function ZfsDropSheet({ open, mode, pool, onClose }: { open: boolean; mode: "export" | "destroy"; pool: ZfsPool; onClose: () => void }) {
  const rpc = useTransport();
  const [plan, setPlan] = useState<DeletePlan | null>(null);
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!open) return;
    setPlan(null);
    setConfirm("");
    setError(null);
    if (mode === "destroy") rpc.call("storage.zfs.destroyPlan", { pool: pool.id }).then(setPlan, (e) => setError(errText(e)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, mode]);
  const go = async () => {
    setError(null);
    try {
      if (mode === "export") await rpc.call("storage.zfs.export", { pool: pool.id });
      else if (plan) await rpc.call("storage.zfs.destroy", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm });
      toast({ level: "info", title: mode === "export" ? t("Exportando {name}", { name: pool.name }) : t("Destruyendo {name}", { name: pool.name }), body: t("Podés ver el progreso en Actividad.") });
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
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant={mode === "destroy" ? "danger" : "primary"} disabled={confirm !== pool.name || (mode === "destroy" && !plan)} onClick={go}>
            {mode === "export" ? t("Exportar") : t("Destruir el pool")}
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">{mode === "export" ? t("Exportar {name}", { name: pool.name }) : t("Destruir {name}", { name: pool.name })}</h2>
      </div>
      {mode === "export" ? (
        <p className="tg-muted ts-wiz-lead">
          {t("Se desmontan sus {n} volúmenes y el pool queda listo para desconectar sus discos y llevarlos a otro equipo. Los datos no se tocan; podés volver a importarlo desde «Buscar pools para importar».", { n: vols })}
        </p>
      ) : (
        <div className="ts-danger">
          <AlertTriangle size={18} />
          <div>
            <strong>{t("Se borra todo lo que hay en el pool")}</strong>
            <p>
              {t("{n} volúmenes{shares}, {size} de datos. Los {disks} discos quedan libres.", { n: vols, shares: plan?.summary.shares.length ? ` (${plan.summary.shares.join(", ")})` : "", size: fmtBytes(plan?.summary.used ?? pool.allocated), disks: plan?.summary.disks.length ?? pool.disks.length })}
            </p>
          </div>
        </div>
      )}
      <label className="ts-field">
        <span>
          {t("Para confirmar, escribí")} <code>{pool.name}</code>
        </span>
        <TextInput value={confirm} onChange={(e) => setConfirm(e.target.value)} aria-label={t("Escribí {name} para confirmar", { name: pool.name })} autoComplete="off" />
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
          {p?.name ?? "?"}/{v.name} · {v.mount || t("/{name} (sin montar)", { name: v.name })}
        </strong>
      </div>
      {v.encrypted && (
        <div className="ts-layer">
          <small>{t("Cifrado")}</small>
          <strong>{t("Nativo de ZFS (AES-256-GCM)")} · {v.locked ? t("clave cerrada") : t("clave cargada")}</strong>
        </div>
      )}
      <div className="ts-layer">
        <small>Pool</small>
        <strong>
          {p?.name ?? "—"} · {p ? zfsLayoutLabel(p.layout) : ""} · {t("{free} libres", { free: fmtBytes(p?.free ?? 0) })} · {t("compresión LZ4")}
        </strong>
      </div>
    </div>
  );
}
