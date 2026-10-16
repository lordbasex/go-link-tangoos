import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, Check, CheckCircle2, Clock, HardDrive, Layers, RotateCcw, XOctagon } from "lucide-react";
import { POOL_PROFILES, RpcError, fmtBytes, poolLayout, poolProfileLabel, t, tiersUnused, tiersUsable, tn, tr, useJobs, useTransport } from "@tango/client";
import type { Disk, Pool, PoolPlan, PoolProfile, Volume } from "@tango/client";
import { Button, Progress, Sheet, TextInput } from "@tango/ui";
import { toast } from "@tango/shell";
import { useCountdown } from "./CreateVolume";
import { TierDiagram } from "./Tiers";
import { ineligibleReason } from "./util";

const STEPS = [t("Discos"), t("Protección"), t("Nombre"), t("Revisión")];

const errorText: Record<string, string> = {
  PLAN_EXPIRED: t("El plan venció. Lo volvemos a armar con el estado actual de los discos."),
  CONFIRMATION_MISMATCH: t("La confirmación no coincide con el plan. Volvé a escribir el nombre del grupo."),
  DEVICE_CHANGED: t("Un disco cambió desde que armaste el plan. Volvé a revisarlo."),
  DEVICE_NOT_ELIGIBLE: t("Uno de los discos ya no se puede usar. Volvé a elegir los discos."),
  POOL_EXISTS: t("Ya existe un grupo o un volumen con ese nombre. Elegí otro."),
  VOLUME_EXISTS: t("Ya existe un volumen con ese nombre. Elegí otro."),
};

const diskLabel = (d: Disk) => `${d.bay ? t("Bahía {n}", { n: d.bay }) : t("Interno")} · ${d.model}`;

/** Asistente: discos → protección (RAID o híbrido, con el reparto en tramos) → nombre → plan con confirmación. */
export function CreatePool({ open, onClose, disks, vols, pools, onCreated }: { open: boolean; onClose: () => void; disks: Disk[]; vols: Volume[]; pools: Pool[]; onCreated?: (name: string) => void }) {
  const rpc = useTransport();
  const jobs = useJobs();
  const [step, setStep] = useState(0);
  const [sel, setSel] = useState<string[]>([]);
  const [profile, setProfile] = useState<PoolProfile>("hybrid1");
  const [name, setName] = useState("");
  const [confirm, setConfirm] = useState("");
  const [plan, setPlan] = useState<PoolPlan | null>(null);
  const [planning, setPlanning] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ code: string; text: string } | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [seen, setSeen] = useState(false);
  const left = useCountdown(plan?.expires);

  const candidates = disks.filter((d) => d.use !== "system");
  const chosen = disks.filter((d) => sel.includes(d.id));
  const eligible = candidates.filter((d) => !ineligibleReason(d, vols, pools));

  useEffect(() => {
    if (!open) return;
    setStep(0);
    setSel([]);
    setName(`pool${pools.length + 1}`);
    setConfirm("");
    setPlan(null);
    setError(null);
    setJobId(null);
    setSeen(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // Perfil sugerido: híbrido cuando hay más de un disco (aprovecha tamaños distintos).
  useEffect(() => {
    const n = sel.length;
    setProfile(n === 1 ? "basic" : "hybrid1");
  }, [sel.length]);

  const tiers = useMemo(() => poolLayout(profile, chosen.map((d) => ({ id: d.id, size: d.size }))), [profile, chosen]);
  const profileOk = (p: PoolProfile) => POOL_PROFILES.find((x) => x.id === p)?.check(sel.length) === null;
  const nameOk = /^[a-z][a-z0-9]{0,23}$/.test(name) && !pools.some((p) => p.name === name) && !vols.some((v) => v.name === name);
  const canNext = step === 0 ? sel.length > 0 : step === 1 ? profileOk(profile) : step === 2 ? nameOk : !!plan && confirm === name;

  const job = jobs.find((j) => j.id === jobId);
  useEffect(() => {
    if (job) setSeen(true);
  }, [job]);
  const failed = !!job && (job.state === "failed" || job.state === "interrupted" || job.state === "cancelled");
  const done = !!jobId && !failed && ((job && job.state === "succeeded") || (seen && !job));

  const toError = (e: unknown) => {
    const code = e instanceof RpcError ? e.code : "INTERNAL";
    return { code, text: errorText[code] ?? (e as Error).message };
  };

  const makePlan = useCallback(async () => {
    setPlanning(true);
    setPlan(null);
    setError(null);
    setConfirm("");
    try {
      setPlan(await rpc.call("storage.pools.plan", { name, profile, disks: sel }));
    } catch (e) {
      setError(toError(e));
    } finally {
      setPlanning(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rpc, name, profile, sel]);

  useEffect(() => {
    if (step === 3) void makePlan();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);
  useEffect(() => {
    if (step === 3 && plan && left === 0 && !busy) void makePlan();
  }, [left, step, plan, busy, makePlan]);

  const create = async () => {
    if (!plan) return;
    setBusy(true);
    setError(null);
    try {
      const r = await rpc.call("storage.pools.create", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm });
      setJobId(r.job);
    } catch (e) {
      const err = toError(e);
      setError(err);
      if (err.code === "PLAN_EXPIRED") void makePlan();
    } finally {
      setBusy(false);
    }
  };

  const close = () => {
    if (jobId && !done) toast({ level: "info", title: t("El grupo {name} se sigue creando", { name }), body: t("Podés ver el progreso en Actividad.") });
    onClose();
  };

  if (jobId) {
    return (
      <Sheet
        open={open}
        onClose={close}
        width={520}
        footer={
          <>
            {done && onCreated && (
              <Button
                onClick={() => {
                  onClose();
                  onCreated(name);
                }}
              >
                {t("Crear un volumen en {name}…", { name })}
              </Button>
            )}
            <Button variant="primary" onClick={close}>
              {done || failed ? t("Cerrar") : t("Seguir en segundo plano")}
            </Button>
          </>
        }
      >
        <div className="ts-wiz-done">
          {done ? <CheckCircle2 size={44} className="ts-ok-icon" /> : failed ? <XOctagon size={44} className="ts-crit-icon" /> : <Layers size={40} className="ts-muted-icon" />}
          <h2 className="tg-h2">{done ? t("El grupo {name} está listo", { name }) : failed ? t("No se pudo crear el grupo {name}", { name }) : t("Creando el grupo {name}…", { name })}</h2>
          <p className="tg-muted">
            {done
              ? t("{size} para repartir en volúmenes. Creá el primero: Btrfs para carpetas compartidas, con cifrado si querés.", { size: fmtBytes(plan?.summary.usable ?? 0) })
              : failed
                ? `${t("La tarea falló.")} ${tr(job?.detail)} ${t("Los discos quedaron libres.")}`
                : t("Se particionan los discos, se arman los RAID de cada tramo y se crea el grupo (LVM). La sincronización inicial sigue en segundo plano.")}
          </p>
          {!done && !failed && (
            <div className="ts-wiz-progress">
              <Progress value={job?.progress ?? 0} />
              <span className="tg-num">{Math.round((job?.progress ?? 0) * 100)} %</span>
            </div>
          )}
        </div>
      </Sheet>
    );
  }

  return (
    <Sheet
      open={open}
      onClose={close}
      width={660}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error.text}
              {(error.code === "PLAN_EXPIRED" || error.code === "DEVICE_CHANGED") && (
                <Button variant="plain" onClick={() => void makePlan()}>
                  <RotateCcw size={12} /> {t("Volver a revisar")}
                </Button>
              )}
            </span>
          )}
          <Button onClick={step === 0 ? close : () => setStep(step - 1)}>{step === 0 ? t("Cancelar") : t("Atrás")}</Button>
          {step < 3 ? (
            <Button variant="primary" disabled={!canNext} onClick={() => setStep(step + 1)}>
              {t("Continuar")}
            </Button>
          ) : (
            <Button variant="danger" disabled={!canNext || busy || planning} onClick={create}>
              {t("Borrar discos y crear el grupo")}
            </Button>
          )}
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">{t("Crear grupo de almacenamiento")}</h2>
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
          <p className="tg-muted ts-wiz-lead">
            {t("Un grupo junta discos con su protección; después creás volúmenes dentro, del tamaño que quieras. Los discos pueden ser de distinto tamaño.")}
          </p>
          <div className="ts-pick-bar">
            <span className="tg-num">
              {sel.length ? (
                <>
                  {t("Seleccionados:")} <b>{sel.length}</b> · {t("{size} en total", { size: fmtBytes(chosen.reduce((a, d) => a + d.size, 0)) })}
                </>
              ) : (
                tn(eligible.length, "{n} disco libre", "{n} discos libres")
              )}
            </span>
            <span className="ts-pick-actions">
              <Button variant="plain" disabled={!eligible.length || sel.length === eligible.length} onClick={() => setSel(eligible.map((d) => d.id))}>
                {t("Todos los libres")}
              </Button>
              <Button variant="plain" disabled={!sel.length} onClick={() => setSel([])}>
                {t("Ninguno")}
              </Button>
            </span>
          </div>
          <div className="ts-pick" data-many={candidates.length > 6 || undefined}>
            {candidates.map((d) => {
              const reason = ineligibleReason(d, vols, pools);
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
        </>
      )}

      {step === 1 && (
        <>
          <p className="tg-muted ts-wiz-lead">
            {t("El RAID híbrido reparte cada disco en tramos del tamaño de los otros discos y arma un RAID por tramo: así se aprovechan discos de distinto tamaño, y el grupo crece sumando de a un disco.")}
          </p>
          <div className="ts-profiles ts-profiles-pool" role="radiogroup" aria-label={t("Protección")}>
            {POOL_PROFILES.map((info) => {
              const why = info.check(sel.length);
              return (
                <button key={info.id} type="button" role="radio" aria-checked={profile === info.id} className="ts-profile" disabled={!!why} onClick={() => setProfile(info.id)}>
                  <strong>
                    {info.label} {info.id === "hybrid1" && <span className="ts-soon">{t("recomendado")}</span>}
                  </strong>
                  <small>{info.description}</small>
                  <small className="ts-tolerance" data-none={info.id === "basic" || undefined}>
                    {info.tolerance}
                  </small>
                  {why && <em>{why}</em>}
                </button>
              );
            })}
          </div>
          {profileOk(profile) && (
            <>
              <TierDiagram tiers={tiers} disks={chosen.map((d) => ({ id: d.id, label: diskLabel(d), size: d.size }))} />
              {tiersUnused(tiers) > 0 && (
                <p className="ts-note">
                  {t("{size} quedan reservados: con {profile} se suman al grupo cuando agregues otro disco igual o más grande que el mayor.", { size: fmtBytes(tiersUnused(tiers)), profile: poolProfileLabel(profile) })}
                  {!profile.startsWith("hybrid") && ` ${t("Con RAID híbrido se aprovecharían desde ya.")}`}
                </p>
              )}
              <p className="ts-note">
                {t("Capacidad útil estimada:")} <b className="tg-num">{fmtBytes(tiersUsable(tiers))}</b> {t("antes del sistema de archivos.")}
              </p>
            </>
          )}
        </>
      )}

      {step === 2 && (
        <div className="ts-form">
          <label className="ts-field">
            <span>{t("Nombre del grupo")}</span>
            <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-label={t("Nombre del grupo")} />
            <small>{nameOk || !name ? t("Minúsculas y números (hasta 24). Los volúmenes del grupo llevan su propio nombre.") : t("Ese nombre no está disponible o no es válido.")}</small>
          </label>
        </div>
      )}

      {step === 3 && (
        <>
          <div className="ts-danger">
            <AlertTriangle size={18} />
            <div>
              <strong>{t("Se van a borrar estos discos")}</strong>
              <p>{t("Todo lo que contienen se pierde. TangoOS vuelve a verificar la identidad de cada disco justo antes de empezar.")}</p>
            </div>
          </div>
          {planning && !plan && <p className="tg-muted ts-wiz-lead">{t("Armando el plan con el estado actual de los discos…")}</p>}
          {plan && (
            <>
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
              <TierDiagram tiers={plan.summary.tiers} disks={plan.summary.disks.map((d) => ({ id: d.id, label: d.model, size: d.size }))} height={96} />
              {plan.summary.notes?.map((n) => (
                <p key={n} className="ts-note">
                  {tr(n)}
                </p>
              ))}
              <div className="ts-summary">
                <span>
                  <Layers size={13} /> {poolProfileLabel(plan.summary.profile)} · {t("{size} útiles", { size: fmtBytes(plan.summary.usable) })}
                </span>
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
          )}
        </>
      )}
    </Sheet>
  );
}
