import { useEffect, useState } from "react";
import { BatteryCharging, BatteryWarning, Plug, Usb } from "lucide-react";
import { RpcError, onBattery, t, tr, useIsReal, useLive, useTransport } from "@tango/client";
import type { UpsConfig, UpsMode } from "@tango/client";
import { toast } from "@tango/shell";
import { Badge, Button, DemoBadge, Group, Ring, Row, Segmented, Status, TextInput, Toggle } from "@tango/ui";
import { tj } from "./common";
import { JobLog, useJobLog } from "./job";

// UPS por NUT (protocol/README.md §13): estado en vivo y política de apagado ordenado.

const fmtRuntime = (s: number | null) => (s == null ? "—" : s >= 3600 ? `${Math.floor(s / 3600)} h ${Math.round((s % 3600) / 60)} min` : `${Math.round(s / 60)} min`);

export function UpsCard() {
  const tp = useTransport();
  const real = useIsReal("ups.status");
  const { data: st } = useLive("ups.status", undefined, ["ups.changed"]);
  const cfgQ = useLive("ups.config.get", undefined, ["services.changed"]);
  const [cfg, setCfg] = useState<UpsConfig | null>(null);
  const [password, setPassword] = useState("");
  const log = useJobLog((j) => {
    if (j.state === "succeeded") toast({ level: "ok", title: t("UPS configurado"), body: "", app: "tango.settings" });
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
      const { job } = await tp.call("ups.config.set", { ...cfg, password: password || undefined });
      log.start(job, cfg.mode === "local" ? t("Preparando NUT…") : t("Guardando…"));
      setPassword("");
    } catch (e) {
      toast({ level: "crit", title: t("No se pudo guardar"), body: e instanceof RpcError ? e.message : tr(String(e)), app: "tango.settings" });
    }
  };

  return (
    <Group
      title={
        <>
          {t("UPS (alimentación ininterrumpida)")} {!real && <DemoBadge />}
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
            {!st.online ? <Status tone="crit">{st.error ? tr(st.error) : t("Sin contacto con el UPS")}</Status> : ob ? <Status tone="warn">{tr(st.statusText)}</Status> : <Status tone="ok">{tr(st.statusText)}</Status>}
            <div className="tset-ups-stats">
              <span>{t("Autonomía {time}", { time: fmtRuntime(st.runtimeSec) })}</span>
              {st.loadPct != null && <span>{t("Carga {pct} %", { pct: Math.round(st.loadPct) })}</span>}
              {st.inputVoltage != null && <span>{t("Red {v} V", { v: Math.round(st.inputVoltage) })}</span>}
            </div>
          </div>
          {ob ? <BatteryWarning size={22} color="var(--tg-warn)" /> : <Plug size={20} color="var(--tg-text-3)" />}
        </div>
      )}
      <Row label={t("Conexión")}>
        <Segmented<UpsMode>
          label={t("Conexión del UPS")}
          value={cfg.mode}
          onChange={(v) => set({ mode: v })}
          options={[
            { value: "none", label: t("Sin UPS") },
            { value: "local", label: t("USB en este equipo") },
            { value: "remote", label: t("Otro equipo (red)") },
          ]}
        />
      </Row>
      {cfg.mode === "local" && (
        <>
          <Row label={t("UPS detectados por USB")} hint={t("Se usa el driver usbhid-ups de NUT (APC, Eaton, CyberPower, Tripp Lite…)")}>
            {st?.detected.length ? (
              <span className="tset-badges">
                {st.detected.map((d) => (
                  <Badge key={d.id}>
                    <Usb size={11} /> {d.vendor} {d.product}
                  </Badge>
                ))}
              </span>
            ) : (
              <Status tone="warn">{t("Ninguno conectado")}</Status>
            )}
          </Row>
          <Row label={t("Compartir con otros equipos")} hint={t("Otros NAS o PC de la red pueden apagarse con este UPS (puerto 3493)")}>
            <Toggle checked={cfg.netServer} onChange={(v) => set({ netServer: v })} label={t("Compartir el UPS en la red")} />
          </Row>
        </>
      )}
      {cfg.mode === "remote" && (
        <div className="tset-ups-form">
          <label>{t("Equipo con el UPS")}</label>
          <TextInput value={cfg.host} onChange={(e) => set({ host: e.target.value.trim() })} placeholder="192.168.1.10" />
          <label>{t("Puerto")}</label>
          <TextInput value={String(cfg.port)} inputMode="numeric" onChange={(e) => set({ port: Number(e.target.value) || 0 })} style={{ width: 90 }} />
          <label>{t("Nombre del UPS")}</label>
          <TextInput value={cfg.upsName} onChange={(e) => set({ upsName: e.target.value.trim() })} placeholder="ups" />
          <label>{t("Usuario (opcional)")}</label>
          <TextInput value={cfg.user ?? ""} onChange={(e) => set({ user: e.target.value.trim() || undefined })} />
          <label>{t("Contraseña (opcional)")}</label>
          <TextInput type="password" value={password} placeholder={t("Sin cambios")} onChange={(e) => setPassword(e.target.value)} />
        </div>
      )}
      {cfg.mode !== "none" && (
        <>
          <Row label={t("Ante un corte de luz")}>
            <Segmented
              label={t("Acción")}
              value={cfg.policy.action}
              onChange={(v) => setPolicy({ action: v })}
              options={[
                { value: "shutdown", label: t("Apagar en orden") },
                { value: "notify", label: t("Solo avisar") },
              ]}
            />
          </Row>
          <Row label={t("Cuándo")} hint={t("Lo que pase primero. Si el UPS avisa batería baja, se actúa enseguida.")}>
            <span className="tset-tm-max">
              {tj("después de {min} min o con menos de {pct} %", {
                min: <TextInput value={String(cfg.policy.afterMinutes)} inputMode="numeric" onChange={(e) => setPolicy({ afterMinutes: Number(e.target.value) || 0 })} style={{ width: 54 }} aria-label={t("Minutos")} />,
                pct: <TextInput value={String(cfg.policy.belowPercent)} inputMode="numeric" onChange={(e) => setPolicy({ belowPercent: Number(e.target.value) || 0 })} style={{ width: 54 }} aria-label={t("Porcentaje de batería")} />,
              })}
            </span>
          </Row>
        </>
      )}
      <Row label="" hint={cfg.mode === "local" ? t("Al guardar se instala NUT si hace falta.") : undefined}>
        <Button variant="primary" disabled={log.running} onClick={save}>
          <BatteryCharging size={13} /> {t("Guardar")}
        </Button>
      </Row>
      {log.id && <JobLog job={log.job} lines={log.lines} failed={log.failed} />}
    </Group>
  );
}
