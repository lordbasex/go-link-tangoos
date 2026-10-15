import { useEffect, useState } from "react";
import { BatteryCharging, BatteryWarning, Plug, Usb } from "lucide-react";
import { RpcError, onBattery, useIsReal, useLive, useTransport } from "@tango/client";
import type { UpsConfig, UpsMode } from "@tango/client";
import { toast } from "@tango/shell";
import { Badge, Button, DemoBadge, Group, Ring, Row, Segmented, Status, TextInput, Toggle } from "@tango/ui";
import { JobLog, useJobLog } from "./job";

// UPS por NUT (protocol/README.md §13): estado en vivo y política de apagado ordenado.

const fmtRuntime = (s: number | null) => (s == null ? "—" : s >= 3600 ? `${Math.floor(s / 3600)} h ${Math.round((s % 3600) / 60)} min` : `${Math.round(s / 60)} min`);

export function UpsCard() {
  const t = useTransport();
  const real = useIsReal("ups.status");
  const { data: st } = useLive("ups.status", undefined, ["ups.changed"]);
  const cfgQ = useLive("ups.config.get", undefined, ["services.changed"]);
  const [cfg, setCfg] = useState<UpsConfig | null>(null);
  const [password, setPassword] = useState("");
  const log = useJobLog((j) => {
    if (j.state === "succeeded") toast({ level: "ok", title: "UPS configurado", body: "", app: "tango.settings" });
    cfgQ.reload();
  });

  useEffect(() => {
    if (cfgQ.data && !cfg) setCfg(cfgQ.data);
  }, [cfgQ.data, cfg]);

  if (!cfg) return null;
  const set = (p: Partial<UpsConfig>) => setCfg({ ...cfg, ...p });
  const setPolicy = (p: Partial<UpsConfig["policy"]>) => setCfg({ ...cfg, policy: { ...cfg.policy, ...p } });
  const ob = !!st && onBattery(st.status);
  const pct = st?.batteryPct ?? null;

  const save = async () => {
    try {
      const { job } = await t.call("ups.config.set", { ...cfg, password: password || undefined });
      log.start(job, cfg.mode === "local" ? "Preparando NUT…" : "Guardando…");
      setPassword("");
    } catch (e) {
      toast({ level: "crit", title: "No se pudo guardar", body: e instanceof RpcError ? e.message : String(e), app: "tango.settings" });
    }
  };

  return (
    <Group
      title={
        <>
          UPS (alimentación ininterrumpida) {!real && <DemoBadge />}
        </>
      }
    >
      {st?.configured && (
        <div className="tset-ups">
          <Ring value={(pct ?? 0) / 100} size={58} stroke={6} color={ob ? "var(--tg-warn)" : "var(--tg-ok)"}>
            <span className="tg-num" style={{ fontSize: 13, fontWeight: 650 }}>{pct == null ? "—" : `${Math.round(pct)}%`}</span>
          </Ring>
          <div className="tset-ups-main">
            <strong>{st.model || st.name}</strong>
            {!st.online ? <Status tone="crit">{st.error || "Sin contacto con el UPS"}</Status> : ob ? <Status tone="warn">{st.statusText}</Status> : <Status tone="ok">{st.statusText}</Status>}
            <div className="tset-ups-stats">
              <span>Autonomía {fmtRuntime(st.runtimeSec)}</span>
              {st.loadPct != null && <span>Carga {Math.round(st.loadPct)} %</span>}
              {st.inputVoltage != null && <span>Red {Math.round(st.inputVoltage)} V</span>}
            </div>
          </div>
          {ob ? <BatteryWarning size={22} color="var(--tg-warn)" /> : <Plug size={20} color="var(--tg-text-3)" />}
        </div>
      )}
      <Row label="Conexión">
        <Segmented<UpsMode>
          label="Conexión del UPS"
          value={cfg.mode}
          onChange={(v) => set({ mode: v })}
          options={[
            { value: "none", label: "Sin UPS" },
            { value: "local", label: "USB en este equipo" },
            { value: "remote", label: "Otro equipo (red)" },
          ]}
        />
      </Row>
      {cfg.mode === "local" && (
        <>
          <Row label="UPS detectados por USB" hint="Se usa el driver usbhid-ups de NUT (APC, Eaton, CyberPower, Tripp Lite…)">
            {st?.detected.length ? (
              <span className="tset-badges">
                {st.detected.map((d) => (
                  <Badge key={d.id}>
                    <Usb size={11} /> {d.vendor} {d.product}
                  </Badge>
                ))}
              </span>
            ) : (
              <Status tone="warn">Ninguno conectado</Status>
            )}
          </Row>
          <Row label="Compartir con otros equipos" hint="Otros NAS o PC de la red pueden apagarse con este UPS (puerto 3493)">
            <Toggle checked={cfg.netServer} onChange={(v) => set({ netServer: v })} label="Compartir el UPS en la red" />
          </Row>
        </>
      )}
      {cfg.mode === "remote" && (
        <div className="tset-ups-form">
          <label>Equipo con el UPS</label>
          <TextInput value={cfg.host} onChange={(e) => set({ host: e.target.value.trim() })} placeholder="192.168.1.10" />
          <label>Puerto</label>
          <TextInput value={String(cfg.port)} inputMode="numeric" onChange={(e) => set({ port: Number(e.target.value) || 0 })} style={{ width: 90 }} />
          <label>Nombre del UPS</label>
          <TextInput value={cfg.upsName} onChange={(e) => set({ upsName: e.target.value.trim() })} placeholder="ups" />
          <label>Usuario (opcional)</label>
          <TextInput value={cfg.user ?? ""} onChange={(e) => set({ user: e.target.value.trim() || undefined })} />
          <label>Contraseña (opcional)</label>
          <TextInput type="password" value={password} placeholder="Sin cambios" onChange={(e) => setPassword(e.target.value)} />
        </div>
      )}
      {cfg.mode !== "none" && (
        <>
          <Row label="Ante un corte de luz">
            <Segmented
              label="Acción"
              value={cfg.policy.action}
              onChange={(v) => setPolicy({ action: v })}
              options={[
                { value: "shutdown", label: "Apagar en orden" },
                { value: "notify", label: "Solo avisar" },
              ]}
            />
          </Row>
          <Row label="Cuándo" hint="Lo que pase primero. Si el UPS avisa batería baja, se actúa enseguida.">
            <span className="tset-tm-max">
              después de <TextInput value={String(cfg.policy.afterMinutes)} inputMode="numeric" onChange={(e) => setPolicy({ afterMinutes: Number(e.target.value) || 0 })} style={{ width: 54 }} /> min o con menos de
              <TextInput value={String(cfg.policy.belowPercent)} inputMode="numeric" onChange={(e) => setPolicy({ belowPercent: Number(e.target.value) || 0 })} style={{ width: 54 }} /> %
            </span>
          </Row>
        </>
      )}
      <Row label="" hint={cfg.mode === "local" ? "Al guardar se instala NUT si hace falta." : undefined}>
        <Button variant="primary" disabled={log.running} onClick={save}>
          <BatteryCharging size={13} /> Guardar
        </Button>
      </Row>
      {log.id && <JobLog job={log.job} lines={log.lines} failed={log.failed} />}
    </Group>
  );
}
