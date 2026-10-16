import { useEffect, useState } from "react";
import { AlertTriangle, Check, HardDrive, KeyRound, Lock, Unlock, Usb, Zap } from "lucide-react";
import { RpcError, fmtBytes, locale, t, tr, useLive, useTransport } from "@tango/client";
import type { CacheMode, Disk, Pool, PoolPlan, Volume } from "@tango/client";
import { Button, Segmented, Sheet, TextInput } from "@tango/ui";
import { toast } from "@tango/shell";
import { ineligibleReason } from "./util";

const unlockError: Record<string, string> = {
  WRONG_PASSPHRASE: t("La frase no abre este volumen."),
  RATE_LIMITED: t("Demasiados intentos fallidos. Esperá unos minutos."),
  KEY_DEVICE_MISSING: t("No está conectada la llave de este volumen (o no tiene su archivo de clave)."),
  VOLUME_BUSY: t("Algo está usando el volumen."),
};
const msg = (e: unknown) => (e instanceof RpcError ? unlockError[e.code] ?? e.message : (e as Error).message);

/** Tarjeta de un volumen bloqueado, con el botón para desbloquearlo. */
export function LockedCard({ v, onUnlock }: { v: Volume; onUnlock: () => void }) {
  return (
    <section className="ts-locked-card">
      <Lock size={22} />
      <div>
        <strong>{t("{name} está bloqueado", { name: v.name })}</strong>
        <p>{v.keyDevice ? t("Está cifrado y cerrado: sus carpetas no se ven en la red hasta que lo desbloquees (o conectes su llave USB al arrancar).") : t("Está cifrado y cerrado: sus carpetas no se ven en la red hasta que lo desbloquees.")}</p>
      </div>
      <Button variant="primary" onClick={onUnlock}>
        <Unlock size={14} /> {t("Desbloquear…")}
      </Button>
    </section>
  );
}

export function UnlockSheet({ vol, onClose }: { vol: Volume | null; onClose: () => void }) {
  const rpc = useTransport();
  const [pass, setPass] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setPass("");
    setError(null);
  }, [vol]);
  if (!vol) return null;
  const unlock = async (useKeyDevice: boolean) => {
    setBusy(true);
    setError(null);
    try {
      await rpc.call("storage.volumes.unlock", useKeyDevice ? { volume: vol.id, useKeyDevice: true } : { volume: vol.id, passphrase: pass });
      setPass("");
      toast({ level: "ok", title: t("{name} desbloqueado", { name: vol.name }), body: t("Montado en /{name}.", { name: vol.name }) });
      onClose();
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      open={!!vol}
      onClose={onClose}
      width={460}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          {vol.keyDevice && (
            <Button disabled={busy} onClick={() => unlock(true)}>
              <Usb size={14} /> {t("Con la llave USB")}
            </Button>
          )}
          <Button variant="primary" disabled={busy || !pass} onClick={() => unlock(false)}>
            {t("Desbloquear")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Desbloquear {name}", { name: vol.name })}</h2>
      <p className="tg-muted ts-wiz-lead">{t("Escribí la frase de cifrado. TangoOS la usa solo para abrir el volumen: no la guarda.")}</p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (pass) void unlock(false);
        }}
      >
        <label className="ts-field">
          <span>{t("Frase")}</span>
          <TextInput type="password" value={pass} autoFocus onChange={(e) => setPass(e.target.value)} aria-label={t("Frase de cifrado")} autoComplete="current-password" />
        </label>
      </form>
    </Sheet>
  );
}

export function PassphraseSheet({ vol, onClose }: { vol: Volume | null; onClose: () => void }) {
  const rpc = useTransport();
  const [old, setOld] = useState("");
  const [next, setNext] = useState("");
  const [next2, setNext2] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setOld("");
    setNext("");
    setNext2("");
    setError(null);
  }, [vol]);
  if (!vol) return null;
  const ok = old && next.length >= 10 && next === next2 && next !== old;
  const go = async () => {
    setBusy(true);
    setError(null);
    try {
      await rpc.call("storage.volumes.passphrase", { volume: vol.id, old, new: next });
      toast({ level: "ok", title: t("Frase cambiada"), body: t("La frase anterior ya no abre {name}.", { name: vol.name }) });
      onClose();
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      open={!!vol}
      onClose={onClose}
      width={460}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={busy || !ok} onClick={go}>
            {t("Cambiar frase")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Cambiar la frase de {name}", { name: vol.name })}</h2>
      <div className="ts-form">
        <label className="ts-field">
          <span>{t("Frase actual")}</span>
          <TextInput type="password" value={old} onChange={(e) => setOld(e.target.value)} aria-label={t("Frase actual")} autoComplete="current-password" />
        </label>
        <label className="ts-field">
          <span>{t("Frase nueva")}</span>
          <TextInput type="password" value={next} onChange={(e) => setNext(e.target.value)} aria-label={t("Frase nueva")} autoComplete="new-password" />
          <small>{next && next.length < 10 ? t("Al menos 10 caracteres.") : t("La llave USB, si tiene una, sigue funcionando.")}</small>
        </label>
        <label className="ts-field">
          <span>{t("Repetí la frase nueva")}</span>
          <TextInput type="password" value={next2} onChange={(e) => setNext2(e.target.value)} aria-label={t("Repetí la frase nueva")} autoComplete="new-password" />
          {next2 && next !== next2 && <small className="ts-err">{t("Las frases no coinciden.")}</small>}
        </label>
      </div>
    </Sheet>
  );
}

export function KeyDeviceSheet({ vol, onClose }: { vol: Volume | null; onClose: () => void }) {
  const rpc = useTransport();
  const keys = useLive("storage.keyDevices.list", undefined, ["storage.changed"]).data ?? [];
  const [dev, setDev] = useState<string | null>(null);
  const [pass, setPass] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setDev(null);
    setPass("");
    setError(null);
  }, [vol]);
  if (!vol) return null;
  const save = async (device: string | null) => {
    setBusy(true);
    setError(null);
    try {
      await rpc.call("storage.volumes.keyDevice", { volume: vol.id, passphrase: pass, device });
      toast({ level: "ok", title: device ? t("Llave USB guardada") : t("Llave USB quitada"), body: device ? t("Con la llave conectada, {name} se desbloquea solo al arrancar.", { name: vol.name }) : t("{name} solo se abre con la frase.", { name: vol.name }) });
      onClose();
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      open={!!vol}
      onClose={onClose}
      width={500}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          {vol.keyDevice && (
            <Button variant="danger" disabled={busy || !pass} onClick={() => save(null)}>
              {t("Quitar la llave")}
            </Button>
          )}
          <Button variant="primary" disabled={busy || !pass || !dev} onClick={() => save(dev)}>
            {t("Guardar en la llave")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Llave USB de {name}", { name: vol.name })}</h2>
      <p className="tg-muted ts-wiz-lead">
        {vol.kind === "zfs"
          ? t("TangoOS guarda en el pendrive una clave aleatoria y en el volumen la frase cifrada para esa clave: la frase sigue abriendo el volumen en cualquier equipo con ZFS. Si el pendrive está conectado al arrancar, el volumen se desbloquea solo. Guardalo como guardarías la frase.")
          : t("TangoOS guarda en el pendrive un archivo de clave aleatorio (una ranura más de LUKS). Si el pendrive está conectado al arrancar, el volumen se desbloquea solo. Guardalo como guardarías la frase.")}
      </p>
      {vol.kind === "zfs" && vol.keyDevice && <p className="ts-note">{t("Si perdiste el pendrive: quitá la llave y después cambiá la frase. Así ninguna copia vieja de la llave abre el volumen.")}</p>}
      <div className="ts-form">
        <div className="ts-key-list">
          {keys.length === 0 && <small className="tg-muted">{t("Conectá un pendrive con FAT, exFAT o ext4.")}</small>}
          {keys.map((k) => (
            <button key={k.uuid} type="button" className="ts-pick-disk" aria-pressed={dev === k.uuid} onClick={() => setDev(dev === k.uuid ? null : k.uuid)}>
              <span className="ts-pick-check">{dev === k.uuid && <Check size={12} strokeWidth={3} />}</span>
              <Usb size={20} strokeWidth={1.6} />
              <span className="ts-pick-text">
                <strong>
                  {k.label || t("Sin etiqueta")} {vol.keyDevice?.toLowerCase() === k.uuid.toLowerCase() && <span className="ts-soon">{t("la actual")}</span>}
                </strong>
                <small>
                  {k.fs} · {fmtBytes(k.size)} · {k.uuid}
                </small>
              </span>
            </button>
          ))}
        </div>
        <label className="ts-field">
          <span>{t("Frase del volumen")}</span>
          <TextInput type="password" value={pass} onChange={(e) => setPass(e.target.value)} aria-label={t("Frase del volumen")} autoComplete="current-password" />
          <small>{t("Hace falta para autorizar el cambio.")}</small>
        </label>
      </div>
    </Sheet>
  );
}

// ---- Caché SSD ----------------------------------------------------------------------

export function CacheStats({ v }: { v: Volume }) {
  const c = v.cache;
  if (!c) return null;
  const reads = c.readHits + c.readMisses;
  return (
    <div className="ts-cache-stats">
      <div className="ts-cache-stat">
        <small>{t("Aciertos de lectura")}</small>
        <strong>{c.hitRatio == null ? "—" : `${Math.round(c.hitRatio * 100)} %`}</strong>
      </div>
      <div className="ts-cache-stat">
        <small>{t("Lecturas desde la SSD")}</small>
        <strong>{reads ? t("{used} de {size}", { used: c.readHits.toLocaleString(locale()), size: reads.toLocaleString(locale()) }) : "—"}</strong>
      </div>
      <div className="ts-cache-stat">
        <small>{t("Ocupado")}</small>
        <strong>
          {t("{used} de {size}", { used: fmtBytes(c.used), size: fmtBytes(c.size) })}
        </strong>
      </div>
      {c.mode === "readwrite" && (
        <div className="ts-cache-stat">
          <small>{t("Sin bajar a los discos")}</small>
          <strong>{fmtBytes(c.dirty)}</strong>
        </div>
      )}
    </div>
  );
}

export function CacheSheet({ vol, disks, vols, pools, onClose }: { vol: Volume | null; disks: Disk[]; vols: Volume[]; pools: Pool[]; onClose: () => void }) {
  const rpc = useTransport();
  const [mode, setMode] = useState<CacheMode>("read");
  const [sel, setSel] = useState<string[]>([]);
  const [plan, setPlan] = useState<PoolPlan | null>(null);
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setMode("read");
    setSel([]);
    setPlan(null);
    setConfirm("");
    setError(null);
  }, [vol]);
  if (!vol) return null;
  const ssds = disks.filter((d) => d.kind !== "hdd" && d.use !== "system");
  const need = mode === "readwrite" ? sel.length === 2 : sel.length === 1 || sel.length === 2;
  const review = async () => {
    setBusy(true);
    setError(null);
    try {
      setPlan(await rpc.call("storage.cache.plan", { volume: vol.id, disks: sel, mode }));
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  };
  const create = async () => {
    if (!plan) return;
    setBusy(true);
    setError(null);
    try {
      await rpc.call("storage.cache.create", { plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: confirm });
      toast({ level: "info", title: t("Agregando caché a {name}", { name: vol.name }), body: t("El volumen sigue disponible mientras tanto.") });
      onClose();
    } catch (e) {
      setError(msg(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      open={!!vol}
      onClose={onClose}
      width={560}
      footer={
        <>
          {error && (
            <span className="ts-wiz-error" role="alert">
              <AlertTriangle size={14} /> {error}
            </span>
          )}
          <Button onClick={plan ? () => setPlan(null) : onClose}>{plan ? t("Atrás") : t("Cancelar")}</Button>
          {plan ? (
            <Button variant="danger" disabled={busy || confirm !== vol.name} onClick={create}>
              {t("Borrar las SSD y agregar la caché")}
            </Button>
          ) : (
            <Button variant="primary" disabled={busy || !need} onClick={review}>
              {t("Continuar")}
            </Button>
          )}
        </>
      }
    >
      <h2 className="tg-h2">{t("Caché SSD para {name}", { name: vol.name })}</h2>
      {!plan ? (
        <div className="ts-form">
          <p className="tg-muted ts-wiz-lead">{t("Una SSD delante de los discos acelera lo que más se usa. Se puede quitar en cualquier momento.")}</p>
          <div className="ts-field">
            <span>{t("Modo")}</span>
            <Segmented
              label={t("Modo de caché")}
              value={mode}
              onChange={setMode}
              options={[
                { value: "read", label: t("Solo lectura") },
                { value: "readwrite", label: t("Lectura y escritura") },
              ]}
            />
            <small>{mode === "read" ? t("1 SSD (o 2 en espejo). Lo que se escribe va igual a los discos: no hay riesgo.") : t("2 SSD en espejo (RAID 1). Más rápido para escribir; si fallan las dos a la vez se pierde lo que no bajó a los discos.")}</small>
          </div>
          <div className="ts-pick">
            {ssds.length === 0 && <small className="tg-muted">{t("No hay SSD ni NVMe libres.")}</small>}
            {ssds.map((d) => {
              const reason = ineligibleReason(d, vols, pools);
              const on = sel.includes(d.id);
              return (
                <button key={d.id} type="button" className="ts-pick-disk" aria-pressed={on} disabled={!!reason} onClick={() => setSel((s) => (on ? s.filter((x) => x !== d.id) : [...s, d.id]))}>
                  <span className="ts-pick-check">{on && <Check size={12} strokeWidth={3} />}</span>
                  <Zap size={20} strokeWidth={1.6} />
                  <span className="ts-pick-text">
                    <strong>
                      {d.bay ? t("Bahía {n}", { n: d.bay }) : t("Interno")} · {d.model}
                    </strong>
                    <small>
                      {fmtBytes(d.size)} · {d.kind === "nvme" ? "NVMe" : "SSD"} · {t("serie {serial}", { serial: d.serial })}
                    </small>
                    {reason && <em>{reason}</em>}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      ) : (
        <>
          <div className="ts-danger">
            <AlertTriangle size={18} />
            <div>
              <strong>{t("Se van a borrar estas SSD")}</strong>
              <p>{t("Todo lo que contienen se pierde.")}</p>
            </div>
          </div>
          <div className="ts-erase">
            {plan.summary.disks.map((d) => (
              <div key={d.id} className="ts-erase-row">
                <HardDrive size={18} strokeWidth={1.6} />
                <div>
                  <strong>{d.model}</strong>
                  <small>
                    {t("Serie {serial} · {size}", { serial: d.serial, size: fmtBytes(d.size) })}
                  </small>
                  <small className="ts-erase-content">{t("Contenido: {content}", { content: tr(d.content) })}</small>
                </div>
              </div>
            ))}
          </div>
          {plan.summary.notes?.map((n) => (
            <p key={n} className="ts-note">
              {tr(n)}
            </p>
          ))}
          <label className="ts-field">
            <span>
              {t("Para confirmar, escribí")} <code>{vol.name}</code>
            </span>
            <TextInput value={confirm} onChange={(e) => setConfirm(e.target.value)} aria-label={t("Escribí {name} para confirmar", { name: vol.name })} autoComplete="off" />
          </label>
        </>
      )}
    </Sheet>
  );
}

export const lockIcon = (v: Volume) => (v.encrypted ? (v.locked ? Lock : KeyRound) : null);
