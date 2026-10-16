import { useEffect, useState } from "react";
import { Copy, Laptop, Link2, Settings2, ShieldCheck, Trash2 } from "lucide-react";
import { fmtAgo, fmtBytes, t, tn, tr, useIsReal, useLive, useTransport } from "@tango/client";
import type { ConnectLink, ShareLinkInfo } from "@tango/client";
import { toast } from "@tango/shell";
import { Badge, Button, DemoBadge, EmptyState, Group, Row, Sheet, Status, TextInput, Toggle } from "@tango/ui";
import { Value, tj } from "./common";

/** TangoConnect (protocol/README.md §15): acceso remoto sin abrir puertos y enlaces públicos. */
export function ConnectSection() {
  const tp = useTransport();
  const real = useIsReal("connect.status");
  const st = useLive("connect.status", undefined, ["connect.changed"]);
  const links = useLive("connect.links.list", undefined, ["connect.changed"]);
  const shares = useLive("shares.links.list", { all: true }, ["connect.changed"]);
  const s = st.data;
  const [busy, setBusy] = useState(false);
  const [pairing, setPairing] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [revoke, setRevoke] = useState<ConnectLink | null>(null);

  const toggle = async (enabled: boolean) => {
    setBusy(true);
    try {
      await tp.call("connect.enable", { enabled });
      st.reload();
      toast({ level: "ok", title: enabled ? t("TangoConnect activado") : t("TangoConnect desactivado"), body: enabled ? t("El NAS se conecta al servidor de señalización. Ahora podés vincular un navegador.") : t("Nadie puede entrar desde internet. Los enlaces públicos dejan de funcionar."), app: "tango.settings" });
    } catch (e) {
      toast({ level: "crit", title: t("No se pudo cambiar"), body: tr((e as Error).message), app: "tango.settings" });
    }
    setBusy(false);
  };

  const copy = async (text: string, what: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast({ level: "ok", title: t("{what} copiado", { what }), app: "tango.settings" });
    } catch {
      toast({ level: "warn", title: t("No se pudo copiar"), body: t("Seleccionalo y copialo a mano."), app: "tango.settings" });
    }
  };

  const signalHost = s ? safeHost(s.signal) : "";
  return (
    <>
      <p className="tg-lead">
        {t("Entrá a tu NAS desde cualquier lugar sin abrir puertos ni configurar el router. La conexión es directa entre tu navegador y el NAS, cifrada de punta a punta; el servidor de señalización solo los presenta.")} {!real && <DemoBadge />}
      </p>

      <Group title={t("Acceso remoto")}>
        <Row label={t("Activar TangoConnect")} hint={s?.enabled ? t("El NAS mantiene una conexión saliente con el servidor de señalización.") : t("Apagado: el NAS no es accesible desde internet.")}>
          <Toggle checked={!!s?.enabled} onChange={(v) => void toggle(v)} disabled={busy || !s} label={t("Activar TangoConnect")} />
        </Row>
        {s?.enabled && (
          <Row label={t("Estado")} hint={s.connected ? (s.hasTurn ? t("Conectado a {host} · relay disponible si no hay conexión directa", { host: signalHost }) : t("Conectado a {host}", { host: signalHost })) : s.lastError ? t("Sin conexión: {error}", { error: tr(s.lastError) }) : t("Conectando con {host}…", { host: signalHost })}>
            {s.connected ? <Status tone="ok">{t("Conectado")}</Status> : <Status tone="warn">{t("Sin conexión")}</Status>}
          </Row>
        )}
        {s && (
          <Row label={t("Huella del NAS")} hint={t("El portal la guarda al vincular y la compara en cada conexión: si cambia, avisa.")}>
            <Button variant="plain" onClick={() => void copy(s.fingerprint, t("La huella"))} title={t("Copiar huella")}>
              <Value>{s.fingerprint.slice(0, 26)}…</Value> <Copy size={13} />
            </Button>
          </Row>
        )}
        <Row label={t("Portal")} hint={t("Desde ahí ves tus NAS y abrís el escritorio completo.")}>
          <Value>{s ? safeHost(s.portal) : "—"}</Value>
          <Button variant="plain" onClick={() => setAdvanced(true)} title={t("Opciones avanzadas")} aria-label={t("Opciones avanzadas")}>
            <Settings2 size={14} />
          </Button>
        </Row>
      </Group>

      <Group title={t("Navegadores vinculados")}>
        <Row label={t("Vincular un navegador")} hint={t("Te damos un código de 9 dígitos para escribir en el portal. Vale 10 minutos y un solo uso.")}>
          <Button variant="primary" disabled={!s?.enabled || !s.connected} onClick={() => setPairing(true)}>
            <Link2 size={14} /> {t("Vincular navegador…")}
          </Button>
        </Row>
        {(links.data ?? []).map((l) => (
          <Row
            key={l.id}
            icon={<span className="tset-row-icon" style={{ background: "#2F7CF6" }}><Laptop size={14} /></span>}
            label={l.label}
            hint={`${t("vinculado {ago}", { ago: fmtAgo(l.createdAt) })}${l.lastSeen ? ` · ${t("última vez {ago}", { ago: fmtAgo(l.lastSeen) })}` : ""}${l.ip ? ` · ${l.ip}` : ""}`}
          >
            <Button variant="plain" onClick={() => setRevoke(l)} title={t("Desvincular {name}", { name: l.label })} aria-label={t("Desvincular {name}", { name: l.label })}>
              <Trash2 size={14} />
            </Button>
          </Row>
        ))}
        {links.data && links.data.length === 0 && <Row label={<span className="tg-muted">{t("Ningún navegador vinculado todavía.")}</span>} />}
      </Group>

      <Group title={t("Enlaces públicos")}>
        {(shares.data ?? []).length === 0 ? (
          <Row label={<span className="tg-muted">{t("No hay enlaces. Creá uno desde Archivos: clic derecho → Compartir enlace…")}</span>} />
        ) : (
          (shares.data ?? []).map((l) => <ShareRow key={l.id} l={l} onCopy={() => void copy(l.url, t("El enlace"))} onRevoked={() => shares.reload()} />)
        )}
      </Group>

      <p className="tg-muted" style={{ fontSize: 12, marginTop: 14, display: "flex", gap: 6, alignItems: "flex-start" }}>
        <ShieldCheck size={14} style={{ flex: "none", marginTop: 1 }} />
        {t("Para entrar desde afuera igual hace falta tu usuario y contraseña (y la verificación en dos pasos si la activaste). El NAS se identifica primero ante el navegador: un servidor falso no puede robarte el vínculo.")}
      </p>

      {pairing && <PairSheet onClose={() => (setPairing(false), links.reload())} portal={s?.portal ?? ""} />}
      {advanced && s && <AdvancedSheet signal={s.signal} portal={s.portal} enabled={s.enabled} onClose={() => (setAdvanced(false), st.reload())} />}
      {revoke && (
        <Sheet
          open
          onClose={() => setRevoke(null)}
          width={420}
          footer={
            <>
              <Button onClick={() => setRevoke(null)}>{t("Cancelar")}</Button>
              <Button
                variant="danger"
                onClick={async () => {
                  try {
                    await tp.call("connect.links.revoke", { id: revoke.id });
                    toast({ level: "ok", title: t("Se desvinculó «{name}»", { name: revoke.label }), body: t("Si estaba conectado, la conexión se cortó."), app: "tango.settings" });
                    links.reload();
                  } catch (e) {
                    toast({ level: "crit", title: t("No se pudo desvincular"), body: tr((e as Error).message), app: "tango.settings" });
                  }
                  setRevoke(null);
                }}
              >
                {t("Desvincular")}
              </Button>
            </>
          }
        >
          <h2 className="tg-h2">{t("¿Desvincular «{name}»?", { name: revoke.label })}</h2>
          <p className="tg-lead" style={{ marginTop: 4 }}>
            {t("Ese navegador ya no va a poder entrar desde el portal. Para volver a usarlo hay que vincularlo de nuevo con un código.")}
          </p>
        </Sheet>
      )}
    </>
  );
}

function ShareRow({ l, onCopy, onRevoked }: { l: ShareLinkInfo; onCopy: () => void; onRevoked: () => void }) {
  const tp = useTransport();
  const meta = [
    l.kind === "dir" ? t("carpeta") : fmtBytes(l.size),
    t("de {owner}", { owner: l.owner }),
    l.hasPassword ? t("con contraseña") : null,
    l.expiresAt ? t("vence {ago}", { ago: fmtAgo(l.expiresAt) }) : t("sin vencimiento"),
    l.maxDownloads ? t("{n}/{max} descargas", { n: l.downloads, max: l.maxDownloads }) : tn(l.downloads, "{n} descarga", "{n} descargas"),
  ].filter(Boolean);
  return (
    <Row label={<span className="tset-uname">{l.name} {l.expired && <Badge tone="warn">{t("Vencido")}</Badge>}</span>} hint={`${l.path} · ${meta.join(" · ")}`}>
      <Button variant="plain" onClick={onCopy} title={t("Copiar enlace")} aria-label={t("Copiar enlace")}>
        <Copy size={14} />
      </Button>
      <Button
        variant="plain"
        title={t("Revocar enlace")}
        aria-label={t("Revocar enlace")}
        onClick={async () => {
          try {
            await tp.call("shares.links.revoke", { id: l.id });
            toast({ level: "ok", title: t("Enlace revocado"), body: t("«{name}» ya no se puede abrir.", { name: l.name }), app: "tango.settings" });
            onRevoked();
          } catch (e) {
            toast({ level: "crit", title: t("No se pudo revocar"), body: tr((e as Error).message), app: "tango.settings" });
          }
        }}
      >
        <Trash2 size={14} />
      </Button>
    </Row>
  );
}

function PairSheet({ onClose, portal }: { onClose: () => void; portal: string }) {
  const tp = useTransport();
  const [code, setCode] = useState<{ code: string; expiresAt: string } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [left, setLeft] = useState(0);

  const ask = async () => {
    setErr(null);
    try {
      setCode(await tp.call("connect.pair.start", undefined));
    } catch (e) {
      setErr(tr((e as Error).message));
    }
  };
  useEffect(() => {
    void ask();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    if (!code) return;
    const tick = () => setLeft(Math.max(0, Math.round((new Date(code.expiresAt).getTime() - Date.now()) / 1000)));
    tick();
    const i = setInterval(tick, 1000);
    return () => clearInterval(i);
  }, [code]);

  const expired = code && left === 0;
  return (
    <Sheet
      open
      onClose={onClose}
      width={460}
      footer={
        <>
          {expired && <Button onClick={() => void ask()}>{t("Pedir otro código")}</Button>}
          <Button variant="primary" onClick={onClose}>
            {t("Listo")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Vincular un navegador")}</h2>
      <p className="tg-lead" style={{ marginTop: 4 }}>
        {tj("En el navegador que querés vincular, entrá a {portal}, tocá «Agregar NAS» y escribí este código.", { portal: <strong>{safeHost(portal)}</strong> })}
      </p>
      {err ? (
        <EmptyState icon={Link2} title={t("No se pudo pedir el código")}>
          {err}
        </EmptyState>
      ) : (
        <div className="tset-paircode" aria-live="polite">
          <span className="tset-paircode-digits" data-expired={expired || undefined}>
            {code ? code.code : "··· ··· ···"}
          </span>
          <span className="tg-muted">{code ? (expired ? t("Venció: pedí otro.") : t("Vence en {time}", { time: `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}` })) : t("Pidiendo código…")}</span>
        </div>
      )}
    </Sheet>
  );
}

function AdvancedSheet({ signal, portal, enabled, onClose }: { signal: string; portal: string; enabled: boolean; onClose: () => void }) {
  const tp = useTransport();
  const [sig, setSig] = useState(signal);
  const [por, setPor] = useState(portal);
  const [err, setErr] = useState<string | null>(null);
  const save = async () => {
    setErr(null);
    try {
      await tp.call("connect.enable", { enabled, signalURL: sig.trim(), portalURL: por.trim() });
      toast({ level: "ok", title: t("Se guardó la configuración de TangoConnect"), app: "tango.settings" });
      onClose();
    } catch (e) {
      setErr(tr((e as Error).message));
    }
  };
  return (
    <Sheet
      open
      onClose={onClose}
      width={520}
      footer={
        <>
          {err && <span className="tset-err" style={{ marginRight: "auto", marginTop: 0 }}>{err}</span>}
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" onClick={() => void save()}>
            {t("Guardar")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Servidores de TangoConnect")}</h2>
      <p className="tg-lead" style={{ marginTop: 4 }}>
        {t("Cambialos solo si usás tu propio servidor de señalización (signalhub) o tu propio portal.")}
      </p>
      <form className="tset-form" onSubmit={(e) => (e.preventDefault(), void save())}>
        <label>
          <span>{t("Servidor de señalización")}</span>
          <TextInput value={sig} onChange={(e) => setSig(e.target.value)} placeholder="wss://signal.go-link.org/ws?v=1" spellCheck={false} />
        </label>
        <label>
          <span>{t("Portal")}</span>
          <TextInput value={por} onChange={(e) => setPor(e.target.value)} placeholder="https://tangoos.go-link.org" spellCheck={false} />
        </label>
        <button type="submit" hidden />
      </form>
    </Sheet>
  );
}

function safeHost(u: string): string {
  try {
    return new URL(u).host;
  } catch {
    return u;
  }
}
