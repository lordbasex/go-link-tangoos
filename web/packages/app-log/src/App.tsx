// Registro (protocol §12): auditoría de cada acción y logs del sistema, en vivo.
import "./log.css";
import { useEffect, useMemo, useRef, useState } from "react";
import { Ban, CheckCircle2, CircleAlert, Pause, Play, ScrollText, ShieldCheck } from "lucide-react";
import { useIsReal, useLive, useTopic } from "@tango/client";
import type { AuditEvent, LogLine, LogUnit } from "@tango/client";
import { WindowToolbar, useWindowMenus } from "@tango/shell";
import type { AppProps } from "@tango/shell";
import { Badge, DemoBadge, EmptyState, IconButton, SearchField, Segmented } from "@tango/ui";

type Tab = "audit" | "system";

const METHOD_LABEL: Record<string, string> = {
  "auth.login": "Inicio de sesión",
  "auth.logout": "Cierre de sesión",
  "setup.complete": "Configuración inicial",
  "storage.volumes.create": "Crear volumen",
  "storage.volumes.delete": "Eliminar volumen",
  "storage.scrub.start": "Verificar volumen",
  "storage.disks.replace": "Reemplazar disco",
  "storage.volumes.expand": "Ampliar volumen",
  "shares.create": "Crear carpeta compartida",
  "shares.delete": "Eliminar carpeta compartida",
  "shares.permissions.set": "Cambiar permisos",
  "snapshots.create": "Crear instantánea",
  "snapshots.delete": "Eliminar instantánea",
  "users.create": "Crear usuario",
  "users.setPassword": "Cambiar contraseña",
  "apps.install": "Instalar app",
  "apps.uninstall": "Desinstalar app",
  "system.updates.apply": "Actualizar el sistema",
  "system.rollback.apply": "Volver a una versión anterior",
  "system.reboot": "Reiniciar",
  "system.shutdown": "Apagar",
  "cert.localca": "Crear CA local",
  "services.set": "Cambiar servicio",
  "nfs.exports.set": "Cambiar acceso NFS",
  "iscsi.targets.create": "Crear destino iSCSI",
  "iscsi.targets.delete": "Eliminar destino iSCSI",
  "notify.channels.set": "Cambiar notificaciones",
  "term.open": "Abrir terminal",
};

const fmtTime = (iso: string) => {
  const d = new Date(iso);
  const today = new Date().toDateString() === d.toDateString();
  return today ? d.toLocaleTimeString("es-AR", { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" }) : d.toLocaleString("es-AR", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
};

export default function LogApp({ win }: AppProps) {
  const [tab, setTab] = useState<Tab>(win.args.tab === "system" ? "system" : "audit");
  const [q, setQ] = useState("");
  const real = useIsReal(tab === "audit" ? "audit.list" : "logs.query");

  useWindowMenus(
    win,
    [
      {
        label: "Ver",
        items: [
          { label: "Auditoría", checked: tab === "audit", action: () => setTab("audit") },
          { label: "Sistema", checked: tab === "system", action: () => setTab("system") },
        ],
      },
    ],
    [tab],
  );

  return (
    <div className="tlog">
      <WindowToolbar win={win}>
        <span className="tg-tb-title">Registro</span>
        <span className="tg-tb-spacer" />
        <Segmented
          label="Vista"
          value={tab}
          onChange={setTab}
          options={[
            { value: "audit", label: "Auditoría" },
            { value: "system", label: "Sistema" },
          ]}
        />
        <span className="tg-tb-spacer" />
        {!real && <DemoBadge />}
        <div style={{ width: 200 }}>
          <SearchField value={q} onChange={setQ} placeholder={tab === "audit" ? "Usuario, acción, IP…" : "Buscar en los logs"} />
        </div>
      </WindowToolbar>
      {tab === "audit" ? <Audit q={q} /> : <System q={q} />}
    </div>
  );
}

function Audit({ q }: { q: string }) {
  const { data } = useLive("audit.list", { q: q || undefined, limit: 200 });
  const [live, setLive] = useState<AuditEvent[]>([]);
  const [result, setResult] = useState<"all" | "fail">("all");
  useEffect(() => setLive([]), [data]);
  useTopic("audit.event", (e) => setLive((l) => [e.data, ...l].slice(0, 100)));

  const rows = useMemo(() => {
    const n = q.toLowerCase();
    const all = [...live.filter((e) => !n || `${e.actor} ${e.method} ${e.target} ${e.ip}`.toLowerCase().includes(n)), ...(data ?? [])];
    return result === "fail" ? all.filter((e) => e.result !== "ok") : all;
  }, [data, live, q, result]);

  return (
    <div className="tlog-main">
      <div className="tlog-filters">
        <Segmented
          label="Resultado"
          value={result}
          onChange={setResult}
          options={[
            { value: "all", label: "Todo" },
            { value: "fail", label: "Fallidos y denegados" },
          ]}
        />
        <span className="tlog-count">{rows.length} eventos</span>
      </div>
      <div className="tg-app-content tlog-table-wrap">
        {rows.length === 0 ? (
          <EmptyState icon={ShieldCheck} title={q ? "Nada coincide" : "Sin eventos todavía"}>
            {q ? `Ningún evento contiene «${q}».` : "Cada acción que cambia algo en el NAS, y cada inicio de sesión, aparece acá."}
          </EmptyState>
        ) : (
          <table className="tg-table tlog-table">
            <thead>
              <tr>
                <th>Hora</th>
                <th>Usuario</th>
                <th>Acción</th>
                <th>Objetivo</th>
                <th>Origen</th>
                <th>Resultado</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((e, i) => (
                <tr key={`${e.at}-${i}`} data-new={i < live.length || undefined}>
                  <td className="tg-num">{fmtTime(e.at)}</td>
                  <td>{e.actor}</td>
                  <td title={e.method}>{METHOD_LABEL[e.method] ?? e.method}</td>
                  <td>{e.target}</td>
                  <td className="tlog-dim">
                    {e.ip} · {e.client}
                  </td>
                  <td>
                    {e.result === "ok" ? (
                      <span className="tlog-res" data-r="ok">
                        <CheckCircle2 size={13} /> Correcto
                      </span>
                    ) : e.result === "denied" ? (
                      <span className="tlog-res" data-r="denied">
                        <Ban size={13} /> Denegado
                      </span>
                    ) : (
                      <span className="tlog-res" data-r="error">
                        <CircleAlert size={13} /> Falló
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

const UNITS: { value: "all" | LogUnit; label: string }[] = [
  { value: "all", label: "Todo" },
  { value: "tangod", label: "tangod" },
  { value: "tango-agent", label: "Agente" },
  { value: "smbd", label: "Samba" },
  { value: "docker", label: "Docker" },
  { value: "kernel", label: "Kernel" },
];

const PRIO: Record<number, { label: string; tone: "crit" | "warn" | "info" | undefined }> = {
  0: { label: "EMERG", tone: "crit" },
  1: { label: "ALERT", tone: "crit" },
  2: { label: "CRIT", tone: "crit" },
  3: { label: "ERR", tone: "crit" },
  4: { label: "WARN", tone: "warn" },
  5: { label: "NOTICE", tone: "info" },
  6: { label: "INFO", tone: undefined },
  7: { label: "DEBUG", tone: undefined },
};

function System({ q }: { q: string }) {
  const [unit, setUnit] = useState<"all" | LogUnit>("all");
  const [follow, setFollow] = useState(true);
  const params = { unit: unit === "all" ? undefined : unit, q: q || undefined, limit: 500 };
  const { data, reload } = useLive("logs.query", params);
  const box = useRef<HTMLDivElement>(null);
  // Modo seguir: refresca cada 2 s (los logs del journal no son eventos del bus).
  useEffect(() => {
    if (!follow) return;
    const i = setInterval(reload, 2000);
    return () => clearInterval(i);
  }, [follow, reload]);

  const lines: LogLine[] = useMemo(() => [...(data ?? [])].sort((a, b) => a.at.localeCompare(b.at)), [data]);
  useEffect(() => {
    if (follow) box.current?.scrollTo({ top: box.current.scrollHeight });
  }, [lines, follow]);

  return (
    <div className="tlog-main">
      <div className="tlog-filters">
        <Segmented label="Servicio" value={unit} onChange={setUnit} options={UNITS} />
        <span className="tlog-count">{lines.length} líneas</span>
        <IconButton icon={follow ? Pause : Play} label={follow ? "Pausar" : "Seguir en vivo"} pressed={follow} onClick={() => setFollow((f) => !f)} />
      </div>
      <div className="tlog-console" ref={box} onWheel={(e) => e.deltaY < 0 && setFollow(false)}>
        {lines.length === 0 && (
          <EmptyState icon={ScrollText} title="Sin líneas">
            {q ? `Nada contiene «${q}».` : "No hay registros para este servicio."}
          </EmptyState>
        )}
        {lines.map((l, i) => {
          const p = PRIO[l.prio] ?? PRIO[6];
          return (
            <div key={`${l.at}-${i}`} className="tlog-line" data-tone={p.tone}>
              <time>{fmtTime(l.at)}</time>
              <span className="tlog-unit">{l.unit}</span>
              {p.tone ? <Badge tone={p.tone}>{p.label}</Badge> : <span className="tlog-prio">{p.label}</span>}
              <span className="tlog-msg">{l.msg}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
