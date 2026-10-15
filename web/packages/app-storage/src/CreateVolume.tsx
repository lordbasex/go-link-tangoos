import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, Check, CheckCircle2, Clock, HardDrive, Lock, RotateCcw, XOctagon } from "lucide-react";
import { PROFILES, RpcError, fmtBytes, profileInfo, useJobs, useTransport } from "@tango/client";
import type { Disk, Volume, VolumePlan, VolumeProfile } from "@tango/client";
import { Button, Progress, Sheet, TextInput, Toggle } from "@tango/ui";
import { toast } from "@tango/shell";
import { capacity, ineligibleReason, profileName } from "./util";

const STEPS = ["Discos", "Perfil", "Opciones", "Revisión"];

/** Mensajes claros por código de error estable (protocol/README.md §7). */
const errorText: Record<string, string> = {
  PLAN_EXPIRED: "El plan venció. Lo volvemos a armar con el estado actual de los discos.",
  CONFIRMATION_MISMATCH: "La confirmación no coincide con el plan. Volvé a escribir el nombre del volumen.",
  DEVICE_CHANGED: "Un disco cambió desde que armaste el plan. Volvé a revisarlo.",
  DEVICE_NOT_ELIGIBLE: "Uno de los discos ya no se puede usar (está montado, en uso o falló). Volvé a elegir los discos.",
  VOLUME_EXISTS: "Ya existe un volumen con ese nombre. Elegí otro.",
};
/** Hacia dónde lleva el botón de recuperación de cada error. */
const errorAction: Record<string, { label: string; step: number | "replan" }> = {
  PLAN_EXPIRED: { label: "Volver a armar el plan", step: "replan" },
  DEVICE_CHANGED: { label: "Volver a revisar", step: "replan" },
  DEVICE_NOT_ELIGIBLE: { label: "Elegir discos", step: 0 },
  VOLUME_EXISTS: { label: "Cambiar nombre", step: 2 },
};

export function useCountdown(expires: string | undefined) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!expires) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [expires]);
  if (!expires) return null;
  return Math.max(0, Math.round((new Date(expires).getTime() - now) / 1000));
}

export function CreateVolume({ open, onClose, disks, vols }: { open: boolean; onClose: () => void; disks: Disk[]; vols: Volume[] }) {
  const t = useTransport();
  const jobs = useJobs();
  const [step, setStep] = useState(0);
  const [sel, setSel] = useState<string[]>([]);
  const [profile, setProfile] = useState<VolumeProfile>("raid1");
  const [name, setName] = useState("");
  const [confirm, setConfirm] = useState("");
  const [jobId, setJobId] = useState<string | null>(null);
  const [seen, setSeen] = useState(false);
  const [error, setError] = useState<{ code: string; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [plan, setPlan] = useState<VolumePlan | null>(null);
  const [planning, setPlanning] = useState(false);
  const left = useCountdown(plan?.expires);

  const bays = disks.filter((d) => d.bay !== null || d.use === "system");
  const chosen = disks.filter((d) => sel.includes(d.id));
  const defaultName = `volume${vols.length + 1}`;

  useEffect(() => {
    if (!open) return;
    setStep(0);
    setSel([]);
    setName(defaultName);
    setConfirm("");
    setJobId(null);
    setSeen(false);
    setError(null);
    setBusy(false);
    setPlan(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // Perfil por defecto según la cantidad de discos: el más protegido que tenga sentido.
  useEffect(() => {
    const n = sel.length;
    if (n === 1) setProfile("basic");
    else if (n === 2) setProfile("raid1");
    else if (n === 3) setProfile("raid5");
    else if (n >= 4) setProfile("raid6");
  }, [sel.length]);

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

  // El paso Revisión muestra lo que el SERVIDOR va a hacer: pide el plan al entrar.
  const makePlan = useCallback(async () => {
    setPlanning(true);
    setPlan(null);
    setError(null);
    setConfirm("");
    try {
      setPlan(await t.call("storage.volumes.plan", { name, profile, disks: sel }));
    } catch (e) {
      setError(toError(e));
    } finally {
      setPlanning(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t, name, profile, sel]);

  useEffect(() => {
    if (step === 3) void makePlan();
    else {
      setPlan(null);
      setError(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);

  // Plan vencido: se rearma solo (nunca se ejecuta un plan viejo).
  useEffect(() => {
    if (step === 3 && plan && left === 0 && !busy) void makePlan();
  }, [left, step, plan, busy, makePlan]);

  const cap = useMemo(() => capacity(profile, chosen.map((d) => d.size)), [profile, chosen]);
  const profileOk = (p: VolumeProfile) => sel.length > 0 && profileInfo(p).check(sel.length) === null;
  const eligible = bays.filter((d) => !ineligibleReason(d, vols));
  // Estimación en vivo mientras se eligen discos (con el perfil que se propondría).
  const preview = useMemo(() => {
    const n = chosen.length;
    if (!n) return null;
    const p: VolumeProfile = n === 1 ? "basic" : n === 2 ? "raid1" : n === 3 ? "raid5" : "raid6";
    return { label: profileInfo(p).label, usable: capacity(p, chosen.map((d) => d.size)).usable };
  }, [chosen]);
  const canNext = step === 0 ? sel.length > 0 : step === 1 ? profileOk(profile) : step === 2 ? /^[a-z][a-z0-9_-]{1,30}$/.test(name) && !vols.some((v) => v.name === name) : !!plan && confirm === name;

  const create = async () => {
    if (!plan) return;
    setBusy(true);
    setError(null);
    try {
      const r = await t.call("storage.volumes.create", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm });
      setJobId(r.job);
    } catch (e) {
      const err = toError(e);
      setError(err);
      if (err.code === "PLAN_EXPIRED") void makePlan();
    } finally {
      setBusy(false);
    }
  };

  const recover = () => {
    const a = error && errorAction[error.code];
    if (!a) return;
    if (a.step === "replan") void makePlan();
    else setStep(a.step);
  };

  const close = () => {
    if (jobId && !done) toast({ level: "info", title: `${name} se sigue creando`, body: "Podés ver el progreso en Actividad, en la barra de menús." });
    onClose();
  };

  // ---- Progreso / éxito ----------------------------------------------------------
  if (jobId) {
    return (
      <Sheet open={open} onClose={close} width={520} footer={<Button variant="primary" onClick={close}>{done || failed ? "Cerrar" : "Seguir en segundo plano"}</Button>}>
        <div className="ts-wiz-done">
          {done ? <CheckCircle2 size={44} className="ts-ok-icon" /> : failed ? <XOctagon size={44} className="ts-crit-icon" /> : <HardDrive size={40} className="ts-muted-icon" />}
          <h2 className="tg-h2">{done ? `${name} está listo` : failed ? `No se pudo crear ${name}` : `Creando ${name}…`}</h2>
          <p className="tg-muted">
            {done
              ? `${fmtBytes(plan?.summary.usable ?? cap.usable)} disponibles con ${profileName[profile]}. Ya podés crear carpetas compartidas.`
              : failed
                ? `${job?.state === "interrupted" ? "La tarea se interrumpió (el servidor se reinició)." : "La tarea falló."} ${job?.detail ?? ""} Los discos no se marcaron como parte de ningún volumen.`
                : job?.state === "queued" || job?.state === "validating"
                  ? "Verificando de nuevo la identidad de cada disco antes de empezar…"
                  : profile === "basic"
                    ? "Se crea Btrfs en el disco y se monta. Podés cerrar esta hoja: la tarea sigue."
                    : `Se arma el array ${profileName[profile]} con mdadm, se crea Btrfs encima y se monta. La sincronización inicial sigue en segundo plano. Podés cerrar esta hoja: la tarea sigue.`}
          </p>
          {failed && (
            <Button disabled title="El Registro llega en M1">
              Ver registro
            </Button>
          )}
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
      width={620}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error.text}
              {errorAction[error.code] && (
                <Button variant="plain" onClick={recover}>
                  <RotateCcw size={12} /> {errorAction[error.code].label}
                </Button>
              )}
            </span>
          )}
          <Button onClick={step === 0 ? close : () => setStep(step - 1)}>{step === 0 ? "Cancelar" : "Atrás"}</Button>
          {step < 3 ? (
            <Button variant="primary" disabled={!canNext} onClick={() => setStep(step + 1)}>
              Continuar
            </Button>
          ) : (
            <Button variant="danger" disabled={!canNext || busy || planning} onClick={create}>
              Borrar discos y crear volumen
            </Button>
          )}
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Crear volumen</h2>
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
          <p className="tg-muted ts-wiz-lead">Elegí los discos del volumen. Solo se muestran como disponibles los que no tienen datos en uso.</p>
          <div className="ts-pick-bar">
            <span className="tg-num">
              {sel.length ? (
                <>
                  Seleccionados: <b>{sel.length}</b> · capacidad útil estimada <b>{fmtBytes(preview?.usable ?? 0)}</b> con {preview?.label}
                </>
              ) : (
                `${eligible.length} ${eligible.length === 1 ? "disco libre" : "discos libres"}`
              )}
            </span>
            <span className="ts-pick-actions">
              <Button variant="plain" disabled={!eligible.length || sel.length === eligible.length} onClick={() => setSel(eligible.map((d) => d.id))}>
                Todos los libres
              </Button>
              <Button variant="plain" disabled={!sel.length} onClick={() => setSel([])}>
                Ninguno
              </Button>
            </span>
          </div>
          <div className="ts-pick" data-many={bays.length > 6 || undefined}>
            {bays.map((d) => {
              const reason = ineligibleReason(d, vols);
              const on = sel.includes(d.id);
              return (
                <button
                  key={d.id}
                  type="button"
                  className="ts-pick-disk"
                  aria-pressed={on}
                  disabled={!!reason}
                  onClick={() => setSel((s) => (on ? s.filter((x) => x !== d.id) : [...s, d.id]))}
                >
                  <span className="ts-pick-check">{on && <Check size={12} strokeWidth={3} />}</span>
                  <HardDrive size={22} strokeWidth={1.5} />
                  <span className="ts-pick-text">
                    <strong>{d.bay ? `Bahía ${d.bay}` : "Disco interno"} · {d.model}</strong>
                    <small>
                      {fmtBytes(d.size)} · serie {d.serial}
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
          <p className="tg-muted ts-wiz-lead">Elegí cómo se protegen los datos. Podés cambiar a un perfil con más discos más adelante.</p>
          <div className="ts-profiles" role="radiogroup" aria-label="Perfil">
            {PROFILES.filter((x) => !x.poolOnly).map((info) => {
              const p = info.id;
              const why = info.check(sel.length);
              return (
                <button key={p} type="button" role="radio" aria-checked={profile === p} className="ts-profile" disabled={!!why} onClick={() => setProfile(p)}>
                  <strong>{info.label}</strong>
                  <small>{info.description}</small>
                  <small className="ts-tolerance" data-none={p === "basic" || undefined}>
                    {info.tolerance}
                  </small>
                  {why && <em>{why}</em>}
                </button>
              );
            })}
          </div>
          {profileOk(profile) && (
            <div className="ts-capbar-wrap">
              <div className="ts-capbar" aria-hidden="true">
                <div className="ts-cap-usable" style={{ flex: cap.usable }} />
                {cap.protection > 0 && <div className="ts-cap-prot" style={{ flex: cap.protection }} />}
                {cap.unused > 0 && <div className="ts-cap-unused" style={{ flex: cap.unused }} />}
              </div>
              <div className="ts-cap-legend">
                <span>
                  <i className="ts-cap-usable" /> Útil <b>{fmtBytes(cap.usable)}</b>
                </span>
                {cap.protection > 0 && (
                  <span>
                    <i className="ts-cap-prot" /> Protección <b>{fmtBytes(cap.protection)}</b>
                  </span>
                )}
                {cap.unused > 0 && (
                  <span>
                    <i className="ts-cap-unused" /> Sin usar <b>{fmtBytes(cap.unused)}</b>
                  </span>
                )}
              </div>
              {cap.unused > 0 && (
                <p className="ts-note">
                  Los discos tienen tamaños distintos: {profileName[profile]} usa de cada uno la capacidad del más chico y sobran {fmtBytes(cap.unused)}. El volumen híbrido que aprovecha todo llega en M6.
                </p>
              )}
            </div>
          )}
        </>
      )}

      {step === 2 && (
        <div className="ts-form">
          <label className="ts-field">
            <span>Nombre del volumen</span>
            <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-label="Nombre del volumen" />
            <small>Se monta en /{name || "…"}. Minúsculas, números, guiones.</small>
          </label>
          <div className="ts-field ts-field-row">
            <span>
              Cifrar con LUKS2 <span className="ts-soon">pronto</span>
              <small>Desbloqueo con contraseña o llave en USB. Llega en M4.</small>
            </span>
            <Toggle checked={false} onChange={() => undefined} label="Cifrar" disabled />
          </div>
          <div className="ts-field ts-field-row">
            <span>
              Sistema de archivos
              <small>Btrfs sobre mdadm: instantáneas, checksums y compresión.</small>
            </span>
            <span className="tg-muted">Btrfs</span>
          </div>
        </div>
      )}

      {step === 3 && (
        <>
          <div className="ts-danger">
            <AlertTriangle size={18} />
            <div>
              <strong>Se van a borrar estos discos</strong>
              <p>Todo lo que contienen se pierde. TangoOS vuelve a verificar la identidad de cada disco justo antes de empezar.</p>
            </div>
          </div>
          {planning && !plan && <p className="tg-muted ts-wiz-lead">Armando el plan con el estado actual de los discos…</p>}
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
                          {local?.bay ? `Bahía ${local.bay}` : "Disco"} · {d.model}
                        </strong>
                        <small>
                          Serie {d.serial} · {fmtBytes(d.size)} · {d.id}
                        </small>
                        <small className="ts-erase-content">Contenido: {d.content}</small>
                      </div>
                    </div>
                  );
                })}
              </div>
              {plan.summary.notes?.map((n) => (
                <p key={n} className="ts-note">
                  {n}
                </p>
              ))}
              <div className="ts-summary">
                <span>
                  <Lock size={13} /> {profileName[plan.summary.profile]} · Btrfs · {fmtBytes(plan.summary.usable)} útiles
                </span>
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
          )}
        </>
      )}
    </Sheet>
  );
}
