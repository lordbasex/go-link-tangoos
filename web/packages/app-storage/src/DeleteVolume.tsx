import { useCallback, useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, Clock, FolderOpen, HardDrive, RotateCcw, Trash2, XOctagon } from "lucide-react";
import { RpcError, fmtBytes, useJobs, useTransport } from "@tango/client";
import type { DeletePlan, Disk, Volume } from "@tango/client";
import { Button, Progress, Sheet, TextInput } from "@tango/ui";
import { toast } from "@tango/shell";

const errorText: Record<string, string> = {
  PLAN_EXPIRED: "El plan venció. Lo volvemos a armar con el estado actual del volumen.",
  CONFIRMATION_MISMATCH: "La confirmación no coincide. Escribí el nombre exacto del volumen.",
  VOLUME_BUSY: "El volumen está ocupado (verificando o construyéndose). Esperá a que termine y volvé a intentar.",
  VOLUME_NOT_FOUND: "El volumen ya no existe.",
  DEVICE_CHANGED: "Un disco del volumen cambió. Volvé a revisar el plan.",
};

/** Eliminar un volumen: plan del servidor → escribir el nombre → job. Nunca hay un "borrar" de un clic. */
export function DeleteVolume({ vol, disks, onClose }: { vol: Volume | null; disks: Disk[]; onClose: () => void }) {
  const t = useTransport();
  const jobs = useJobs();
  const [plan, setPlan] = useState<DeletePlan | null>(null);
  const [typed, setTyped] = useState("");
  const [error, setError] = useState<{ code: string; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [jobId, setJobId] = useState<string | null>(null);
  const [seen, setSeen] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  const makePlan = useCallback(async () => {
    if (!vol) return;
    setPlan(null);
    setTyped("");
    setError(null);
    try {
      setPlan(await t.call("storage.volumes.deletePlan", { volume: vol.id }));
    } catch (e) {
      const code = e instanceof RpcError ? e.code : "INTERNAL";
      setError({ code, text: errorText[code] ?? (e as Error).message });
    }
  }, [t, vol]);

  useEffect(() => {
    setJobId(null);
    setSeen(false);
    setBusy(false);
    if (vol) void makePlan();
  }, [vol, makePlan]);

  useEffect(() => {
    if (!plan) return;
    const i = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(i);
  }, [plan]);
  const left = plan ? Math.max(0, Math.round((new Date(plan.expires).getTime() - now) / 1000)) : null;
  useEffect(() => {
    if (left === 0 && !busy && !jobId) void makePlan();
  }, [left, busy, jobId, makePlan]);

  const job = jobs.find((j) => j.id === jobId);
  useEffect(() => {
    if (job) setSeen(true);
  }, [job]);
  const failed = !!job && (job.state === "failed" || job.state === "interrupted" || job.state === "cancelled");
  const done = !!jobId && !failed && ((job && job.state === "succeeded") || (seen && !job));

  const remove = async () => {
    if (!plan) return;
    setBusy(true);
    setError(null);
    try {
      const r = await t.call("storage.volumes.delete", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: typed });
      setJobId(r.job);
    } catch (e) {
      const code = e instanceof RpcError ? e.code : "INTERNAL";
      setError({ code, text: errorText[code] ?? (e as Error).message });
      if (code === "PLAN_EXPIRED" || code === "DEVICE_CHANGED") void makePlan();
    } finally {
      setBusy(false);
    }
  };

  const close = () => {
    if (jobId && !done && !failed) toast({ level: "info", title: `${vol?.name} se sigue eliminando`, body: "Podés ver el progreso en Actividad, en la barra de menús." });
    onClose();
  };

  if (!vol) return null;

  if (jobId) {
    return (
      <Sheet open onClose={close} width={500} footer={<Button variant="primary" onClick={close}>{done || failed ? "Cerrar" : "Seguir en segundo plano"}</Button>}>
        <div className="ts-wiz-done">
          {done ? <CheckCircle2 size={44} className="ts-ok-icon" /> : failed ? <XOctagon size={44} className="ts-crit-icon" /> : <Trash2 size={40} className="ts-muted-icon" />}
          <h2 className="tg-h2">{done ? `${vol.name} se eliminó` : failed ? `No se pudo eliminar ${vol.name}` : `Eliminando ${vol.name}…`}</h2>
          <p className="tg-muted">
            {done
              ? vol.kind === "pool"
                ? "Su espacio volvió al grupo de almacenamiento."
                : "Los discos quedaron libres para un volumen nuevo."
              : failed
                ? `${job?.detail ?? ""} El volumen puede haber quedado parcialmente desarmado: revisalo en Discos.`
                : vol.kind === "pool"
                  ? "Se desmonta, se quitan sus carpetas compartidas y se borra el volumen lógico. Los discos siguen en el grupo."
                  : "Se desmonta, se quitan sus carpetas compartidas, se detiene el array y se limpian los discos."}
          </p>
          {!done && !failed && (
            <div className="ts-wiz-progress">
              <Progress value={job?.progress ?? 0} tone="crit" />
              <span className="tg-num">{Math.round((job?.progress ?? 0) * 100)} %</span>
            </div>
          )}
        </div>
      </Sheet>
    );
  }

  return (
    <Sheet
      open
      onClose={close}
      width={560}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error.text}
              {(error.code === "PLAN_EXPIRED" || error.code === "DEVICE_CHANGED") && (
                <Button variant="plain" onClick={makePlan}>
                  <RotateCcw size={12} /> Volver a revisar
                </Button>
              )}
            </span>
          )}
          <Button onClick={close}>Cancelar</Button>
          <Button variant="danger" disabled={!plan || typed !== plan.summary.name || busy} onClick={remove}>
            Eliminar volumen y borrar sus datos
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Eliminar {vol.name}</h2>
      </div>
      <div className="ts-danger">
        <AlertTriangle size={18} />
        <div>
          <strong>Se pierde todo lo que hay en este volumen</strong>
          <p>No hay papelera ni instantáneas que lo recuperen: las instantáneas viven dentro del volumen y se borran con él.</p>
        </div>
      </div>
      {!plan && !error && <p className="tg-muted ts-wiz-lead">Revisando qué contiene el volumen…</p>}
      {plan && (
        <>
          <div className="ts-loss">
            <div>
              <small>Datos</small>
              <strong className="tg-num">{fmtBytes(plan.summary.used)}</strong>
            </div>
            <div>
              <small>Carpetas compartidas</small>
              <strong className="tg-num">{plan.summary.shares.length}</strong>
            </div>
            <div>
              <small>Instantáneas</small>
              <strong className="tg-num">{plan.summary.snapshots}</strong>
            </div>
          </div>
          {plan.summary.shares.length > 0 && (
            <div className="ts-loss-shares">
              {plan.summary.shares.map((s) => (
                <span key={s}>
                  <FolderOpen size={13} /> {s}
                </span>
              ))}
            </div>
          )}
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
                      Serie {d.serial} · queda libre y vacío
                    </small>
                  </div>
                </div>
              );
            })}
          </div>
          <div className="ts-summary">
            <span />
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
            <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} aria-label={`Escribí ${plan.summary.name} para confirmar`} autoComplete="off" />
          </label>
        </>
      )}
    </Sheet>
  );
}
