import { useCallback, useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, Clock, FolderOpen, HardDrive, RotateCcw, Trash2, XOctagon } from "lucide-react";
import { RpcError, fmtBytes, t, tr, useJobs, useTransport } from "@tango/client";
import type { DeletePlan, Disk, Volume } from "@tango/client";
import { Button, Progress, Sheet, TextInput } from "@tango/ui";
import { toast } from "@tango/shell";

const errorText: Record<string, string> = {
  PLAN_EXPIRED: t("El plan venció. Lo volvemos a armar con el estado actual del volumen."),
  CONFIRMATION_MISMATCH: t("La confirmación no coincide. Escribí el nombre exacto del volumen."),
  VOLUME_BUSY: t("El volumen está ocupado (verificando o construyéndose). Esperá a que termine y volvé a intentar."),
  VOLUME_NOT_FOUND: t("El volumen ya no existe."),
  DEVICE_CHANGED: t("Un disco del volumen cambió. Volvé a revisar el plan."),
};

/** Eliminar un volumen: plan del servidor → escribir el nombre → job. Nunca hay un "borrar" de un clic. */
export function DeleteVolume({ vol, disks, onClose }: { vol: Volume | null; disks: Disk[]; onClose: () => void }) {
  const rpc = useTransport();
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
      setPlan(await rpc.call("storage.volumes.deletePlan", { volume: vol.id }));
    } catch (e) {
      const code = e instanceof RpcError ? e.code : "INTERNAL";
      setError({ code, text: errorText[code] ?? (e as Error).message });
    }
  }, [rpc, vol]);

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
      const r = await rpc.call("storage.volumes.delete", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: typed });
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
    if (jobId && !done && !failed) toast({ level: "info", title: t("{name} se sigue eliminando", { name: String(vol?.name) }), body: t("Podés ver el progreso en Actividad, en la barra de menús.") });
    onClose();
  };

  if (!vol) return null;

  if (jobId) {
    return (
      <Sheet open onClose={close} width={500} footer={<Button variant="primary" onClick={close}>{done || failed ? t("Cerrar") : t("Seguir en segundo plano")}</Button>}>
        <div className="ts-wiz-done">
          {done ? <CheckCircle2 size={44} className="ts-ok-icon" /> : failed ? <XOctagon size={44} className="ts-crit-icon" /> : <Trash2 size={40} className="ts-muted-icon" />}
          <h2 className="tg-h2">{done ? t("{name} se eliminó", { name: vol.name }) : failed ? t("No se pudo eliminar {name}", { name: vol.name }) : t("Eliminando {name}…", { name: vol.name })}</h2>
          <p className="tg-muted">
            {done
              ? vol.kind === "pool"
                ? t("Su espacio volvió al grupo de almacenamiento.")
                : t("Los discos quedaron libres para un volumen nuevo.")
              : failed
                ? `${tr(job?.detail)} ${t("El volumen puede haber quedado parcialmente desarmado: revisalo en Discos.")}`
                : vol.kind === "pool"
                  ? t("Se desmonta, se quitan sus carpetas compartidas y se borra el volumen lógico. Los discos siguen en el grupo.")
                  : t("Se desmonta, se quitan sus carpetas compartidas, se detiene el array y se limpian los discos.")}
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
                  <RotateCcw size={12} /> {t("Volver a revisar")}
                </Button>
              )}
            </span>
          )}
          <Button onClick={close}>{t("Cancelar")}</Button>
          <Button variant="danger" disabled={!plan || typed !== plan.summary.name || busy} onClick={remove}>
            {t("Eliminar volumen y borrar sus datos")}
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">{t("Eliminar {name}", { name: vol.name })}</h2>
      </div>
      <div className="ts-danger">
        <AlertTriangle size={18} />
        <div>
          <strong>{t("Se pierde todo lo que hay en este volumen")}</strong>
          <p>{t("No hay papelera ni instantáneas que lo recuperen: las instantáneas viven dentro del volumen y se borran con él.")}</p>
        </div>
      </div>
      {!plan && !error && <p className="tg-muted ts-wiz-lead">{t("Revisando qué contiene el volumen…")}</p>}
      {plan && (
        <>
          <div className="ts-loss">
            <div>
              <small>{t("Datos")}</small>
              <strong className="tg-num">{fmtBytes(plan.summary.used)}</strong>
            </div>
            <div>
              <small>{t("Carpetas compartidas")}</small>
              <strong className="tg-num">{plan.summary.shares.length}</strong>
            </div>
            <div>
              <small>{t("Instantáneas")}</small>
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
                      {local?.bay ? t("Bahía {n}", { n: local.bay }) : t("Disco")} · {d.model}
                    </strong>
                    <small>
                      {t("Serie {serial} · queda libre y vacío", { serial: d.serial })}
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
                <Clock size={12} /> {t("Este plan vence en {time}", { time: `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}` })}
              </span>
            )}
          </div>
          <label className="ts-field">
            <span>
              {t("Para confirmar, escribí")} <code>{plan.summary.name}</code>
            </span>
            <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} aria-label={t("Escribí {name} para confirmar", { name: plan.summary.name })} autoComplete="off" />
          </label>
        </>
      )}
    </Sheet>
  );
}
