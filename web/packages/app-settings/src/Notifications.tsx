import { useEffect, useState } from "react";
import { Mail, MessageCircle, Send } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { RpcError, useIsReal, useLive, useTransport } from "@tango/client";
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
  const t = useTransport();
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
      await t.call("notify.channels.set", c);
      setDirty(false);
      toast({ level: "ok", title: "Notificaciones guardadas", app: "tango.settings" });
    } catch (e) {
      setErr(e instanceof RpcError ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const test = async (channel: "email" | "ntfy" | "telegram") => {
    if (dirty) {
      toast({ level: "info", title: "Guardá los cambios primero", body: "La prueba usa la configuración guardada.", app: "tango.settings" });
      return;
    }
    try {
      await t.call("notify.test", { channel });
      toast({ level: "ok", title: "Prueba enviada", body: "Si no llega en un minuto, revisá los datos del canal.", app: "tango.settings" });
    } catch (e) {
      toast({ level: "warn", title: "La prueba falló", body: e instanceof RpcError ? e.message : String(e), app: "tango.settings" });
    }
  };

  const toggleLevel = (l: Level, on: boolean) => {
    setDirty(true);
    setC((x) => (x ? { ...x, levels: on ? [...new Set([...x.levels, l])] : x.levels.filter((y) => y !== l) } : x));
  };

  return (
    <>
      <Group title="En el escritorio">
        <Row label="Avisos en pantalla" hint="Discos, volúmenes, tareas terminadas y actualizaciones">
          <Status tone="ok">Activos</Status>
        </Row>
      </Group>

      <Group
        title={
          <>
            Correo {!real && <DemoBadge />}
          </>
        }
      >
        <Row icon={<Icon icon={Mail} color="#2F7CF6" />} label="Avisar por correo" hint="Por SMTP, con tu cuenta o un servicio transaccional">
          {c.email.enabled && (
            <Button variant="plain" onClick={() => void test("email")}>
              Enviar prueba
            </Button>
          )}
          <Toggle checked={c.email.enabled} onChange={(v) => upd("email", { enabled: v })} label="Correo" />
        </Row>
        {c.email.enabled && (
          <div className="tset-form">
            <Field label="Servidor SMTP">
              <TextInput value={c.email.host} onChange={(e) => upd("email", { host: e.target.value })} placeholder="smtp.ejemplo.com" />
            </Field>
            <Field label="Puerto">
              <TextInput value={String(c.email.port)} inputMode="numeric" onChange={(e) => upd("email", { port: Number(e.target.value.replace(/\D/g, "")) || 0 })} />
            </Field>
            <Field label="Cifrado">
              <Segmented
                label="Cifrado"
                value={c.email.tls}
                onChange={(v) => upd("email", { tls: v })}
                options={[
                  { value: "starttls", label: "STARTTLS" },
                  { value: "tls", label: "TLS" },
                  { value: "none", label: "Ninguno" },
                ]}
              />
            </Field>
            <Field label="Usuario">
              <TextInput value={c.email.user} onChange={(e) => upd("email", { user: e.target.value })} autoComplete="off" />
            </Field>
            <Field label="Contraseña">
              <TextInput type="password" value={c.email.password} onChange={(e) => upd("email", { password: e.target.value })} autoComplete="new-password" />
            </Field>
            <Field label="Remitente">
              <TextInput value={c.email.from} onChange={(e) => upd("email", { from: e.target.value })} placeholder="tango@casa.lan" />
            </Field>
            <Field label="Destinatarios">
              <TextInput value={c.email.to} onChange={(e) => upd("email", { to: e.target.value })} placeholder="vos@ejemplo.com, otra@ejemplo.com" />
            </Field>
          </div>
        )}
      </Group>

      <Group title="ntfy">
        <Row icon={<Icon icon={MessageCircle} color="#8B5CF6" />} label="Avisar por ntfy" hint="Notificaciones push al teléfono, en ntfy.sh o en tu propio servidor">
          {c.ntfy.enabled && (
            <Button variant="plain" onClick={() => void test("ntfy")}>
              Enviar prueba
            </Button>
          )}
          <Toggle checked={c.ntfy.enabled} onChange={(v) => upd("ntfy", { enabled: v })} label="ntfy" />
        </Row>
        {c.ntfy.enabled && (
          <div className="tset-form">
            <Field label="Servidor">
              <TextInput value={c.ntfy.server} onChange={(e) => upd("ntfy", { server: e.target.value })} placeholder="https://ntfy.sh" />
            </Field>
            <Field label="Tema">
              <TextInput value={c.ntfy.topic} onChange={(e) => upd("ntfy", { topic: e.target.value })} placeholder="tango-casa" />
            </Field>
            <Field label="Token (opcional)">
              <TextInput type="password" value={c.ntfy.token} onChange={(e) => upd("ntfy", { token: e.target.value })} autoComplete="off" />
            </Field>
          </div>
        )}
      </Group>

      <Group title="Telegram">
        <Row icon={<Icon icon={Send} color="#0ea5e9" />} label="Avisar por Telegram" hint="Creá un bot con @BotFather y pegá su token">
          {c.telegram.enabled && (
            <Button variant="plain" onClick={() => void test("telegram")}>
              Enviar prueba
            </Button>
          )}
          <Toggle checked={c.telegram.enabled} onChange={(v) => upd("telegram", { enabled: v })} label="Telegram" />
        </Row>
        {c.telegram.enabled && (
          <div className="tset-form">
            <Field label="Token del bot">
              <TextInput type="password" value={c.telegram.botToken} onChange={(e) => upd("telegram", { botToken: e.target.value })} autoComplete="off" />
            </Field>
            <Field label="Chat">
              <TextInput value={c.telegram.chatId} onChange={(e) => upd("telegram", { chatId: e.target.value })} placeholder="123456789" />
            </Field>
          </div>
        )}
      </Group>

      <Group title="Qué avisar fuera del escritorio">
        {LEVELS.map((l) => (
          <Row key={l.id} label={l.label} hint={l.hint}>
            <Toggle checked={c.levels.includes(l.id)} onChange={(v) => toggleLevel(l.id, v)} label={l.label} />
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
          Descartar
        </Button>
        <Button variant="primary" onClick={() => void save()} disabled={!dirty || saving}>
          {saving ? "Guardando…" : "Guardar"}
        </Button>
      </div>
    </>
  );
}
