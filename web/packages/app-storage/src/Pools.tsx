import { useEffect, useState } from "react";
import { AlertTriangle, Check, Database, HardDrive, KeyRound, Layers, Lock, Maximize2, Plus, RefreshCw, Trash2, Zap } from "lucide-react";
import { RpcError, fmtBytes, poolProfileLabel, t, tr, useTransport } from "@tango/client";
import type { DeletePlan, Disk, Pool, PoolPlan, Volume } from "@tango/client";
import { Badge, Button, EmptyState, Progress, Sheet, Status, TextInput } from "@tango/ui";
import { toast } from "@tango/shell";
import { TierDiagram } from "./Tiers";
import { fsName, ineligibleReason, volumeColor } from "./util";

export function poolState(p: Pool): { tone: "ok" | "warn" | "crit" | "info"; text: string } {
  if (p.task === "rebuild") return { tone: "warn", text: t("Reconstruyendo: todavía sin redundancia") };
  if (p.task === "reshape") return { tone: "info", text: t("Ampliando") };
  if (p.task === "resync" || p.state === "building") return { tone: "info", text: t("Sincronizando") };
  switch (p.state) {
    case "degraded":
      return { tone: "crit", text: t("Sin redundancia") };
    case "readonly":
      return { tone: "warn", text: t("Solo lectura") };
    case "scrubbing":
      return { tone: "info", text: t("Verificando", undefined, "scrub") };
    default:
      return { tone: "ok", text: t("En orden") };
  }
}

const diskName = (d: Disk | undefined) => (d ? `${d.bay ? t("Bahía {n}", { n: d.bay }) : t("Interno")} · ${d.model}` : t("Disco"));

function PoolCard({ p, disks, vols, onCreateVolume, onExpand, onReplace, onDelete }: { p: Pool; disks: Disk[]; vols: Volume[]; onCreateVolume: () => void; onExpand: () => void; onReplace: () => void; onDelete: () => void }) {
  const s = poolState(p);
  const pvols = vols.filter((v) => v.pool === p.id);
  const members = disks.filter((d) => p.disks.includes(d.id));
  const cache = disks.filter((d) => p.cacheDisks.includes(d.id));
  const expandable = !["basic", "raid1"].includes(p.profile);
  const failed = members.some((d) => d.use === "failed") || p.state === "degraded";
  return (
    <section className="tg-card">
      <div className="ts-pool-head">
        <Layers size={18} />
        <h3>{p.name}</h3>
        <Badge>{poolProfileLabel(p.profile)}</Badge>
        <Status tone={s.tone}>{s.text}</Status>
        <span className="ts-push tg-num tg-muted">
          {t("{used} asignados de {size}", { used: fmtBytes(p.allocated), size: fmtBytes(p.size) })}
        </span>
      </div>
      {p.task && (
        <div className="ts-vol-progress" style={{ marginTop: 8 }}>
          <Progress value={p.progress ?? 0} tone={p.task === "rebuild" ? "warn" : undefined} />
          <span className="tg-num tg-muted">{Math.round((p.progress ?? 0) * 100)} %</span>
        </div>
      )}
      <div className="ts-alloc" aria-label={t("Asignado {used}, libre {free}", { used: fmtBytes(p.allocated), free: fmtBytes(p.free) })}>
        {pvols.map((v) => (
          <div key={v.id} style={{ flex: v.size, background: volumeColor(v.id, vols) }} title={`${v.name} · ${fmtBytes(v.size)}`} />
        ))}
        <div style={{ flex: Math.max(p.free, 0) }} />
      </div>
      <div className="ts-alloc-legend">
        {pvols.map((v) => (
          <span key={v.id}>
            <i style={{ background: volumeColor(v.id, vols) }} /> {v.name} <b>{fmtBytes(v.size)}</b>
          </span>
        ))}
        <span>
          <i style={{ background: "var(--tg-track)" }} /> {t("Libre")} <b>{fmtBytes(p.free)}</b>
        </span>
      </div>

      <div className="ts-card-title" style={{ marginTop: 16 }}>
        {t("Cómo se reparten los discos")}
      </div>
      <TierDiagram tiers={p.tiers} disks={members.map((d) => ({ id: d.id, label: diskName(d), size: d.size }))} height={110} />

      {pvols.length > 0 && (
        <div className="ts-pool-vols">
          {pvols.map((v) => (
            <div key={v.id} className="ts-pool-vol">
              {v.locked ? <Lock size={14} /> : v.encrypted ? <KeyRound size={14} /> : <Database size={14} />}
              <strong>{v.name}</strong>
              <span className="tg-muted">
                {fsName[v.fs] ?? v.fs} · {fmtBytes(v.size)}
                {v.locked ? ` · ${t("bloqueado")}` : v.encrypted ? ` · ${t("cifrado")}` : ""}
              </span>
              <span className="ts-push">
                {v.cache && (
                  <span className="ts-cache-chip">
                    <Zap size={11} /> {v.cache.mode === "read" ? t("Caché de lectura") : t("Caché lectura/escritura")}
                    {v.cache.hitRatio != null && ` · ${Math.round(v.cache.hitRatio * 100)} %`}
                  </span>
                )}
              </span>
            </div>
          ))}
        </div>
      )}
      {cache.length > 0 && (
        <p className="ts-note">
          <Zap size={12} /> {t("SSD de caché: {list}.", { list: cache.map((d) => `${d.model} (${fmtBytes(d.size)})`).join(", ") })}
        </p>
      )}
      <div className="ts-row-actions">
        <Button variant="primary" disabled={p.free < 256 * 1024 ** 2} title={p.free < 256 * 1024 ** 2 ? t("El grupo no tiene espacio libre") : undefined} onClick={onCreateVolume}>
          <Plus size={14} /> {t("Crear volumen…")}
        </Button>
        {failed && p.profile !== "basic" && (
          <Button variant="primary" onClick={onReplace}>
            <RefreshCw size={14} /> {t("Reemplazar disco…")}
          </Button>
        )}
        <Button disabled={!expandable || p.state !== "healthy" || !!p.task} title={!expandable ? t("Un grupo {profile} no gana espacio sumando discos", { profile: poolProfileLabel(p.profile) }) : p.state !== "healthy" || p.task ? t("Esperá a que el grupo esté en orden") : t("Sumar discos libres")} onClick={onExpand}>
          <Maximize2 size={14} /> {t("Ampliar grupo…")}
        </Button>
        <Button variant="danger" disabled={pvols.length > 0 || !!p.task} title={pvols.length ? t("Eliminá primero sus volúmenes") : undefined} onClick={onDelete}>
          <Trash2 size={14} /> {t("Eliminar grupo…")}
        </Button>
      </div>
    </section>
  );
}

type Maint = { kind: "expand" | "replace" | "delete"; pool: Pool } | null;

export function Pools({ pools, disks, vols, onCreatePool, onCreateVolume }: { pools: Pool[]; disks: Disk[]; vols: Volume[]; onCreatePool: () => void; onCreateVolume: (pool: string) => void }) {
  const [maint, setMaint] = useState<Maint>(null);
  if (!pools.length)
    return (
      <EmptyState icon={Layers} title={t("Todavía no hay grupos de almacenamiento")} action={<Button variant="primary" onClick={onCreatePool}>{t("Crear grupo…")}</Button>}>
        {t("Un grupo junta discos (aunque sean de distinto tamaño) con su protección. Adentro creás uno o más volúmenes, cifrados si querés, y le podés sumar una caché SSD.")}
      </EmptyState>
    );
  return (
    <div className="ts-stack">
      <PoolMaintenance m={maint} disks={disks} vols={vols} pools={pools} onClose={() => setMaint(null)} />
      {pools.map((p) => (
        <PoolCard
          key={p.id}
          p={p}
          disks={disks}
          vols={vols}
          onCreateVolume={() => onCreateVolume(p.id)}
          onExpand={() => setMaint({ kind: "expand", pool: p })}
          onReplace={() => setMaint({ kind: "replace", pool: p })}
          onDelete={() => setMaint({ kind: "delete", pool: p })}
        />
      ))}
    </div>
  );
}

const title = (kind: "expand" | "replace" | "delete", name: string) =>
  kind === "expand" ? t("Ampliar el grupo {name}", { name }) : kind === "replace" ? t("Reemplazar un disco del grupo {name}", { name }) : t("Eliminar el grupo {name}", { name });

/** Ampliar, reemplazar un disco o eliminar un grupo: elegir discos → plan → nombre escrito. */
function PoolMaintenance({ m, disks, vols, pools, onClose }: { m: Maint; disks: Disk[]; vols: Volume[]; pools: Pool[]; onClose: () => void }) {
  const rpc = useTransport();
  const [sel, setSel] = useState<string[]>([]);
  const [plan, setPlan] = useState<PoolPlan | DeletePlan | null>(null);
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setSel([]);
    setPlan(null);
    setConfirm("");
    setError(null);
    if (m?.kind === "delete") {
      rpc.call("storage.pools.deletePlan", { pool: m.pool.id }).then(setPlan, (e: Error) => setError(e.message));
    }
  }, [m, rpc]);
  if (!m) return null;
  const p = m.pool;
  const free = disks.filter((d) => d.use !== "system" && !ineligibleReason(d, vols, pools));
  const failed = disks.find((d) => d.pool === p.id && d.use === "failed");
  const review = async () => {
    setBusy(true);
    setError(null);
    try {
      setPlan(
        m.kind === "expand"
          ? await rpc.call("storage.pools.expandPlan", { pool: p.id, add: sel })
          : await rpc.call("storage.pools.replacePlan", { pool: p.id, failed: failed?.id ?? null, replacement: sel[0] }),
      );
    } catch (e) {
      setError(e instanceof RpcError ? e.message : (e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const go = async () => {
    if (!plan) return;
    setBusy(true);
    setError(null);
    try {
      const params = { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm };
      if (m.kind === "expand") await rpc.call("storage.pools.expand", params);
      else if (m.kind === "replace") await rpc.call("storage.pools.replace", params);
      else await rpc.call("storage.pools.delete", params);
      toast({ level: "info", title: title(m.kind, p.name), body: t("Podés seguir el progreso en Actividad.") });
      onClose();
    } catch (e) {
      setError(e instanceof RpcError ? e.message : (e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const poolPlan = plan && "tiers" in plan.summary ? (plan as PoolPlan) : null;
  const pickMany = m.kind === "expand";
  return (
    <Sheet
      open={!!m}
      onClose={onClose}
      width={600}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={plan && m.kind !== "delete" ? () => setPlan(null) : onClose}>{plan && m.kind !== "delete" ? t("Atrás") : t("Cancelar")}</Button>
          {plan ? (
            <Button variant="danger" disabled={busy || confirm !== p.name} onClick={go}>
              {m.kind === "delete" ? t("Eliminar el grupo") : m.kind === "expand" ? t("Borrar discos y ampliar") : t("Borrar disco y reconstruir")}
            </Button>
          ) : (
            m.kind !== "delete" && (
              <Button variant="primary" disabled={busy || !sel.length} onClick={review}>
                {t("Continuar")}
              </Button>
            )
          )}
        </>
      }
    >
      <h2 className="tg-h2">
        {title(m.kind, p.name)}
      </h2>
      {!plan && m.kind !== "delete" && (
        <>
          <p className="tg-muted ts-wiz-lead">
            {m.kind === "expand"
              ? p.profile.startsWith("hybrid")
                ? t("Sumá discos iguales o más grandes que el mayor del grupo: cada tramo gana un miembro y el espacio reservado pasa a tener redundancia.")
                : t("Los discos nuevos se suman al RAID y se reorganiza (reshape).")
              : failed
                ? t("{disk} falló. Elegí el disco nuevo: tiene que ser igual o más grande.", { disk: diskName(failed) })
                : t("Elegí el disco nuevo.")}
          </p>
          <div className="ts-pick">
            {free.length === 0 && <small className="tg-muted">{t("No hay discos libres.")}</small>}
            {free.map((d) => {
              const on = sel.includes(d.id);
              return (
                <button
                  key={d.id}
                  type="button"
                  className="ts-pick-disk"
                  aria-pressed={on}
                  onClick={() => setSel((s) => (pickMany ? (on ? s.filter((x) => x !== d.id) : [...s, d.id]) : on ? [] : [d.id]))}
                >
                  <span className="ts-pick-check">{on && <Check size={12} strokeWidth={3} />}</span>
                  <HardDrive size={22} strokeWidth={1.5} />
                  <span className="ts-pick-text">
                    <strong>{diskName(d)}</strong>
                    <small>
                      {fmtBytes(d.size)} · {t("serie {serial}", { serial: d.serial })}
                    </small>
                  </span>
                </button>
              );
            })}
          </div>
        </>
      )}
      {plan && (
        <>
          {m.kind !== "delete" ? (
            <div className="ts-danger">
              <AlertTriangle size={18} />
              <div>
                <strong>{t("Se van a borrar estos discos")}</strong>
                <p>{t("Todo lo que contienen se pierde. Los volúmenes del grupo siguen disponibles durante la operación.")}</p>
              </div>
            </div>
          ) : (
            <div className="ts-danger">
              <AlertTriangle size={18} />
              <div>
                <strong>{t("El grupo y sus discos se borran")}</strong>
                <p>{t("Los discos quedan libres, sin datos.")}</p>
              </div>
            </div>
          )}
          <div className="ts-erase">
            {plan.summary.disks.map((d) => (
              <div key={d.id} className="ts-erase-row">
                <HardDrive size={18} strokeWidth={1.6} />
                <div>
                  <strong>{d.model}</strong>
                  <small>{t("Serie {serial}", { serial: d.serial })}</small>
                </div>
              </div>
            ))}
          </div>
          {poolPlan && poolPlan.summary.tiers.length > 0 && m.kind === "expand" && (
            <TierDiagram tiers={poolPlan.summary.tiers} disks={poolPlan.summary.tiers.flatMap((tier) => tier.members).filter((x, i, a) => a.indexOf(x) === i).map((id) => {
              const d = disks.find((x) => x.id === id);
              return { id, label: diskName(d), size: d?.size ?? 0 };
            })} height={96} />
          )}
          {poolPlan?.summary.notes?.map((n) => (
            <p key={n} className="ts-note">
              {tr(n)}
            </p>
          ))}
          <label className="ts-field">
            <span>
              {t("Para confirmar, escribí")} <code>{p.name}</code>
            </span>
            <TextInput value={confirm} onChange={(e) => setConfirm(e.target.value)} aria-label={t("Escribí {name} para confirmar", { name: p.name })} autoComplete="off" />
          </label>
        </>
      )}
    </Sheet>
  );
}
