import { useEffect, useRef, useState } from "react";
import { ExternalLink, MoreHorizontal, Package, Play, ScrollText, Square } from "lucide-react";
import { fmtBytes, fmtPct, t, tr, useTransport } from "@tango/client";
import type { AppState, InstalledApp } from "@tango/client";
import { Button, EmptyState, IconButton, Sheet, TextInput } from "@tango/ui";
import { showContextMenu } from "@tango/shell";
import { appUrl } from "./ticket";
import type { AppsCtx } from "./App";
import { isActive } from "./jobs";

const stateLabel: Record<AppState, string> = {
  installing: "Instalando",
  running: "En ejecución",
  stopped: "Detenida",
  updating: "Actualizando",
  error: "Con error",
};

export function StatePill({ state }: { state: AppState }) {
  return (
    <span className="tapp-pill" data-state={state}>
      <i aria-hidden="true" />
      {t(stateLabel[state])}
    </span>
  );
}

/** Contenedor de hojas modales: debajo de la toolbar de la ventana (la ventana es el bloque contenedor). */
function SheetHost({ children }: { children: React.ReactNode }) {
  return <div className="tapp-sheet-host">{children}</div>;
}

export function UninstallSheet({ open, app, ctx, onClose, onDone }: { open: boolean; app: InstalledApp; ctx: AppsCtx; onClose: () => void; onDone?: () => void }) {
  const tp = useTransport();
  const [keep, setKeep] = useState(true);
  const [typed, setTyped] = useState("");
  useEffect(() => {
    if (open) {
      setKeep(true);
      setTyped("");
    }
  }, [open]);
  const ok = typed.trim() === app.name;
  const go = () => {
    if (!ok) return;
    ctx.run(app.id, tp.call("apps.uninstall", { id: app.id, keepData: keep }), t("No se pudo desinstalar {name}", { name: app.name }));
    onClose();
    onDone?.();
  };
  return (
    <SheetHost>
      <Sheet
        open={open}
        onClose={onClose}
        width={480}
        footer={
          <>
            <Button onClick={onClose}>{t("Cancelar")}</Button>
            <Button variant="danger" disabled={!ok} onClick={go}>
              {keep ? t("Desinstalar") : t("Desinstalar y borrar datos")}
            </Button>
          </>
        }
      >
        <div className="tapp-sheet-head">
          <img src={app.icon} alt="" width={44} height={44} />
          <div>
            <h2 className="tg-h2">{t("Desinstalar {name}", { name: app.name })}</h2>
            <p className="tg-muted">{t("Se detiene y se borra el contenedor.")}</p>
          </div>
        </div>
        <label className="tapp-check">
          <input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
          <span>
            {t("Conservar los datos")}
            <small>{keep ? t("Quedan en {path} para reinstalar después.", { path: app.dataPath }) : t("Se borra {path} y todo lo que contiene.", { path: app.dataPath })}</small>
          </span>
        </label>
        <label className="tapp-typed">
          {t("Para confirmar, escribí")} <code>{app.name}</code>
          <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} onKeyDown={(e) => e.key === "Enter" && go()} aria-label={t("Nombre de la app")} />
        </label>
      </Sheet>
    </SheetHost>
  );
}

function LogsSheet({ app, onClose }: { app: InstalledApp | null; onClose: () => void }) {
  const tp = useTransport();
  const [lines, setLines] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const box = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (!app) return;
    let alive = true;
    const load = () =>
      tp.call("apps.logs", { id: app.id, tail: 200 }).then(
        (r) => alive && (setLines(r.lines), setErr(null)),
        (e: Error) => alive && setErr(e.message),
      );
    load();
    const i = setInterval(load, 2000);
    return () => {
      alive = false;
      clearInterval(i);
    };
  }, [app, tp]);
  useEffect(() => {
    const el = box.current;
    if (el && el.scrollHeight - el.scrollTop - el.clientHeight < 60) el.scrollTop = el.scrollHeight;
  }, [lines]);
  return (
    <SheetHost>
      <Sheet open={!!app} onClose={onClose} width={720} footer={<Button onClick={onClose}>{t("Cerrar")}</Button>}>
        {app && (
          <>
            <div className="tapp-sheet-head">
              <img src={app.icon} alt="" width={32} height={32} />
              <div>
                <h2 className="tg-h2">{t("Registro de {name}", { name: app.name })}</h2>
                <p className="tg-muted">{t("Últimas 200 líneas · se actualiza cada 2 s")}</p>
              </div>
            </div>
            <pre className="tapp-term tapp-logs" ref={box}>
              {err ? <span className="tapp-err">{err}</span> : lines.length ? lines.join("\n") : t("Sin líneas todavía.")}
            </pre>
          </>
        )}
      </Sheet>
    </SheetHost>
  );
}

export function Installed({ ctx, onBrowse }: { ctx: AppsCtx; onBrowse: () => void }) {
  const tp = useTransport();
  const [logs, setLogs] = useState<InstalledApp | null>(null);
  const [removing, setRemoving] = useState<InstalledApp | null>(null);

  if (!ctx.installed.length)
    return (
      <EmptyState icon={Package} title={t("Todavía no instalaste apps")} action={<Button onClick={onBrowse}>{t("Ver el catálogo")}</Button>}>
        {t("Jellyfin, Immich, Home Assistant y más: cada una corre aislada y guarda sus datos en tu volumen.")}
      </EmptyState>
    );

  const act = (a: InstalledApp, verb: "start" | "stop" | "restart" | "update", fail: string) => ctx.run(a.id, tp.call(`apps.${verb}`, { id: a.id }), fail);

  const more = (a: InstalledApp) => (e: React.MouseEvent) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    showContextMenu(r.left, r.bottom + 4, [
      {
        label: t("Abrir en pestaña nueva"),
        disabled: !a.url || a.url.startsWith("data:"),
        action: () => {
          // La pestaña se abre ya (gesto del usuario) y después recibe la URL con ticket.
          const w = window.open("about:blank", "_blank");
          if (w) w.opener = null;
          void appUrl(tp, a).then((url) => (url && w ? (w.location.href = url) : w?.close()));
        },
      },
      { separator: true },
      { label: t("Reiniciar"), disabled: a.state !== "running", action: () => act(a, "restart", t("No se pudo reiniciar {name}", { name: a.name })) },
      { label: t("Actualizar"), disabled: a.state === "installing" || a.state === "updating", action: () => act(a, "update", t("No se pudo actualizar {name}", { name: a.name })) },
      { label: t("Ver registro…"), action: () => setLogs(a) },
      { label: t("Detalles"), action: () => ctx.openDetail(a.id) },
      { separator: true },
      { label: t("Desinstalar…"), action: () => setRemoving(a) },
    ]);
  };

  return (
    <div className="tg-app-pad">
      <div className="tapp-list" role="list">
        {ctx.installed.map((a) => {
          const busy = isActive(ctx.jobs.get(a.id)) || a.state === "installing" || a.state === "updating";
          return (
            <div key={a.id} className="tapp-row" role="listitem">
              <img src={a.icon} alt="" className="tapp-row-icon" />
              <div className="tapp-row-main">
                <strong>{a.name}</strong>
                <span className="tg-muted">
                  {a.version} · {a.dataPath}
                </span>
                {a.error && <span className="tapp-err">{tr(a.error)}</span>}
              </div>
              <StatePill state={a.state} />
              <div className="tapp-row-usage tg-num">
                <span>CPU {a.cpu === null ? "—" : fmtPct(a.cpu, 1)}</span>
                <span>RAM {a.mem === null ? "—" : fmtBytes(a.mem)}</span>
              </div>
              <div className="tapp-row-actions">
                {a.state === "running" ? (
                  <IconButton icon={Square} label={t("Detener {name}", { name: a.name })} disabled={busy} onClick={() => act(a, "stop", t("No se pudo detener {name}", { name: a.name }))} />
                ) : (
                  <IconButton icon={Play} label={t("Iniciar {name}", { name: a.name })} disabled={busy || a.state === "installing"} onClick={() => act(a, "start", t("No se pudo iniciar {name}", { name: a.name }))} />
                )}
                <IconButton icon={ScrollText} label={t("Ver registro")} onClick={() => setLogs(a)} />
                <IconButton icon={MoreHorizontal} label={t("Más acciones")} onClick={more(a)} />
                <Button variant="primary" disabled={a.state !== "running" || !a.url} onClick={() => ctx.launch(a)}>
                  <ExternalLink size={13} /> {t("Abrir")}
                </Button>
              </div>
            </div>
          );
        })}
      </div>
      <LogsSheet app={logs} onClose={() => setLogs(null)} />
      {removing && <UninstallSheet open app={removing} ctx={ctx} onClose={() => setRemoving(null)} />}
    </div>
  );
}
