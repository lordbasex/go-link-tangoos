// Reemplazar un disco y ampliar un volumen (protocol/README.md §11).
// Mismo esquema que crear/eliminar: plan del servidor → escribir el nombre → job con progreso en vivo.
import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, ArrowRight, CheckCircle2, Clock, HardDrive, Info, Maximize2, RefreshCw, RotateCcw, XOctagon } from "lucide-react";
import { RpcError, fmtBytes, fmtDuration, fmtRate, profileInfo, profileLabel, useJobs, useLive, useTransport } from "@tango/client";
import type { Disk, MaintenancePlan, Volume } from "@tango/client";
import { Button, Progress, Sheet, TextInput } from "@tango/ui";
import { toast } from "@tango/shell";

const errorText: Record<string, string> = {
  PLAN_EXPIRED: "El plan venció. Lo volvemos a armar con el estado actual de los discos.",
  CONFIRMATION_MISMATCH: "La confirmación no coincide. Escribí el nombre exacto del volumen.",
  DEVICE_CHANGED: "Un disco cambió desde que armaste el plan. Volvé a revisarlo.",
  DEVICE_NOT_ELIGIBLE: "El disco elegido ya no está libre. Elegí otro.",
  DEVICE_TOO_SMALL: "El disco es más chico que los del volumen. Elegí uno igual o más grande.",
  VOLUME_BUSY: "El volumen tiene una tarea en curso. Esperá a que termine.",
  VOLUME_NOT_FOUND: "El volumen ya no existe.",
  NOT_SUPPORTED: "Este TangoOS todavía no permite esta operación.",
};

const errOf = (e: unknown) => {
  const code = e instanceof RpcError ? e.code : "INTERNAL";
  return { code, text: errorText[code] ?? (e as Error).message };
};

/** Tamaño de referencia del array: el menor de los miembros sanos. */
export function memberSize(v: Volume, disks: Disk[]): number {
  const m = disks.filter((d) => (v.disks.includes(d.id) || d.volume === v.id) && d.use !== "failed");
  return m.length ? Math.min(...m.map((d) => d.size)) : 0;
}

/** Texto de la tarea larga del array: "Reconstruyendo · 42 % · 152 MB/s · faltan 12 min". */
export function taskLine(v: Volume): string | null {
  if (!v.task) return null;
  const label = v.task === "rebuild" ? "Reconstruyendo" : v.task === "reshape" ? "Ampliando" : "Sincronizando";
  const parts = [label, `${Math.round((v.progress ?? 0) * 100)} %`];
  if (v.taskSpeed) parts.push(fmtRate(v.taskSpeed));
  if (v.taskEtaSec != null && v.taskEtaSec > 0) parts.push(`faltan ${fmtDuration(v.taskEtaSec)}`);
  return parts.join(" · ");
}

function useCountdown(expires: string | undefined) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!expires) return;
    const i = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(i);
  }, [expires]);
  return expires ? Math.max(0, Math.round((new Date(expires).getTime() - now) / 1000)) : null;
}

/** Estado común de un job de mantenimiento: corriendo / terminó / falló. */
function useJobState(jobId: string | null) {
  const jobs = useJobs();
  const [seen, setSeen] = useState(false);
  const job = jobs.find((j) => j.id === jobId);
  useEffect(() => {
    if (job) setSeen(true);
  }, [job]);
  useEffect(() => setSeen(false), [jobId]);
  const failed = !!job && (job.state === "failed" || job.state === "interrupted" || job.state === "cancelled");
  const done = !!jobId && !failed && ((job && job.state === "succeeded") || (seen && !job));
  return { job, failed, done };
}

function PickDisk({ d, pressed, reason, onToggle, multi }: { d: Disk; pressed: boolean; reason: string | null; onToggle: () => void; multi?: boolean }) {
  return (
    <button type="button" className="ts-pick-disk" aria-pressed={pressed} disabled={!!reason} onClick={onToggle}>
      <span className="ts-pick-check" data-radio={!multi || undefined}>{pressed && <CheckCircle2 size={12} strokeWidth={3} />}</span>
      <HardDrive size={18} strokeWidth={1.6} />
      <span className="ts-pick-text">
        <strong>
          {d.bay ? `Bahía ${d.bay}` : "Disco interno"} · {d.model}
        </strong>
        <small>
          {fmtBytes(d.size)} · serie {d.serial}
        </small>
        {reason && <em>{reason}</em>}
      </span>
    </button>
  );
}

function PlanFooter({ error, onReplan, onCancel, children }: { error: { code: string; text: string } | null; onReplan: () => void; onCancel: () => void; children: React.ReactNode }) {
  return (
    <>
      {error && (
        <span className="ts-wiz-error" role="alert">
          <AlertTriangle size={14} /> {error.text}
          {(error.code === "PLAN_EXPIRED" || error.code === "DEVICE_CHANGED") && (
            <Button variant="plain" onClick={onReplan}>
              <RotateCcw size={12} /> Volver a revisar
            </Button>
          )}
        </span>
      )}
      <Button onClick={onCancel}>Cancelar</Button>
      {children}
    </>
  );
}

function Expiry({ left }: { left: number | null }) {
  if (left === null) return null;
  return (
    <span className="ts-plan-exp" data-soon={left < 60 || undefined}>
      <Clock size={12} /> Este plan vence en {Math.floor(left / 60)}:{String(left % 60).padStart(2, "0")}
    </span>
  );
}

function EraseList({ plan, disks, verb }: { plan: MaintenancePlan; disks: Disk[]; verb: string }) {
  return (
    <div className="ts-erase">
      {plan.summary.disks.map((d) => {
        const local = disks.find((x) => x.id === d.id);
        return (
          <div key={d.id} className="ts-erase-row">
            <HardDrive size={18} strokeWidth={1.6} />
            <div>
              <strong>
                {local?.bay ? `Bahía ${local.bay}` : "Disco"} · {d.model} · {fmtBytes(d.size)}
              </strong>
              <small>
                Serie {d.serial} · {verb}. Contenido: {d.content}
              </small>
            </div>
          </div>
        );
      })}
    </div>
  );
}

/** Vista de progreso: lee el volumen en vivo para mostrar %, velocidad y tiempo restante. */
function Running({ volId, title, doneTitle, failTitle, body, doneBody, jobId, onClose }: { volId: string; title: string; doneTitle: string; failTitle: string; body: string; doneBody: string; jobId: string; onClose: () => void }) {
  const vols = useLive("storage.volumes.list", undefined, ["storage.changed"]).data ?? [];
  const v = vols.find((x) => x.id === volId);
  const { job, failed, done } = useJobState(jobId);
  const progress = v?.task ? (v.progress ?? 0) : (job?.progress ?? (done ? 1 : 0));
  const line = v ? taskLine(v) : null;
  return (
    <Sheet open onClose={onClose} width={520} footer={<Button variant="primary" onClick={onClose}>{done || failed ? "Cerrar" : "Seguir en segundo plano"}</Button>}>
      <div className="ts-wiz-done">
        {done ? <CheckCircle2 size={44} className="ts-ok-icon" /> : failed ? <XOctagon size={44} className="ts-crit-icon" /> : <RefreshCw size={40} className="ts-muted-icon ts-spin-slow" />}
        <h2 className="tg-h2">{done ? doneTitle : failed ? failTitle : title}</h2>
        <p className="tg-muted">{done ? doneBody : failed ? `${job?.detail ?? ""} Revisá el estado del volumen en Volúmenes.` : body}</p>
        {!done && !failed && (
          <>
            <div className="ts-wiz-progress">
              <Progress value={progress} />
              <span className="tg-num">{Math.round(progress * 100)} %</span>
            </div>
            {line && <small className="tg-muted tg-num ts-task-line">{line}</small>}
          </>
        )}
      </div>
    </Sheet>
  );
}

// ---- Reemplazar disco ---------------------------------------------------------------

export function ReplaceDisk({ vol, disks, onClose }: { vol: Volume | null; disks: Disk[]; onClose: () => void }) {
  const t = useTransport();
  const [pick, setPick] = useState<string | null>(null);
  const [plan, setPlan] = useState<MaintenancePlan | null>(null);
  const [typed, setTyped] = useState("");
  const [error, setError] = useState<{ code: string; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [jobId, setJobId] = useState<string | null>(null);
  const left = useCountdown(plan?.expires);

  const need = vol ? memberSize(vol, disks) : 0;
  const failed = vol ? disks.find((d) => d.volume === vol.id && d.use === "failed") ?? null : null;
  const candidates = useMemo(() => disks.filter((d) => d.use !== "system" && d.use !== "failed" && d.volume !== vol?.id), [disks, vol]);
  const reasonOf = (d: Disk) => (d.use !== "free" ? "En uso por otro volumen" : d.foreign ? "Viene de un Synology" : d.size < need ? `Más chico que los discos del volumen (${fmtBytes(need)})` : null);

  useEffect(() => {
    setPick(null);
    setPlan(null);
    setTyped("");
    setError(null);
    setJobId(null);
    setBusy(false);
  }, [vol?.id]);

  const makePlan = useCallback(async () => {
    if (!vol || !pick) return;
    setBusy(true);
    setError(null);
    setTyped("");
    try {
      setPlan(await t.call("storage.disks.replacePlan", { volume: vol.id, failed: failed?.id ?? null, replacement: pick }));
    } catch (e) {
      setPlan(null);
      setError(errOf(e));
    } finally {
      setBusy(false);
    }
  }, [t, vol, pick, failed?.id]);

  useEffect(() => {
    if (left === 0 && plan && !busy && !jobId) void makePlan();
  }, [left, plan, busy, jobId, makePlan]);

  const run = async () => {
    if (!plan) return;
    setBusy(true);
    setError(null);
    try {
      const r = await t.call("storage.disks.replace", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: typed });
      setJobId(r.job);
    } catch (e) {
      const err = errOf(e);
      setError(err);
      if (err.code === "PLAN_EXPIRED" || err.code === "DEVICE_CHANGED") void makePlan();
      if (err.code === "DEVICE_NOT_ELIGIBLE" || err.code === "DEVICE_TOO_SMALL") setPlan(null);
    } finally {
      setBusy(false);
    }
  };

  const close = () => {
    if (jobId) toast({ level: "info", title: `${vol?.name} se sigue reconstruyendo`, body: "Podés usar el volumen mientras tanto. El progreso está en Almacenamiento y en Actividad." });
    onClose();
  };

  if (!vol) return null;

  if (jobId)
    return (
      <Running
        volId={vol.id}
        jobId={jobId}
        onClose={close}
        title={`Reconstruyendo ${vol.name}…`}
        doneTitle={`${vol.name} recuperó la redundancia`}
        failTitle={`No se pudo reconstruir ${vol.name}`}
        body="Se copian los datos al disco nuevo. Podés seguir usando el volumen; va un poco más lento hasta que termine."
        doneBody={`${profileLabel(vol.profile)} vuelve a soportar la falla de un disco.`}
      />
    );

  const picked = disks.find((d) => d.id === pick);
  return (
    <Sheet
      open
      onClose={close}
      width={580}
      footer={
        <PlanFooter error={error} onReplan={makePlan} onCancel={close}>
          {plan ? (
            <Button variant="danger" disabled={typed !== plan.summary.name || busy} onClick={run}>
              Borrar el disco nuevo y reconstruir
            </Button>
          ) : (
            <Button variant="primary" disabled={!pick || busy} onClick={makePlan}>
              Continuar
            </Button>
          )}
        </PlanFooter>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Reemplazar disco de {vol.name}</h2>
      </div>
      {!plan ? (
        <>
          <p className="tg-muted ts-wiz-lead">
            {failed ? (
              <>
                El disco de la {failed.bay ? `bahía ${failed.bay}` : "unidad interna"} ({failed.model}, serie {failed.serial}) dejó de responder. Elegí el disco nuevo: tiene que ser de {fmtBytes(need)} o más.
              </>
            ) : (
              <>Elegí el disco que se suma al array. Tiene que ser de {fmtBytes(need)} o más.</>
            )}
          </p>
          {candidates.length === 0 ? (
            <div className="ts-note-box">
              <Info size={16} />
              <span>No hay discos libres. Poné el disco nuevo en una bahía vacía (no hace falta apagar el NAS): aparece acá solo.</span>
            </div>
          ) : (
            <div className="ts-pick">
              {candidates.map((d) => (
                <PickDisk key={d.id} d={d} pressed={pick === d.id} reason={reasonOf(d)} onToggle={() => setPick(d.id)} />
              ))}
            </div>
          )}
        </>
      ) : (
        <>
          <div className="ts-rebuild-flow">
            <div>
              <small>Sale</small>
              <strong>{failed ? `${failed.bay ? `Bahía ${failed.bay}` : "Disco"} · ${failed.model}` : "—"}</strong>
            </div>
            <ArrowRight size={18} />
            <div>
              <small>Entra</small>
              <strong>{picked ? `${picked.bay ? `Bahía ${picked.bay}` : "Disco"} · ${picked.model}` : "Disco nuevo"}</strong>
            </div>
            <div className="ts-push" />
            <div>
              <small>Reconstrucción</small>
              <strong className="tg-num">≈ {fmtDuration(plan.summary.rebuild?.estimatedSec ?? 0)}</strong>
            </div>
          </div>
          <div className="ts-danger">
            <AlertTriangle size={18} />
            <div>
              <strong>Se borra todo lo que tenga el disco nuevo</strong>
              <p>Los datos del volumen no se tocan: se copian desde los discos sanos.</p>
            </div>
          </div>
          <EraseList plan={plan} disks={disks} verb="se borra y pasa a ser parte del array" />
          {plan.summary.notes?.map((n) => (
            <p key={n} className="ts-note">
              <Info size={12} /> {n}
            </p>
          ))}
          <div className="ts-summary">
            <span className="tg-muted">
              {profileLabel(plan.summary.profile)} · {fmtBytes(plan.summary.usable)} útiles
            </span>
            <Expiry left={left} />
          </div>
          <label className="ts-field">
            <span>
              Para confirmar, escribí <code>{plan.summary.name}</code>
            </span>
            <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} aria-label={`Escribí ${plan.summary.name} para confirmar`} autoComplete="off" />
          </label>
        </>
      )}
    </Sheet>
  );
}

// ---- Ampliar volumen ----------------------------------------------------------------

export function ExpandVolume({ vol, disks, onClose }: { vol: Volume | null; disks: Disk[]; onClose: () => void }) {
  const t = useTransport();
  const [sel, setSel] = useState<string[]>([]);
  const [plan, setPlan] = useState<MaintenancePlan | null>(null);
  const [typed, setTyped] = useState("");
  const [error, setError] = useState<{ code: string; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [jobId, setJobId] = useState<string | null>(null);
  const left = useCountdown(plan?.expires);

  const need = vol ? memberSize(vol, disks) : 0;
  const members = vol ? disks.filter((d) => vol.disks.includes(d.id) || d.volume === vol.id).length : 0;
  const free = disks.filter((d) => d.use === "free" && !d.foreign);
  const reasonOf = (d: Disk) => (d.size < need ? `Más chico que los discos del volumen (${fmtBytes(need)})` : null);
  const n = members + sel.length;
  const info = vol ? profileInfo(vol.profile) : null;
  const why = info && sel.length ? info.check(n) : null;
  const estimate = info && sel.length && !why ? info.usable(Array(n).fill(need)) : null;

  useEffect(() => {
    setSel([]);
    setPlan(null);
    setTyped("");
    setError(null);
    setJobId(null);
    setBusy(false);
  }, [vol?.id]);

  const makePlan = useCallback(async () => {
    if (!vol || !sel.length) return;
    setBusy(true);
    setError(null);
    setTyped("");
    try {
      setPlan(await t.call("storage.volumes.expandPlan", { volume: vol.id, add: sel }));
    } catch (e) {
      setPlan(null);
      setError(errOf(e));
    } finally {
      setBusy(false);
    }
  }, [t, vol, sel]);

  useEffect(() => {
    if (left === 0 && plan && !busy && !jobId) void makePlan();
  }, [left, plan, busy, jobId, makePlan]);

  const run = async () => {
    if (!plan) return;
    setBusy(true);
    setError(null);
    try {
      const r = await t.call("storage.volumes.expand", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: typed });
      setJobId(r.job);
    } catch (e) {
      const err = errOf(e);
      setError(err);
      if (err.code === "PLAN_EXPIRED" || err.code === "DEVICE_CHANGED") void makePlan();
      if (err.code === "DEVICE_NOT_ELIGIBLE" || err.code === "DEVICE_TOO_SMALL") setPlan(null);
    } finally {
      setBusy(false);
    }
  };

  const close = () => {
    if (jobId) toast({ level: "info", title: `${vol?.name} se sigue ampliando`, body: "Puede tardar horas. Podés usar el volumen mientras tanto." });
    onClose();
  };

  if (!vol) return null;

  if (jobId)
    return (
      <Running
        volId={vol.id}
        jobId={jobId}
        onClose={close}
        title={`Ampliando ${vol.name}…`}
        doneTitle={`${vol.name} se amplió`}
        failTitle={`No se pudo ampliar ${vol.name}`}
        body="El array se reorganiza para repartir los datos entre todos los discos. Podés seguir usando el volumen: la capacidad nueva aparece cuando termina."
        doneBody="La capacidad nueva ya está disponible."
      />
    );

  return (
    <Sheet
      open
      onClose={close}
      width={580}
      footer={
        <PlanFooter error={error} onReplan={makePlan} onCancel={close}>
          {plan ? (
            <Button variant="danger" disabled={typed !== plan.summary.name || busy} onClick={run}>
              <Maximize2 size={13} /> Borrar los discos nuevos y ampliar
            </Button>
          ) : (
            <Button variant="primary" disabled={!sel.length || !!why || busy} onClick={makePlan}>
              Continuar
            </Button>
          )}
        </PlanFooter>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Ampliar {vol.name}</h2>
      </div>
      {!plan ? (
        <>
          <p className="tg-muted ts-wiz-lead">
            Sumá discos libres a este {profileLabel(vol.profile)}. Cada disco nuevo tiene que ser de {fmtBytes(need)} o más; si es más grande, se usa hasta ese tamaño.
          </p>
          {free.length === 0 ? (
            <div className="ts-note-box">
              <Info size={16} />
              <span>No hay discos libres. Poné discos nuevos en bahías vacías y van a aparecer acá.</span>
            </div>
          ) : (
            <div className="ts-pick" data-cols={free.length > 6 || undefined}>
              {free.map((d) => (
                <PickDisk key={d.id} multi d={d} pressed={sel.includes(d.id)} reason={reasonOf(d)} onToggle={() => setSel((s) => (s.includes(d.id) ? s.filter((x) => x !== d.id) : [...s, d.id]))} />
              ))}
            </div>
          )}
          <div className="ts-selbar tg-num">
            <span>
              {members} discos actuales + {sel.length} {sel.length === 1 ? "nuevo" : "nuevos"}
            </span>
            {why ? <em>{why}</em> : estimate ? <strong>{fmtBytes(vol.size)} → ≈ {fmtBytes(estimate)}</strong> : <span className="tg-muted">Elegí uno o más discos</span>}
          </div>
        </>
      ) : (
        <>
          <div className="ts-grow">
            <div>
              <small>Hoy</small>
              <strong className="tg-num">{fmtBytes(plan.summary.usable)}</strong>
            </div>
            <ArrowRight size={18} />
            <div>
              <small>Después</small>
              <strong className="tg-num">{fmtBytes(plan.summary.newUsable ?? plan.summary.usable)}</strong>
            </div>
          </div>
          <EraseList plan={plan} disks={disks} verb="se borra y se suma al array" />
          {plan.summary.notes?.map((n2) => (
            <p key={n2} className="ts-note">
              <Info size={12} /> {n2}
            </p>
          ))}
          <div className="ts-summary">
            <span className="tg-muted">Podés seguir usando el volumen mientras se reorganiza.</span>
            <Expiry left={left} />
          </div>
          <label className="ts-field">
            <span>
              Para confirmar, escribí <code>{plan.summary.name}</code>
            </span>
            <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} aria-label={`Escribí ${plan.summary.name} para confirmar`} autoComplete="off" />
          </label>
        </>
      )}
    </Sheet>
  );
}
