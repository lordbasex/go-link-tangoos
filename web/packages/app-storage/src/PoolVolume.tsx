import { useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, Database, KeyRound, ShieldAlert, Usb, XOctagon } from "lucide-react";
import { RpcError, fmtBytes, poolProfileLabel, useJobs, useLive, useTransport } from "@tango/client";
import type { Pool, PoolFS, Volume } from "@tango/client";
import { Button, Progress, Segmented, Sheet, TextInput, Toggle } from "@tango/ui";
import { toast } from "@tango/shell";

const GiB = 1024 ** 3;
const fsHelp: Record<PoolFS, string> = {
  btrfs: "Instantáneas, checksums y carpetas compartidas. El recomendado.",
  xfs: "Archivos muy grandes y mucha concurrencia (máquinas virtuales, bricks de un sistema distribuido). Sin carpetas compartidas ni instantáneas.",
  ext4: "El clásico de Linux, para usos especiales. Sin carpetas compartidas ni instantáneas.",
};

/** Sigue un job hasta que termina (mismo criterio que el asistente de volúmenes). */
function useJobOutcome(jobId: string | null) {
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

/** Tamaño en GiB con un control deslizante y un campo; max en bytes. */
function SizePicker({ value, max, min = 0.25 * GiB, onChange }: { value: number; max: number; min?: number; onChange: (v: number) => void }) {
  const gb = (n: number) => Math.round((n / GiB) * 10) / 10;
  return (
    <div className="ts-size-row">
      <input type="range" min={min} max={max} step={0.25 * GiB} value={Math.min(value, max)} onChange={(e) => onChange(Number(e.target.value))} aria-label="Tamaño" />
      <TextInput
        value={String(gb(value))}
        inputMode="decimal"
        aria-label="Tamaño en GB"
        onChange={(e) => {
          const n = Number(e.target.value.replace(",", "."));
          if (!Number.isNaN(n)) onChange(Math.min(max, Math.max(0, n * GiB)));
        }}
      />
      <span className="tg-muted">GB</span>
    </div>
  );
}

export function CreatePoolVolume({ open, onClose, pools, vols, initialPool }: { open: boolean; onClose: () => void; pools: Pool[]; vols: Volume[]; initialPool?: string | null }) {
  const t = useTransport();
  const keys = useLive("storage.keyDevices.list", undefined, ["storage.changed"]).data ?? [];
  const usable = pools.filter((p) => p.free >= 0.25 * GiB);
  const [poolId, setPoolId] = useState<string>("");
  const [name, setName] = useState("");
  const [size, setSize] = useState(0);
  const [all, setAll] = useState(true);
  const [fs, setFs] = useState<PoolFS>("btrfs");
  const [enc, setEnc] = useState(false);
  const [pass, setPass] = useState("");
  const [pass2, setPass2] = useState("");
  const [key, setKey] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [jobId, setJobId] = useState<string | null>(null);
  const { job, failed, done } = useJobOutcome(jobId);

  const pool = pools.find((p) => p.id === poolId);
  useEffect(() => {
    if (!open) return;
    const first = (initialPool && pools.find((p) => p.id === initialPool || p.name === initialPool)) || usable[0];
    setPoolId(first?.id ?? "");
    let n = vols.length + 1;
    while (vols.some((v) => v.name === `volume${n}`)) n++;
    setName(`volume${n}`);
    setAll(true);
    setSize(first?.free ?? 0);
    setFs("btrfs");
    setEnc(false);
    setPass("");
    setPass2("");
    setKey(null);
    setError(null);
    setJobId(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const nameOk = /^[a-z][a-z0-9]{0,31}$/.test(name) && !vols.some((v) => v.name === name) && !pools.some((p) => p.name === name);
  const want = all ? pool?.free ?? 0 : size;
  const passOk = !enc || (pass.length >= 10 && pass === pass2);
  const can = !!pool && nameOk && want >= 0.25 * GiB && want <= (pool?.free ?? 0) && passOk && !busy;

  const create = async () => {
    if (!pool) return;
    setBusy(true);
    setError(null);
    try {
      const r = await t.call("storage.poolVolumes.create", {
        pool: pool.id, name, size: all ? 0 : Math.floor(want), fs, encryption: enc ? { passphrase: pass, keyDevice: key } : null,
      });
      setPass("");
      setPass2("");
      setJobId(r.job);
    } catch (e) {
      setError(e instanceof RpcError ? e.message : (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const close = () => {
    if (jobId && !done && !failed) toast({ level: "info", title: `${name} se sigue creando`, body: "Podés ver el progreso en Actividad." });
    onClose();
  };

  if (jobId) {
    return (
      <Sheet open={open} onClose={close} width={500} footer={<Button variant="primary" onClick={close}>{done || failed ? "Cerrar" : "Seguir en segundo plano"}</Button>}>
        <div className="ts-wiz-done">
          {done ? <CheckCircle2 size={44} className="ts-ok-icon" /> : failed ? <XOctagon size={44} className="ts-crit-icon" /> : <Database size={40} className="ts-muted-icon" />}
          <h2 className="tg-h2">{done ? `${name} está listo` : failed ? `No se pudo crear ${name}` : `Creando ${name}…`}</h2>
          <p className="tg-muted">
            {done
              ? `${fmtBytes(want)} en el grupo ${pool?.name}, montado en /${name}.${enc ? " Está cifrado: sin la frase (o la llave) no se recuperan los datos." : ""}`
              : failed
                ? `La tarea falló. ${job?.detail ?? ""}`
                : enc
                  ? "Se crea el volumen lógico, se cifra con LUKS2 (argon2id), se formatea y se monta."
                  : "Se crea el volumen lógico, se formatea y se monta."}
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
      width={560}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={close}>Cancelar</Button>
          <Button variant="primary" disabled={!can} onClick={create}>
            Crear volumen
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Crear volumen</h2>
      </div>
      {!usable.length ? (
        <p className="tg-muted ts-wiz-lead">No hay grupos con espacio libre. Creá un grupo de almacenamiento o ampliá uno existente.</p>
      ) : (
        <div className="ts-form">
          <label className="ts-field">
            <span>Grupo de almacenamiento</span>
            {usable.length > 1 ? (
              <Segmented
                label="Grupo"
                value={poolId}
                onChange={(id) => {
                  setPoolId(id);
                  setSize(pools.find((p) => p.id === id)?.free ?? 0);
                }}
                options={usable.map((p) => ({ value: p.id, label: p.name }))}
              />
            ) : (
              <strong className="tg-num">{pool?.name}</strong>
            )}
            {pool && (
              <small>
                {poolProfileLabel(pool.profile)} · {fmtBytes(pool.free)} libres de {fmtBytes(pool.size)}
              </small>
            )}
          </label>
          <label className="ts-field">
            <span>Nombre</span>
            <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} aria-label="Nombre del volumen" />
            <small>{nameOk || !name ? `Se monta en /${name || "…"}. Minúsculas y números.` : "Ese nombre no está disponible o no es válido."}</small>
          </label>
          <div className="ts-field ts-field-row">
            <span>
              Usar todo el espacio libre
              <small>{all ? `${fmtBytes(pool?.free ?? 0)}. Se puede agrandar después, sin desmontar.` : "Elegí el tamaño: el resto queda libre en el grupo para otros volúmenes."}</small>
            </span>
            <Toggle checked={all} onChange={setAll} label="Usar todo el espacio libre" />
          </div>
          {!all && pool && <SizePicker value={size} max={pool.free} onChange={setSize} />}
          <div className="ts-field">
            <span>Sistema de archivos</span>
            <Segmented label="Sistema de archivos" value={fs} onChange={setFs} options={[{ value: "btrfs", label: "Btrfs" }, { value: "xfs", label: "XFS" }, { value: "ext4", label: "ext4" }]} />
            <small>{fsHelp[fs]}</small>
          </div>
          <div className="ts-field ts-field-row">
            <span>
              Cifrar con LUKS2
              <small>Los datos quedan ilegibles sin la frase. Se desbloquea desde acá o, sola al arrancar, con una llave USB.</small>
            </span>
            <Toggle checked={enc} onChange={setEnc} label="Cifrar" />
          </div>
          {enc && (
            <>
              <label className="ts-field">
                <span>Frase de cifrado</span>
                <TextInput type="password" value={pass} onChange={(e) => setPass(e.target.value)} aria-label="Frase de cifrado" autoComplete="new-password" />
                <small>{pass && pass.length < 10 ? "Al menos 10 caracteres." : "Una frase larga es más segura que una contraseña corta."}</small>
              </label>
              <label className="ts-field">
                <span>Repetí la frase</span>
                <TextInput type="password" value={pass2} onChange={(e) => setPass2(e.target.value)} aria-label="Repetí la frase" autoComplete="new-password" />
                {pass2 && pass !== pass2 && <small className="ts-err">Las frases no coinciden.</small>}
              </label>
              <div className="ts-field">
                <span>Llave USB (opcional)</span>
                <div className="ts-key-list">
                  {keys.length === 0 && <small>Conectá un pendrive con FAT, exFAT o ext4 para guardar un archivo de clave.</small>}
                  {keys.map((k) => (
                    <button key={k.uuid} type="button" className="ts-pick-disk" aria-pressed={key === k.uuid} onClick={() => setKey(key === k.uuid ? null : k.uuid)}>
                      <span className="ts-pick-check">{key === k.uuid && <CheckCircle2 size={12} />}</span>
                      <Usb size={20} strokeWidth={1.6} />
                      <span className="ts-pick-text">
                        <strong>{k.label || "Sin etiqueta"}</strong>
                        <small>
                          {k.fs} · {fmtBytes(k.size)} · {k.uuid}
                        </small>
                      </span>
                    </button>
                  ))}
                </div>
              </div>
              <div className="ts-key-hint">
                <ShieldAlert size={16} />
                <span>Guardá la frase en un lugar seguro. Si la perdés y no tenés la llave, TangoOS no puede recuperar los datos: nadie puede.</span>
              </div>
            </>
          )}
        </div>
      )}
    </Sheet>
  );
}

export function ResizeVolume({ vol, pools, onClose }: { vol: Volume | null; pools: Pool[]; onClose: () => void }) {
  const t = useTransport();
  const pool = pools.find((p) => p.id === vol?.pool);
  const [size, setSize] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (vol) {
      setSize(vol.size);
      setError(null);
    }
  }, [vol]);
  if (!vol) return null;
  const max = vol.size + (pool?.free ?? 0);
  const go = async () => {
    setBusy(true);
    setError(null);
    try {
      await t.call("storage.poolVolumes.resize", { volume: vol.id, size: Math.floor(size) });
      toast({ level: "info", title: `Agrandando ${vol.name}`, body: "Sigue montado mientras tanto." });
      onClose();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      open={!!vol}
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
          <Button variant="primary" disabled={busy || size <= vol.size + 4 * 1024 ** 2 || size > max} onClick={go}>
            Agrandar
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">Agrandar {vol.name}</h2>
      <p className="tg-muted ts-wiz-lead">
        Hoy tiene {fmtBytes(vol.size)}; el grupo {pool?.name} tiene {fmtBytes(pool?.free ?? 0)} libres. Se agranda en línea, sin desmontar. Achicar no se ofrece.
      </p>
      <SizePicker value={size} min={vol.size} max={max} onChange={setSize} />
      {vol.encrypted && (
        <p className="ts-note">
          <KeyRound size={12} /> Está cifrado: el contenedor LUKS también se agranda.
        </p>
      )}
    </Sheet>
  );
}
