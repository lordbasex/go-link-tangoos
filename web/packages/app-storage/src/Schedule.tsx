// Programación de instantáneas por carpeta compartida (protocol/README.md §11).
import { useEffect, useState } from "react";
import { RpcError, defaultSchedule, useTransport } from "@tango/client";
import type { SnapshotSchedule } from "@tango/client";
import { Button, Sheet, Toggle } from "@tango/ui";
import { toast } from "@tango/shell";

const days = ["domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"];
const everyOptions = [1, 2, 4, 6, 12] as const;

/** "Cada 4 h (6) · diarias 03:00 (14) · semanales los domingos (8)" o "Sin programar". */
export function scheduleSummary(s: SnapshotSchedule): string {
  if (!s.enabled) return "Sin programar";
  const parts: string[] = [];
  if (s.hourly.keep > 0) parts.push(`cada ${s.hourly.every} h (se guardan ${s.hourly.keep})`);
  if (s.daily.keep > 0) parts.push(`diarias ${s.daily.at} (${s.daily.keep})`);
  if (s.weekly.keep > 0) parts.push(`semanales los ${days[s.weekly.day]} (${s.weekly.keep})`);
  return parts.length ? `Programadas: ${parts.join(" · ")}` : "Programación activa sin niveles";
}

function Keep({ value, onChange, disabled, label }: { value: number; onChange: (n: number) => void; disabled: boolean; label: string }) {
  return (
    <label className="ts-sched-ctl" data-off={disabled || undefined}>
      Conservar
      <input type="number" min={1} max={500} value={value} disabled={disabled} aria-label={label} onChange={(e) => onChange(Math.max(1, Math.min(500, Math.round(Number(e.target.value) || 1))))} />
    </label>
  );
}

export function ScheduleSheet({ share, onClose }: { share: { id: string; name: string } | null; onClose: () => void }) {
  const t = useTransport();
  const [s, setS] = useState<SnapshotSchedule | null>(null);
  const [saving, setSaving] = useState(false);
  // Al desactivar un nivel recordamos cuántas se conservaban, para volver a activarlo igual.
  const [memo, setMemo] = useState({ hourly: 6, daily: 14, weekly: 8 });

  useEffect(() => {
    setS(null);
    if (!share) return;
    t.call("snapshots.schedule.get", { share: share.id }).then(
      (r) => {
        setS(r);
        setMemo({ hourly: r.hourly.keep || 6, daily: r.daily.keep || 14, weekly: r.weekly.keep || 8 });
      },
      () => setS(structuredClone(defaultSchedule)),
    );
  }, [t, share]);

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
      await t.call("snapshots.schedule.set", { share: share.id, schedule: s });
      toast({ level: "ok", title: "Programación guardada", body: `${share.name}: ${scheduleSummary(s).replace(/^Programadas: /, "")}` });
      onClose();
    } catch (e) {
      toast({ level: "warn", title: "No se pudo guardar", body: e instanceof RpcError && e.code === "INVALID_PARAMS" ? "Revisá las horas y cantidades." : (e as Error).message });
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
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="primary" disabled={!s || saving} onClick={save}>
            Guardar
          </Button>
        </>
      }
    >
      <div className="ts-wiz-head">
        <h2 className="tg-h2">Instantáneas de {share.name}</h2>
      </div>
      {!s ? (
        <p className="tg-muted ts-wiz-lead">Leyendo la programación…</p>
      ) : (
        <>
          <div className="ts-sched-row" style={{ borderTop: 0 }}>
            <div>
              <strong>Instantáneas automáticas</strong>
              <small>TangoOS las toma solo y borra las más viejas de cada nivel.</small>
            </div>
            <Toggle checked={s.enabled} onChange={(on) => setS({ ...s, enabled: on })} label="Instantáneas automáticas" />
          </div>
          <div className="ts-sched" aria-disabled={!s.enabled} style={!s.enabled ? { opacity: 0.5, pointerEvents: "none" } : undefined}>
            <div className="ts-sched-row">
              <div>
                <strong>Cada algunas horas</strong>
                <span className="ts-sched-ctl" data-off={!s.hourly.keep || undefined}>
                  Cada
                  <select value={s.hourly.every} disabled={!s.hourly.keep} aria-label="Frecuencia" onChange={(e) => patch("hourly", { every: Number(e.target.value) as SnapshotSchedule["hourly"]["every"] })}>
                    {everyOptions.map((h) => (
                      <option key={h} value={h}>
                        {h} {h === 1 ? "hora" : "horas"}
                      </option>
                    ))}
                  </select>
                </span>
                <Keep value={s.hourly.keep || memo.hourly} disabled={!s.hourly.keep} onChange={(n) => patch("hourly", { keep: n })} label="Instantáneas horarias a conservar" />
              </div>
              <Toggle checked={s.hourly.keep > 0} onChange={(on) => level("hourly", on)} label="Instantáneas horarias" />
            </div>
            <div className="ts-sched-row">
              <div>
                <strong>Una por día</strong>
                <span className="ts-sched-ctl" data-off={!s.daily.keep || undefined}>
                  A las
                  <input type="time" value={s.daily.at} disabled={!s.daily.keep} aria-label="Hora diaria" onChange={(e) => patch("daily", { at: e.target.value || "03:00" })} />
                </span>
                <Keep value={s.daily.keep || memo.daily} disabled={!s.daily.keep} onChange={(n) => patch("daily", { keep: n })} label="Instantáneas diarias a conservar" />
              </div>
              <Toggle checked={s.daily.keep > 0} onChange={(on) => level("daily", on)} label="Instantáneas diarias" />
            </div>
            <div className="ts-sched-row">
              <div>
                <strong>Una por semana</strong>
                <span className="ts-sched-ctl" data-off={!s.weekly.keep || undefined}>
                  Los
                  <select value={s.weekly.day} disabled={!s.weekly.keep} aria-label="Día de la semana" onChange={(e) => patch("weekly", { day: Number(e.target.value) })}>
                    {days.map((d, i) => (
                      <option key={d} value={i}>
                        {d}
                      </option>
                    ))}
                  </select>
                  a las
                  <input type="time" value={s.weekly.at} disabled={!s.weekly.keep} aria-label="Hora semanal" onChange={(e) => patch("weekly", { at: e.target.value || "03:30" })} />
                </span>
                <Keep value={s.weekly.keep || memo.weekly} disabled={!s.weekly.keep} onChange={(n) => patch("weekly", { keep: n })} label="Instantáneas semanales a conservar" />
              </div>
              <Toggle checked={s.weekly.keep > 0} onChange={(on) => level("weekly", on)} label="Instantáneas semanales" />
            </div>
          </div>
          <p className="ts-sched-total">
            {s.enabled ? `Se conservan hasta ${total} instantáneas programadas. Las manuales y las protegidas no se borran solas.` : "Sin programación: solo instantáneas manuales."}
          </p>
        </>
      )}
    </Sheet>
  );
}
