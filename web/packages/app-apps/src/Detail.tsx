import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, CheckCircle2, Info, XOctagon } from "lucide-react";
import { fmtBytes, useLive, useTransport } from "@tango/client";
import type { CatalogApp, CatalogOption } from "@tango/client";
import { Button, Group, Progress, Row, TextInput, Toggle } from "@tango/ui";
import { openApp } from "@tango/shell";
import type { AppsCtx } from "./App";
import { GetButton } from "./Discover";
import { StatePill, UninstallSheet } from "./Installed";
import { isActive, isFailed } from "./jobs";
import type { TrackedJob } from "./jobs";

type Values = Record<string, string | number | boolean>;

function initial(opts: CatalogOption[]): Values {
  const v: Values = {};
  for (const o of opts) v[o.key] = o.default ?? (o.type === "bool" ? false : "");
  return v;
}

function validate(o: CatalogOption, v: string | number | boolean): string | null {
  if (o.type === "bool") return null;
  const s = String(v ?? "").trim();
  if (o.required && !s) return "Obligatorio";
  if (o.type === "port" && s) {
    const n = Number(s);
    if (!Number.isInteger(n) || n < 1 || n > 65535) return "Un puerto entre 1 y 65535";
  }
  if (o.type === "password" && s && s.length < 8) return "Al menos 8 caracteres";
  return null;
}

/** Caja estilo terminal con las líneas del job (job.detail). */
export function JobLog({ tj }: { tj: TrackedJob }) {
  const failed = isFailed(tj);
  const done = tj.job.state === "succeeded";
  return (
    <div className="tapp-job" data-state={tj.job.state}>
      <div className="tapp-job-head">
        {failed ? <XOctagon size={14} color="var(--tg-crit)" /> : done ? <CheckCircle2 size={14} color="var(--tg-ok)" /> : null}
        <strong>{tj.job.title || "Preparando…"}</strong>
        <span className="tg-num tg-muted">{Math.round(tj.job.progress * 100)} %</span>
      </div>
      <Progress value={tj.job.progress} tone={failed ? "crit" : done ? "ok" : undefined} indeterminate={tj.job.state === "queued" || tj.job.state === "validating"} />
      <pre className="tapp-term" aria-live="polite">
        {(tj.lines.length ? tj.lines : [tj.job.detail]).map((l, i) => (
          <div key={i}>
            <span className="tapp-term-prompt">›</span> {l}
          </div>
        ))}
      </pre>
    </div>
  );
}

export function Detail({ app, ctx, onUninstalled }: { app: CatalogApp; ctx: AppsCtx; onUninstalled: () => void }) {
  const t = useTransport();
  const vols = (useLive("storage.volumes.list", undefined, ["storage.changed"]).data ?? []).filter((v) => v.state !== "readonly");
  const inst = ctx.installed.find((i) => i.id === app.id);
  const tj = ctx.jobs.get(app.id);
  const [values, setValues] = useState<Values>(() => initial(app.options));
  const [volume, setVolume] = useState("");
  const [touched, setTouched] = useState(false);
  const [uninstall, setUninstall] = useState(false);

  useEffect(() => {
    setValues(initial(app.options));
    setTouched(false);
  }, [app.id, app.options]);

  useEffect(() => {
    if (!volume && vols[0]) setVolume(vols[0].id);
  }, [vols, volume]);

  const errors = useMemo(() => Object.fromEntries(app.options.map((o) => [o.key, validate(o, values[o.key])])), [app.options, values]);
  const valid = Object.values(errors).every((e) => !e) && !!volume;
  const busy = isActive(tj);

  const install = () => {
    setTouched(true);
    if (!valid) return;
    ctx.run(app.id, t.call("apps.install", { id: app.id, volume, options: values }), `No se pudo instalar ${app.name}`);
  };

  return (
    <div className="tg-app-pad tapp-detail">
      <header className="tapp-detail-head">
        <img src={app.icon} alt="" className="tapp-detail-icon" />
        <div className="tapp-detail-title">
          <h1 className="tg-h1">{app.name}</h1>
          <span className="tg-muted">
            {app.category} · versión {app.version}
          </span>
          <div className="tapp-detail-actions">
            <GetButton app={app} ctx={ctx} big />
            {inst && <StatePill state={inst.state} />}
          </div>
        </div>
      </header>

      <p className="tapp-detail-desc">{app.description}</p>

      {app.notes && (
        <div className="tapp-note">
          <Info size={15} />
          <span>{app.notes}</span>
        </div>
      )}

      <div className="tapp-facts">
        <div>
          <span>Descarga</span>
          <strong>{fmtBytes(app.sizeHint)}</strong>
        </div>
        <div>
          <span>Puerto web</span>
          <strong>{app.webPort ?? "—"}</strong>
        </div>
        <div>
          <span>Red</span>
          <strong>{app.requiresHostNetwork ? "La del NAS" : "Aislada"}</strong>
        </div>
        <div>
          <span>Datos</span>
          <strong>{inst ? inst.dataPath : "@apps/" + app.id}</strong>
        </div>
      </div>

      {tj && (busy || tj.job.state !== "succeeded" || !inst || inst.state === "installing") && <JobLog tj={tj} />}

      {inst && !busy ? (
        <Group title="Instalada">
          <Row label="Estado" hint={inst.error}>
            <StatePill state={inst.state} />
          </Row>
          <Row label="Volumen" hint={inst.dataPath}>
            <span className="tg-muted">{inst.volume}</span>
          </Row>
          <Row label="Desinstalar" hint="Podés conservar sus datos para reinstalarla después.">
            <Button variant="danger" onClick={() => setUninstall(true)}>
              Desinstalar…
            </Button>
          </Row>
        </Group>
      ) : !inst && !busy ? (
        <>
          <Group title="Instalación">
            {!ctx.dockerReady && (
              <Row label={<span className="tapp-warn"><AlertTriangle size={14} /> Docker no está instalado</span>} hint="Instalalo desde Ajustes de Docker; tarda menos de un minuto." />
            )}
            {vols.length === 0 ? (
              <Row label="Primero creá un volumen" hint="Las apps guardan sus datos en un volumen, así entran en las instantáneas.">
                <Button onClick={() => openApp("tango.storage", { action: "create" })}>Crear volumen…</Button>
              </Row>
            ) : (
              <Row label="Volumen" hint="Ahí se crea la carpeta de datos de la app.">
                <select className="tg-input tapp-select" value={volume} onChange={(e) => setVolume(e.target.value)} aria-label="Volumen">
                  {vols.map((v) => (
                    <option key={v.id} value={v.id}>
                      {v.name} · {fmtBytes(v.size - v.used)} libres
                    </option>
                  ))}
                </select>
              </Row>
            )}
            {app.options.map((o) => (
              <Row key={o.key} label={o.label} hint={touched && errors[o.key] ? <span className="tapp-err">{errors[o.key]}</span> : o.required ? "Obligatorio" : undefined}>
                {o.type === "bool" ? (
                  <Toggle checked={!!values[o.key]} onChange={(v) => setValues((s) => ({ ...s, [o.key]: v }))} label={o.label} />
                ) : (
                  <TextInput
                    type={o.type === "password" ? "password" : o.type === "port" ? "number" : "text"}
                    value={String(values[o.key] ?? "")}
                    onChange={(e) => setValues((s) => ({ ...s, [o.key]: o.type === "port" ? e.target.value.replace(/\D/g, "") : e.target.value }))}
                    aria-invalid={touched && !!errors[o.key]}
                    aria-label={o.label}
                    style={{ width: 220 }}
                  />
                )}
              </Row>
            ))}
          </Group>
          <div className="tapp-install-bar">
            <span className="tg-muted">Se descargan unos {fmtBytes(app.sizeHint)}. La app corre aislada en su propio contenedor.</span>
            <Button variant="primary" size="lg" disabled={!ctx.dockerReady || vols.length === 0} onClick={install}>
              Instalar {app.name}
            </Button>
          </div>
        </>
      ) : null}

      {inst && <UninstallSheet open={uninstall} app={inst} ctx={ctx} onClose={() => setUninstall(false)} onDone={onUninstalled} />}
    </div>
  );
}
