import { useEffect, useRef, useState } from "react";
import { ExternalLink, MoreHorizontal, Package, Play, ScrollText, Square } from "lucide-react";
import { fmtBytes, fmtPct, useTransport } from "@tango/client";
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
      {stateLabel[state]}
    </span>
  );
}

/** Contenedor de hojas modales: debajo de la toolbar de la ventana (la ventana es el bloque contenedor). */
function SheetHost({ children }: { children: React.ReactNode }) {
  return <div className="tapp-sheet-host">{children}</div>;
}

export function UninstallSheet({ open, app, ctx, onClose, onDone }: { open: boolean; app: InstalledApp; ctx: AppsCtx; onClose: () => void; onDone?: () => void }) {
  const t = useTransport();
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
    ctx.run(app.id, t.call("apps.uninstall", { id: app.id, keepData: keep }), `No se pudo desinstalar ${app.name}`);
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
            <Button onClick={onClose}>Cancelar</Button>
            <Button variant="danger" disabled={!ok} onClick={go}>
              {keep ? "Desinstalar" : "Desinstalar y borrar datos"}
            </Button>
          </>
        }
      >
        <div className="tapp-sheet-head">
          <img src={app.icon} alt="" width={44} height={44} />
          <div>
            <h2 className="tg-h2">Desinstalar {app.name}</h2>
            <p className="tg-muted">Se detiene y se borra el contenedor.</p>
          </div>
        </div>
        <label className="tapp-check">
          <input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
          <span>
            Conservar los datos
            <small>{keep ? `Quedan en ${app.dataPath} para reinstalar después.` : `Se borra ${app.dataPath} y todo lo que contiene.`}</small>
          </span>
        </label>
        <label className="tapp-typed">
          Para confirmar, escribí <code>{app.name}</code>
          <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} onKeyDown={(e) => e.key === "Enter" && go()} aria-label="Nombre de la app" />
        </label>
      </Sheet>
    </SheetHost>
  );
}

function LogsSheet({ app, onClose }: { app: InstalledApp | null; onClose: () => void }) {
  const t = useTransport();
  const [lines, setLines] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const box = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (!app) return;
    let alive = true;
    const load = () =>
      t.call("apps.logs", { id: app.id, tail: 200 }).then(
        (r) => alive && (setLines(r.lines), setErr(null)),
        (e: Error) => alive && setErr(e.message),
      );
    load();
    const i = setInterval(load, 2000);
    return () => {
      alive = false;
      clearInterval(i);
    };
  }, [app, t]);
  useEffect(() => {
    const el = box.current;
    if (el && el.scrollHeight - el.scrollTop - el.clientHeight < 60) el.scrollTop = el.scrollHeight;
  }, [lines]);
  return (
    <SheetHost>
      <Sheet open={!!app} onClose={onClose} width={720} footer={<Button onClick={onClose}>Cerrar</Button>}>
        {app && (
          <>
            <div className="tapp-sheet-head">
              <img src={app.icon} alt="" width={32} height={32} />
              <div>
                <h2 className="tg-h2">Registro de {app.name}</h2>
                <p className="tg-muted">Últimas 200 líneas · se actualiza cada 2 s</p>
              </div>
            </div>
            <pre className="tapp-term tapp-logs" ref={box}>
              {err ? <span className="tapp-err">{err}</span> : lines.length ? lines.join("\n") : "Sin líneas todavía."}
            </pre>
          </>
        )}
      </Sheet>
    </SheetHost>
  );
}

export function Installed({ ctx, onBrowse }: { ctx: AppsCtx; onBrowse: () => void }) {
  const t = useTransport();
  const [logs, setLogs] = useState<InstalledApp | null>(null);
  const [removing, setRemoving] = useState<InstalledApp | null>(null);

  if (!ctx.installed.length)
    return (
      <EmptyState icon={Package} title="Todavía no instalaste apps" action={<Button onClick={onBrowse}>Ver el catálogo</Button>}>
        Jellyfin, Immich, Home Assistant y más: cada una corre aislada y guarda sus datos en tu volumen.
      </EmptyState>
    );

  const act = (a: InstalledApp, verb: "start" | "stop" | "restart" | "update", fail: string) => ctx.run(a.id, t.call(`apps.${verb}`, { id: a.id }), fail);

  const more = (a: InstalledApp) => (e: React.MouseEvent) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    showContextMenu(r.left, r.bottom + 4, [
      {
        label: "Abrir en pestaña nueva",
        disabled: !a.url || a.url.startsWith("data:"),
        action: () => {
          // La pestaña se abre ya (gesto del usuario) y después recibe la URL con ticket.
          const w = window.open("about:blank", "_blank");
          if (w) w.opener = null;
          void appUrl(t, a).then((url) => (url && w ? (w.location.href = url) : w?.close()));
        },
      },
      { separator: true },
      { label: "Reiniciar", disabled: a.state !== "running", action: () => act(a, "restart", `No se pudo reiniciar ${a.name}`) },
      { label: "Actualizar", disabled: a.state === "installing" || a.state === "updating", action: () => act(a, "update", `No se pudo actualizar ${a.name}`) },
      { label: "Ver registro…", action: () => setLogs(a) },
      { label: "Detalles", action: () => ctx.openDetail(a.id) },
      { separator: true },
      { label: "Desinstalar…", action: () => setRemoving(a) },
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
                {a.error && <span className="tapp-err">{a.error}</span>}
              </div>
              <StatePill state={a.state} />
              <div className="tapp-row-usage tg-num">
                <span>CPU {a.cpu === null ? "—" : fmtPct(a.cpu, 1)}</span>
                <span>RAM {a.mem === null ? "—" : fmtBytes(a.mem)}</span>
              </div>
              <div className="tapp-row-actions">
                {a.state === "running" ? (
                  <IconButton icon={Square} label={`Detener ${a.name}`} disabled={busy} onClick={() => act(a, "stop", `No se pudo detener ${a.name}`)} />
                ) : (
                  <IconButton icon={Play} label={`Iniciar ${a.name}`} disabled={busy || a.state === "installing"} onClick={() => act(a, "start", `No se pudo iniciar ${a.name}`)} />
                )}
                <IconButton icon={ScrollText} label="Ver registro" onClick={() => setLogs(a)} />
                <IconButton icon={MoreHorizontal} label="Más acciones" onClick={more(a)} />
                <Button variant="primary" disabled={a.state !== "running" || !a.url} onClick={() => ctx.launch(a)}>
                  <ExternalLink size={13} /> Abrir
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
