import { useState } from "react";
import { CheckCircle2, History, Package, RefreshCw, ShieldCheck, Wrench } from "lucide-react";
import { RpcError, fmtAgo, fmtDate, useIsReal, useLive, useTransport } from "@tango/client";
import type { RollbackPoint } from "@tango/client";
import { toast } from "@tango/shell";
import { Badge, Button, DemoBadge, EmptyState, Group, Row, Sheet, TextInput, Toggle } from "@tango/ui";
import { TangoLogo } from "@tango/theme-milonga";
import { Value } from "./common";
import { JobLog, useJobLog } from "./job";

export function Updates() {
  const t = useTransport();
  const real = useIsReal("system.updates.check");
  const { data: info, reload, loading, error } = useLive("system.updates.check", undefined);
  const [auto, setAuto] = useState(true);
  const [checking, setChecking] = useState(false);
  const [rollbackOpen, setRollbackOpen] = useState(false);
  const log = useJobLog((j) => {
    reload();
    if (j.state !== "succeeded") toast({ level: "crit", title: "La actualización falló", body: j.detail, app: "tango.settings" });
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
      const { job } = await t.call("system.updates.apply", { scope });
      log.start(job, "Creando instantánea del sistema (@rootfs)…");
    } catch (e) {
      toast({ level: "crit", title: "No se pudo actualizar", body: e instanceof RpcError ? e.message : String(e), app: "tango.settings" });
    }
  };

  if (error && !info) return <EmptyState icon={Wrench} title="No se pudo consultar las actualizaciones">{error.message}</EmptyState>;

  return (
    <>
      <div className="tset-update-hero">
        <div className="tset-about-logo tset-update-logo">
          <TangoLogo size={30} color="#fff" />
        </div>
        <div className="tset-update-text">
          <strong>
            {loading && !info
              ? "Buscando actualizaciones…"
              : pending === 0
                ? "TangoOS está al día"
                : [info?.tangoos.available && `TangoOS ${info.tangoos.available}`, sec.length && `${sec.length} parche${sec.length > 1 ? "s" : ""} de seguridad`, other.length && `${other.length} actualización${other.length > 1 ? "es" : ""} de Debian`]
                    .filter(Boolean)
                    .join(" · ")}
          </strong>
          <small>
            Versión instalada: {info?.tangoos.current ?? "—"} · {info ? `revisado ${fmtAgo(info.checkedAt)}` : ""} {!real && <DemoBadge />}
          </small>
        </div>
        {!log.running && pending > 0 && (
          <div className="tset-update-actions">
            {sec.length > 0 && (info?.tangoos.available || other.length > 0) && (
              <Button onClick={() => void apply("security")}>
                <ShieldCheck size={14} /> Solo seguridad
              </Button>
            )}
            <Button variant="primary" onClick={() => void apply("all")}>
              Actualizar ahora
            </Button>
          </div>
        )}
        {!log.running && pending === 0 && info && <CheckCircle2 size={22} color="var(--tg-ok)" />}
        <Button variant="plain" onClick={() => void check()} disabled={checking || log.running} aria-label="Buscar actualizaciones">
          <RefreshCw size={14} className={checking ? "tg-spin" : undefined} />
        </Button>
      </div>

      {log.id && <JobLog job={log.job} lines={log.lines} failed={log.failed} />}

      <p className="tset-foot" style={{ marginTop: 10 }}>
        Antes de instalar, TangoOS guarda una instantánea Btrfs del sistema (@rootfs). Si después algo no funciona, podés volver a ella desde acá o desde el menú de arranque. Tus volúmenes y datos no se tocan.
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
            hint="tangod, tango-agent y el escritorio"
          >
            <Value>
              {info.tangoos.current} → {info.tangoos.available}
            </Value>
          </Row>
        </Group>
      )}

      {sec.length > 0 && (
        <Group title={`Debian 13 · ${sec.length} parche${sec.length > 1 ? "s" : ""} de seguridad`}>
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
              <Badge tone="crit">Seguridad</Badge>
            </Row>
          ))}
        </Group>
      )}

      {other.length > 0 && (
        <Group title={`Debian 13 · ${other.length} actualización${other.length > 1 ? "es" : ""}`}>
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

      <Group title="Automático y recuperación">
        <Row label="Instalar parches de seguridad de Debian solos" hint="Solo trixie-security. Las versiones de TangoOS nunca se instalan solas.">
          <Toggle checked={auto} onChange={setAuto} label="Parches automáticos" />
        </Row>
        <Row
          icon={
            <span className="tset-row-icon" style={{ background: "#0e9c8f" }}>
              <History size={14} />
            </span>
          }
          label="Volver a una versión anterior"
          hint="Arranca el sistema desde una instantánea tomada antes de una actualización."
        >
          <Button onClick={() => setRollbackOpen(true)} disabled={log.running}>
            Ver puntos…
          </Button>
        </Row>
      </Group>

      {rollbackOpen && <RollbackSheet open onClose={() => setRollbackOpen(false)} />}
    </>
  );
}

function RollbackSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useTransport();
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
      const { job } = await t.call("system.rollback.apply", { id: sel.id, typed });
      log.start(job, "Marcando la instantánea como sistema por defecto…");
    } catch (e) {
      setErr(e instanceof RpcError ? e.message : String(e));
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
            {log.done ? "Listo" : "Cerrar"}
          </Button>
        ) : (
          <>
            <Button onClick={close}>Cancelar</Button>
            <Button variant="danger" disabled={!sel || typed !== "VOLVER"} onClick={() => void apply()}>
              Volver a este punto y reiniciar
            </Button>
          </>
        )
      }
    >
      <h2 className="tg-h2">Volver a una versión anterior</h2>
      <p className="tg-lead" style={{ marginTop: 6 }}>
        El sistema arranca desde la instantánea elegida. Los volúmenes, carpetas, usuarios y apps no se tocan; solo vuelven los programas del sistema.
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
                  <b>{p.label}</b>
                  <small>
                    {fmtDate(p.at)} · <span className="tset-mono">{p.id}</span>
                  </small>
                </span>
              </button>
            ))}
            {points?.length === 0 && <div className="tset-foot">Todavía no hay puntos de restauración: se crean antes de cada actualización.</div>}
          </div>
          {sel && (
            <label className="tset-typed">
              Para confirmar, escribí <code>VOLVER</code>
              <TextInput value={typed} onChange={(e) => setTyped(e.target.value.toUpperCase())} aria-label="Escribí VOLVER" />
            </label>
          )}
          {err && <div className="tset-err">{err}</div>}
        </>
      )}
    </Sheet>
  );
}
