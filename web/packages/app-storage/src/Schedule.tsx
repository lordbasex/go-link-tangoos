// Programación de instantáneas por carpeta compartida (protocol/README.md §11).
import { useEffect, useState } from "react";
import { RpcError, defaultSchedule, t, tn, useTransport } from "@tango/client";
import type { SnapshotSchedule } from "@tango/client";
import { Button, Sheet, Toggle } from "@tango/ui";
import { toast } from "@tango/shell";

const days = [t("domingo"), t("lunes"), t("martes"), t("miércoles"), t("jueves"), t("viernes"), t("sábado")];
const everyOptions = [1, 2, 4, 6, 12] as const;

/** "Cada 4 h (6) · diarias 03:00 (14) · semanales los domingos (8)" o "Sin programar". */
export function scheduleSummary(s: SnapshotSchedule): string {
  if (!s.enabled) return t("Sin programar");
  const parts = scheduleParts(s);
  return parts ? t("Programadas: {list}", { list: parts }) : t("Programación activa sin niveles");
}

/** Los niveles activos, sin el prefijo "Programadas:" ("" si no hay ninguno). */
function scheduleParts(s: SnapshotSchedule): string {
  const parts: string[] = [];
  if (s.hourly.keep > 0) parts.push(t("cada {every} h (se guardan {keep})", { every: s.hourly.every, keep: s.hourly.keep }));
  if (s.daily.keep > 0) parts.push(t("diarias {at} ({keep})", { at: s.daily.at, keep: s.daily.keep }));
  if (s.weekly.keep > 0) parts.push(t("semanales los {day} ({keep})", { day: days[s.weekly.day], keep: s.weekly.keep }));
  return parts.join(" · ");
}

function Keep({ value, onChange, disabled, label }: { value: number; onChange: (n: number) => void; disabled: boolean; label: string }) {
  return (
    <label className="ts-sched-ctl" data-off={disabled || undefined}>
      {t("Conservar")}
      <input type="number" min={1} max={500} value={value} disabled={disabled} aria-label={label} onChange={(e) => onChange(Math.max(1, Math.min(500, Math.round(Number(e.target.value) || 1))))} />
    </label>
  );
}

export function ScheduleSheet({ share, onClose }: { share: { id: string; name: string } | null; onClose: () => void }) {
  const rpc = useTransport();
  const [s, setS] = useState<SnapshotSchedule | null>(null);
  const [saving, setSaving] = useState(false);
  // Al desactivar un nivel recordamos cuántas se conservaban, para volver a activarlo igual.
  const [memo, setMemo] = useState({ hourly: 6, daily: 14, weekly: 8 });

  useEffect(() => {
    setS(null);
    if (!share) return;
    rpc.call("snapshots.schedule.get", { share: share.id }).then(
      (r) => {
        setS(r);
        setMemo({ hourly: r.hourly.keep || 6, daily: r.daily.keep || 14, weekly: r.weekly.keep || 8 });
      },
      () => setS(structuredClone(defaultSchedule)),
    );
  }, [rpc, share]);

  if (!share) return null;

  const level = (k: "hourly" | "daily" | "weekly", on: boolean) =>
    setS((x) => {
      if (!x) return x;
      if (!on) setMemo((m) => ({ ...m, [k]: x[k].keep || m[k] }));
      return { ...x, [k]: { ...x[k], keep: on ? memo[k] : 0 } };
    });
  const patch = <K extends "hourly" | "daily" | "weekly">(k: K, p: Partial<SnapshotSchedule[K]>) => setS((x) => (x ? { ...x, [k]: { ...x[k], ...p } } : x));

  const total = s ? (s.hourly.keep || 0) + (s.daily.keep || 0) + (s.weekly.keep || 0) : 0;

  const save = async () => {
    if (!s) return;
    setSaving(true);
    try {
      await rpc.call("snapshots.schedule.set", { share: share.id, schedule: s });
      toast({ level: "ok", title: t("Programación guardada"), body: `${share.name}: ${s.enabled ? scheduleParts(s) || t("Programación activa sin niveles") : t("Sin programar")}` });
      onClose();
    } catch (e) {
      toast({ level: "warn", title: t("No se pudo guardar"), body: e instanceof RpcError && e.code === "INVALID_PARAMS" ? t("Revisá las horas y cantidades.") : (e as Error).message });
    } finally {
      setSaving(false);
    }
  };

  return (
    <Sheet
      open
      onClose={onClose}
      width={520}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={!s || saving} onClick={save}>
            {t("Guardar")}
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">{t("Instantáneas de {name}", { name: share.name })}</h2>
      </div>
      {!s ? (
        <p className="tg-muted ts-wiz-lead">{t("Leyendo la programación…")}</p>
      ) : (
        <>
          <div className="ts-sched-row" style={{ borderTop: 0 }}>
            <div>
              <strong>{t("Instantáneas automáticas")}</strong>
              <small>{t("TangoOS las toma solo y borra las más viejas de cada nivel.")}</small>
            </div>
            <Toggle checked={s.enabled} onChange={(on) => setS({ ...s, enabled: on })} label={t("Instantáneas automáticas")} />
          </div>
          <div className="ts-sched" aria-disabled={!s.enabled} style={!s.enabled ? { opacity: 0.5, pointerEvents: "none" } : undefined}>
            <div className="ts-sched-row">
              <div>
                <strong>{t("Cada algunas horas")}</strong>
                <span className="ts-sched-ctl" data-off={!s.hourly.keep || undefined}>
                  {t("Cada")}
                  <select value={s.hourly.every} disabled={!s.hourly.keep} aria-label={t("Frecuencia")} onChange={(e) => patch("hourly", { every: Number(e.target.value) as SnapshotSchedule["hourly"]["every"] })}>
                    {everyOptions.map((h) => (
                      <option key={h} value={h}>
                        {tn(h, "{n} hora", "{n} horas")}
                      </option>
                    ))}
                  </select>
                </span>
                <Keep value={s.hourly.keep || memo.hourly} disabled={!s.hourly.keep} onChange={(n) => patch("hourly", { keep: n })} label={t("Instantáneas horarias a conservar")} />
              </div>
              <Toggle checked={s.hourly.keep > 0} onChange={(on) => level("hourly", on)} label={t("Instantáneas horarias")} />
            </div>
            <div className="ts-sched-row">
              <div>
                <strong>{t("Una por día")}</strong>
                <span className="ts-sched-ctl" data-off={!s.daily.keep || undefined}>
                  {t("A las")}
                  <input type="time" value={s.daily.at} disabled={!s.daily.keep} aria-label={t("Hora diaria")} onChange={(e) => patch("daily", { at: e.target.value || "03:00" })} />
                </span>
                <Keep value={s.daily.keep || memo.daily} disabled={!s.daily.keep} onChange={(n) => patch("daily", { keep: n })} label={t("Instantáneas diarias a conservar")} />
              </div>
              <Toggle checked={s.daily.keep > 0} onChange={(on) => level("daily", on)} label={t("Instantáneas diarias")} />
            </div>
            <div className="ts-sched-row">
              <div>
                <strong>{t("Una por semana")}</strong>
                <span className="ts-sched-ctl" data-off={!s.weekly.keep || undefined}>
                  {t("Los")}
                  <select value={s.weekly.day} disabled={!s.weekly.keep} aria-label={t("Día de la semana")} onChange={(e) => patch("weekly", { day: Number(e.target.value) })}>
                    {days.map((d, i) => (
                      <option key={d} value={i}>
                        {d}
                      </option>
                    ))}
                  </select>
                  {t("a las")}
                  <input type="time" value={s.weekly.at} disabled={!s.weekly.keep} aria-label={t("Hora semanal")} onChange={(e) => patch("weekly", { at: e.target.value || "03:30" })} />
                </span>
                <Keep value={s.weekly.keep || memo.weekly} disabled={!s.weekly.keep} onChange={(n) => patch("weekly", { keep: n })} label={t("Instantáneas semanales a conservar")} />
              </div>
              <Toggle checked={s.weekly.keep > 0} onChange={(on) => level("weekly", on)} label={t("Instantáneas semanales")} />
            </div>
          </div>
          <p className="ts-sched-total">
            {s.enabled ? t("Se conservan hasta {n} instantáneas programadas. Las manuales y las protegidas no se borran solas.", { n: total }) : t("Sin programación: solo instantáneas manuales.")}
          </p>
        </>
      )}
    </Sheet>
  );
}
