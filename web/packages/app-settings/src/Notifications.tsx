import { useEffect, useState } from "react";
import { Mail, MessageCircle, Send } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { RpcError, t, tr, useIsReal, useLive, useTransport } from "@tango/client";
import type { NotifyChannels } from "@tango/client";
import { toast } from "@tango/shell";
import { Button, DemoBadge, Group, Row, Segmented, Status, TextInput, Toggle } from "@tango/ui";

function Icon({ icon: I, color }: { icon: LucideIcon; color: string }) {
  return (
    <span className="tset-row-icon" style={{ background: color }}>
      <I size={15} />
    </span>
  );
}

type Level = NotifyChannels["levels"][number];
const LEVELS: { id: Level; label: string; hint: string }[] = [
  { id: "crit", label: "Críticos", hint: "Disco caído, volumen sin redundancia, SMART en falla" },
  { id: "warn", label: "Advertencias", hint: "Temperatura alta, espacio por debajo del 10 %, sectores reasignados" },
  { id: "info", label: "Informativos", hint: "Actualizaciones disponibles" },
  { id: "ok", label: "Tareas terminadas", hint: "Volúmenes creados, verificaciones, instalaciones" },
];

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="tset-field">
      <span>{label}</span>
      {children}
    </label>
  );
}

export function Notifications() {
  const tp = useTransport();
  const real = useIsReal("notify.channels.get");
  const { data } = useLive("notify.channels.get", undefined);
  const [c, setC] = useState<NotifyChannels | null>(null);
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (data && !dirty) setC(structuredClone(data));
  }, [data, dirty]);

  if (!c) return null;

  const upd = <K extends "email" | "ntfy" | "telegram">(k: K, patch: Partial<NotifyChannels[K]>) => {
    setDirty(true);
    setC((x) => (x ? { ...x, [k]: { ...x[k], ...patch } } : x));
  };

  const save = async () => {
    setSaving(true);
    setErr(null);
    try {
      await tp.call("notify.channels.set", c);
      setDirty(false);
      toast({ level: "ok", title: t("Notificaciones guardadas"), app: "tango.settings" });
    } catch (e) {
      setErr(e instanceof RpcError ? e.message : tr(String(e)));
    } finally {
      setSaving(false);
    }
  };

  const test = async (channel: "email" | "ntfy" | "telegram") => {
    if (dirty) {
      toast({ level: "info", title: t("Guardá los cambios primero"), body: t("La prueba usa la configuración guardada."), app: "tango.settings" });
      return;
    }
    try {
      await tp.call("notify.test", { channel });
      toast({ level: "ok", title: t("Prueba enviada"), body: t("Si no llega en un minuto, revisá los datos del canal."), app: "tango.settings" });
    } catch (e) {
      toast({ level: "warn", title: t("La prueba falló"), body: e instanceof RpcError ? e.message : tr(String(e)), app: "tango.settings" });
    }
  };

  const toggleLevel = (l: Level, on: boolean) => {
    setDirty(true);
    setC((x) => (x ? { ...x, levels: on ? [...new Set([...x.levels, l])] : x.levels.filter((y) => y !== l) } : x));
  };

  return (
    <>
      <Group title={t("En el escritorio")}>
        <Row label={t("Avisos en pantalla")} hint={t("Discos, volúmenes, tareas terminadas y actualizaciones")}>
          <Status tone="ok">{t("Activos")}</Status>
        </Row>
      </Group>

      <Group
        title={
          <>
            {t("Correo")} {!real && <DemoBadge />}
          </>
        }
      >
        <Row icon={<Icon icon={Mail} color="#2F7CF6" />} label={t("Avisar por correo")} hint={t("Por SMTP, con tu cuenta o un servicio transaccional")}>
          {c.email.enabled && (
            <Button variant="plain" onClick={() => void test("email")}>
              {t("Enviar prueba")}
            </Button>
          )}
          <Toggle checked={c.email.enabled} onChange={(v) => upd("email", { enabled: v })} label={t("Correo")} />
        </Row>
        {c.email.enabled && (
          <div className="tset-form">
            <Field label={t("Servidor SMTP")}>
              <TextInput value={c.email.host} onChange={(e) => upd("email", { host: e.target.value })} placeholder={t("smtp.ejemplo.com")} />
            </Field>
            <Field label={t("Puerto")}>
              <TextInput value={String(c.email.port)} inputMode="numeric" onChange={(e) => upd("email", { port: Number(e.target.value.replace(/\D/g, "")) || 0 })} />
            </Field>
            <Field label={t("Cifrado")}>
              <Segmented
                label={t("Cifrado")}
                value={c.email.tls}
                onChange={(v) => upd("email", { tls: v })}
                options={[
                  { value: "starttls", label: "STARTTLS" },
                  { value: "tls", label: "TLS" },
                  { value: "none", label: t("Ninguno") },
                ]}
              />
            </Field>
            <Field label={t("Usuario")}>
              <TextInput value={c.email.user} onChange={(e) => upd("email", { user: e.target.value })} autoComplete="off" />
            </Field>
            <Field label={t("Contraseña")}>
              <TextInput type="password" value={c.email.password} onChange={(e) => upd("email", { password: e.target.value })} autoComplete="new-password" />
            </Field>
            <Field label={t("Remitente")}>
              <TextInput value={c.email.from} onChange={(e) => upd("email", { from: e.target.value })} placeholder="tango@casa.lan" />
            </Field>
            <Field label={t("Destinatarios")}>
              <TextInput value={c.email.to} onChange={(e) => upd("email", { to: e.target.value })} placeholder={t("vos@ejemplo.com, otra@ejemplo.com")} />
            </Field>
          </div>
        )}
      </Group>

      <Group title="ntfy">
        <Row icon={<Icon icon={MessageCircle} color="#8B5CF6" />} label={t("Avisar por ntfy")} hint={t("Notificaciones push al teléfono, en ntfy.sh o en tu propio servidor")}>
          {c.ntfy.enabled && (
            <Button variant="plain" onClick={() => void test("ntfy")}>
              {t("Enviar prueba")}
            </Button>
          )}
          <Toggle checked={c.ntfy.enabled} onChange={(v) => upd("ntfy", { enabled: v })} label="ntfy" />
        </Row>
        {c.ntfy.enabled && (
          <div className="tset-form">
            <Field label={t("Servidor")}>
              <TextInput value={c.ntfy.server} onChange={(e) => upd("ntfy", { server: e.target.value })} placeholder="https://ntfy.sh" />
            </Field>
            <Field label={t("Tema")}>
              <TextInput value={c.ntfy.topic} onChange={(e) => upd("ntfy", { topic: e.target.value })} placeholder="tango-casa" />
            </Field>
            <Field label={t("Token (opcional)")}>
              <TextInput type="password" value={c.ntfy.token} onChange={(e) => upd("ntfy", { token: e.target.value })} autoComplete="off" />
            </Field>
          </div>
        )}
      </Group>

      <Group title="Telegram">
        <Row icon={<Icon icon={Send} color="#0ea5e9" />} label={t("Avisar por Telegram")} hint={t("Creá un bot con @BotFather y pegá su token")}>
          {c.telegram.enabled && (
            <Button variant="plain" onClick={() => void test("telegram")}>
              {t("Enviar prueba")}
            </Button>
          )}
          <Toggle checked={c.telegram.enabled} onChange={(v) => upd("telegram", { enabled: v })} label="Telegram" />
        </Row>
        {c.telegram.enabled && (
          <div className="tset-form">
            <Field label={t("Token del bot")}>
              <TextInput type="password" value={c.telegram.botToken} onChange={(e) => upd("telegram", { botToken: e.target.value })} autoComplete="off" />
            </Field>
            <Field label={t("Chat")}>
              <TextInput value={c.telegram.chatId} onChange={(e) => upd("telegram", { chatId: e.target.value })} placeholder="123456789" />
            </Field>
          </div>
        )}
      </Group>

      <Group title={t("Qué avisar fuera del escritorio")}>
        {LEVELS.map((l) => (
          <Row key={l.id} label={t(l.label)} hint={t(l.hint)}>
            <Toggle checked={c.levels.includes(l.id)} onChange={(v) => toggleLevel(l.id, v)} label={t(l.label)} />
          </Row>
        ))}
      </Group>

      {err && <div className="tset-err">{err}</div>}
      <div className="tset-actions">
        <Button
          onClick={() => {
            setDirty(false);
            if (data) setC(structuredClone(data));
          }}
          disabled={!dirty || saving}
        >
          {t("Descartar")}
        </Button>
        <Button variant="primary" onClick={() => void save()} disabled={!dirty || saving}>
          {saving ? t("Guardando…") : t("Guardar")}
        </Button>
      </div>
    </>
  );
}
