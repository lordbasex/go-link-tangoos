import { useState } from "react";
import { CheckCircle2, History, Package, RefreshCw, ShieldCheck, Wrench } from "lucide-react";
import { RpcError, fmtAgo, fmtDate, t, tn, tr, useIsReal, useLive, useTransport } from "@tango/client";
import type { RollbackPoint } from "@tango/client";
import { toast } from "@tango/shell";
import { Badge, Button, DemoBadge, EmptyState, Group, Row, Sheet, TextInput, Toggle } from "@tango/ui";
import { TangoLogo } from "@tango/theme-milonga";
import { Value, tj } from "./common";
import { JobLog, useJobLog } from "./job";

export function Updates() {
  const tp = useTransport();
  const real = useIsReal("system.updates.check");
  const { data: info, reload, loading, error } = useLive("system.updates.check", undefined);
  const [auto, setAuto] = useState(true);
  const [checking, setChecking] = useState(false);
  const [rollbackOpen, setRollbackOpen] = useState(false);
  const log = useJobLog((j) => {
    reload();
    if (j.state !== "succeeded") toast({ level: "crit", title: t("La actualización falló"), body: tr(j.detail), app: "tango.settings" });
  });

  const sec = info?.debian.filter((d) => d.security) ?? [];
  const other = info?.debian.filter((d) => !d.security) ?? [];
  const pending = (info?.tangoos.available ? 1 : 0) + (info?.debian.length ?? 0);

  const check = async () => {
    setChecking(true);
    reload();
    setTimeout(() => setChecking(false), 600);
  };

  const apply = async (scope: "all" | "security") => {
    try {
      const { job } = await tp.call("system.updates.apply", { scope });
      log.start(job, t("Creando instantánea del sistema (@rootfs)…"));
    } catch (e) {
      toast({ level: "crit", title: t("No se pudo actualizar"), body: e instanceof RpcError ? e.message : tr(String(e)), app: "tango.settings" });
    }
  };

  if (error && !info) return <EmptyState icon={Wrench} title={t("No se pudo consultar las actualizaciones")}>{tr(error.message)}</EmptyState>;

  return (
    <>
      <div className="tset-update-hero">
        <div className="tset-about-logo tset-update-logo">
          <TangoLogo size={30} color="#fff" />
        </div>
        <div className="tset-update-text">
          <strong>
            {loading && !info
              ? t("Buscando actualizaciones…")
              : pending === 0
                ? t("TangoOS está al día")
                : [info?.tangoos.available && `TangoOS ${info.tangoos.available}`, sec.length && tn(sec.length, "{n} parche de seguridad", "{n} parches de seguridad"), other.length && tn(other.length, "{n} actualización de Debian", "{n} actualizaciones de Debian")]
                    .filter(Boolean)
                    .join(" · ")}
          </strong>
          <small>
            {t("Versión instalada: {v}", { v: info?.tangoos.current ?? "—" })} · {info ? t("revisado {ago}", { ago: fmtAgo(info.checkedAt) }) : ""} {!real && <DemoBadge />}
          </small>
        </div>
        {!log.running && pending > 0 && (
          <div className="tset-update-actions">
            {sec.length > 0 && (info?.tangoos.available || other.length > 0) && (
              <Button onClick={() => void apply("security")}>
                <ShieldCheck size={14} /> {t("Solo seguridad")}
              </Button>
            )}
            <Button variant="primary" onClick={() => void apply("all")}>
              {t("Actualizar ahora")}
            </Button>
          </div>
        )}
        {!log.running && pending === 0 && info && <CheckCircle2 size={22} color="var(--tg-ok)" />}
        <Button variant="plain" onClick={() => void check()} disabled={checking || log.running} aria-label={t("Buscar actualizaciones")}>
          <RefreshCw size={14} className={checking ? "tg-spin" : undefined} />
        </Button>
      </div>

      {log.id && <JobLog job={log.job} lines={log.lines} failed={log.failed} />}

      <p className="tset-foot" style={{ marginTop: 10 }}>
        {t("Antes de instalar, TangoOS guarda una instantánea Btrfs del sistema (@rootfs). Si después algo no funciona, podés volver a ella desde acá o desde el menú de arranque. Tus volúmenes y datos no se tocan.")}
      </p>

      {info?.tangoos.available && (
        <Group title="TangoOS">
          <Row
            icon={
              <span className="tset-row-icon" style={{ background: "#2F7CF6" }}>
                <Package size={15} />
              </span>
            }
            label={`tangoos ${info.tangoos.available}`}
            hint={t("tangod, tango-agent y el escritorio")}
          >
            <Value>
              {info.tangoos.current} → {info.tangoos.available}
            </Value>
          </Row>
        </Group>
      )}

      {sec.length > 0 && (
        <Group title={`Debian 13 · ${tn(sec.length, "{n} parche de seguridad", "{n} parches de seguridad")}`}>
          {sec.map((d) => (
            <Row
              key={d.name}
              icon={
                <span className="tset-row-icon" style={{ background: "#c70036" }}>
                  <ShieldCheck size={14} />
                </span>
              }
              label={<span className="tset-mono">{d.name}</span>}
              hint={`${d.from} → ${d.to}`}
            >
              <Badge tone="crit">{t("Seguridad")}</Badge>
            </Row>
          ))}
        </Group>
      )}

      {other.length > 0 && (
        <Group title={`Debian 13 · ${tn(other.length, "{n} actualización", "{n} actualizaciones")}`}>
          {other.map((d) => (
            <Row
              key={d.name}
              icon={
                <span className="tset-row-icon" style={{ background: "#8E8E96" }}>
                  <Package size={14} />
                </span>
              }
              label={<span className="tset-mono">{d.name}</span>}
              hint={`${d.from} → ${d.to}`}
            />
          ))}
        </Group>
      )}

      <Group title={t("Automático y recuperación")}>
        <Row label={t("Instalar parches de seguridad de Debian solos")} hint={t("Solo trixie-security. Las versiones de TangoOS nunca se instalan solas.")}>
          <Toggle checked={auto} onChange={setAuto} label={t("Parches automáticos")} />
        </Row>
        <Row
          icon={
            <span className="tset-row-icon" style={{ background: "#0e9c8f" }}>
              <History size={14} />
            </span>
          }
          label={t("Volver a una versión anterior")}
          hint={t("Arranca el sistema desde una instantánea tomada antes de una actualización.")}
        >
          <Button onClick={() => setRollbackOpen(true)} disabled={log.running}>
            {t("Ver puntos…")}
          </Button>
        </Row>
      </Group>

      {rollbackOpen && <RollbackSheet open onClose={() => setRollbackOpen(false)} />}
    </>
  );
}

function RollbackSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const tp = useTransport();
  const { data: points } = useLive("system.rollback.list", undefined);
  const [sel, setSel] = useState<RollbackPoint | null>(null);
  const [typed, setTyped] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const log = useJobLog();

  const close = () => {
    setSel(null);
    setTyped("");
    setErr(null);
    log.reset();
    onClose();
  };

  const apply = async () => {
    if (!sel) return;
    setErr(null);
    try {
      const { job } = await tp.call("system.rollback.apply", { id: sel.id, typed });
      log.start(job, t("Marcando la instantánea como sistema por defecto…"));
    } catch (e) {
      setErr(e instanceof RpcError ? e.message : tr(String(e)));
    }
  };

  return (
    <Sheet
      open={open}
      onClose={close}
      width={540}
      footer={
        log.id ? (
          <Button variant="primary" onClick={close} disabled={log.running}>
            {log.done ? t("Listo") : t("Cerrar")}
          </Button>
        ) : (
          <>
            <Button onClick={close}>{t("Cancelar")}</Button>
            <Button variant="danger" disabled={!sel || typed !== "VOLVER"} onClick={() => void apply()}>
              {t("Volver a este punto y reiniciar")}
            </Button>
          </>
        )
      }
    >
      <h2 className="tg-h2">{t("Volver a una versión anterior")}</h2>
      <p className="tg-lead" style={{ marginTop: 6 }}>
        {t("El sistema arranca desde la instantánea elegida. Los volúmenes, carpetas, usuarios y apps no se tocan; solo vuelven los programas del sistema.")}
      </p>
      {log.id ? (
        <JobLog job={log.job} lines={log.lines} failed={log.failed} />
      ) : (
        <>
          <div className="tset-rollback-list" role="radiogroup">
            {(points ?? []).map((p) => (
              <button key={p.id} type="button" role="radio" aria-checked={sel?.id === p.id} className="tset-rollback" onClick={() => setSel(p)}>
                <History size={16} />
                <span>
                  <b>{tr(p.label)}</b>
                  <small>
                    {fmtDate(p.at)} · <span className="tset-mono">{p.id}</span>
                  </small>
                </span>
              </button>
            ))}
            {points?.length === 0 && <div className="tset-foot">{t("Todavía no hay puntos de restauración: se crean antes de cada actualización.")}</div>}
          </div>
          {sel && (
            <label className="tset-typed">
              {tj("Para confirmar, escribí {word}", { word: <code>VOLVER</code> })}
              <TextInput value={typed} onChange={(e) => setTyped(e.target.value.toUpperCase())} aria-label={t("Escribí VOLVER")} />
            </label>
          )}
          {err && <div className="tset-err">{err}</div>}
        </>
      )}
    </Sheet>
  );
}
