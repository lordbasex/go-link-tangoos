import { useEffect, useState } from "react";
import { Copy, Laptop, Link2, Settings2, ShieldCheck, Trash2 } from "lucide-react";
import { fmtAgo, fmtBytes, useIsReal, useLive, useTransport } from "@tango/client";
import type { ConnectLink, ShareLinkInfo } from "@tango/client";
import { toast } from "@tango/shell";
import { Badge, Button, DemoBadge, EmptyState, Group, Row, Sheet, Status, TextInput, Toggle } from "@tango/ui";
import { Value } from "./common";

/** TangoConnect (protocol/README.md §15): acceso remoto sin abrir puertos y enlaces públicos. */
export function ConnectSection() {
  const tr = useTransport();
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
      await tr.call("connect.enable", { enabled });
      st.reload();
      toast({ level: "ok", title: enabled ? "TangoConnect activado" : "TangoConnect desactivado", body: enabled ? "El NAS se conecta al servidor de señalización. Ahora podés vincular un navegador." : "Nadie puede entrar desde internet. Los enlaces públicos dejan de funcionar.", app: "tango.settings" });
    } catch (e) {
      toast({ level: "crit", title: "No se pudo cambiar", body: (e as Error).message, app: "tango.settings" });
    }
    setBusy(false);
  };

  const copy = async (text: string, what: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast({ level: "ok", title: `${what} copiado`, app: "tango.settings" });
    } catch {
      toast({ level: "warn", title: "No se pudo copiar", body: "Seleccionalo y copialo a mano.", app: "tango.settings" });
    }
  };

  const signalHost = s ? safeHost(s.signal) : "";
  return (
    <>
      <p className="tg-lead">
        Entrá a tu NAS desde cualquier lugar sin abrir puertos ni configurar el router. La conexión es directa entre tu navegador y el NAS, cifrada de punta a punta; el servidor de señalización solo los presenta. {!real && <DemoBadge />}
      </p>

      <Group title="Acceso remoto">
        <Row label="Activar TangoConnect" hint={s?.enabled ? "El NAS mantiene una conexión saliente con el servidor de señalización." : "Apagado: el NAS no es accesible desde internet."}>
          <Toggle checked={!!s?.enabled} onChange={(v) => void toggle(v)} disabled={busy || !s} label="Activar TangoConnect" />
        </Row>
        {s?.enabled && (
          <Row label="Estado" hint={s.connected ? `Conectado a ${signalHost}${s.hasTurn ? " · relay disponible si no hay conexión directa" : ""}` : s.lastError ? `Sin conexión: ${s.lastError}` : `Conectando con ${signalHost}…`}>
            {s.connected ? <Status tone="ok">Conectado</Status> : <Status tone="warn">Sin conexión</Status>}
          </Row>
        )}
        {s && (
          <Row label="Huella del NAS" hint="El portal la guarda al vincular y la compara en cada conexión: si cambia, avisa.">
            <Button variant="plain" onClick={() => void copy(s.fingerprint, "La huella")} title="Copiar huella">
              <Value>{s.fingerprint.slice(0, 26)}…</Value> <Copy size={13} />
            </Button>
          </Row>
        )}
        <Row label="Portal" hint="Desde ahí ves tus NAS y abrís el escritorio completo.">
          <Value>{s ? safeHost(s.portal) : "—"}</Value>
          <Button variant="plain" onClick={() => setAdvanced(true)} title="Opciones avanzadas" aria-label="Opciones avanzadas">
            <Settings2 size={14} />
          </Button>
        </Row>
      </Group>

      <Group title="Navegadores vinculados">
        <Row label="Vincular un navegador" hint="Te damos un código de 9 dígitos para escribir en el portal. Vale 10 minutos y un solo uso.">
          <Button variant="primary" disabled={!s?.enabled || !s.connected} onClick={() => setPairing(true)}>
            <Link2 size={14} /> Vincular navegador…
          </Button>
        </Row>
        {(links.data ?? []).map((l) => (
          <Row
            key={l.id}
            icon={<span className="tset-row-icon" style={{ background: "#2F7CF6" }}><Laptop size={14} /></span>}
            label={l.label}
            hint={`vinculado ${fmtAgo(l.createdAt)}${l.lastSeen ? ` · última vez ${fmtAgo(l.lastSeen)}` : ""}${l.ip ? ` · ${l.ip}` : ""}`}
          >
            <Button variant="plain" onClick={() => setRevoke(l)} title={`Desvincular ${l.label}`} aria-label={`Desvincular ${l.label}`}>
              <Trash2 size={14} />
            </Button>
          </Row>
        ))}
        {links.data && links.data.length === 0 && <Row label={<span className="tg-muted">Ningún navegador vinculado todavía.</span>} />}
      </Group>

      <Group title="Enlaces públicos">
        {(shares.data ?? []).length === 0 ? (
          <Row label={<span className="tg-muted">No hay enlaces. Creá uno desde Archivos: clic derecho → Compartir enlace…</span>} />
        ) : (
          (shares.data ?? []).map((l) => <ShareRow key={l.id} l={l} onCopy={() => void copy(l.url, "El enlace")} onRevoked={() => shares.reload()} />)
        )}
      </Group>

      <p className="tg-muted" style={{ fontSize: 12, marginTop: 14, display: "flex", gap: 6, alignItems: "flex-start" }}>
        <ShieldCheck size={14} style={{ flex: "none", marginTop: 1 }} />
        Para entrar desde afuera igual hace falta tu usuario y contraseña (y la verificación en dos pasos si la activaste). El NAS se identifica primero ante el navegador: un servidor falso no puede robarte el vínculo.
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
              <Button onClick={() => setRevoke(null)}>Cancelar</Button>
              <Button
                variant="danger"
                onClick={async () => {
                  try {
                    await tr.call("connect.links.revoke", { id: revoke.id });
                    toast({ level: "ok", title: `Se desvinculó «${revoke.label}»`, body: "Si estaba conectado, la conexión se cortó.", app: "tango.settings" });
                    links.reload();
                  } catch (e) {
                    toast({ level: "crit", title: "No se pudo desvincular", body: (e as Error).message, app: "tango.settings" });
                  }
                  setRevoke(null);
                }}
              >
                Desvincular
              </Button>
            </>
          }
        >
          <h2 className="tg-h2">¿Desvincular «{revoke.label}»?</h2>
          <p className="tg-lead" style={{ marginTop: 4 }}>Ese navegador ya no va a poder entrar desde el portal. Para volver a usarlo hay que vincularlo de nuevo con un código.</p>
        </Sheet>
      )}
    </>
  );
}

function ShareRow({ l, onCopy, onRevoked }: { l: ShareLinkInfo; onCopy: () => void; onRevoked: () => void }) {
  const tr = useTransport();
  const meta = [
    l.kind === "dir" ? "carpeta" : fmtBytes(l.size),
    `de ${l.owner}`,
    l.hasPassword ? "con contraseña" : null,
    l.expiresAt ? `vence ${fmtAgo(l.expiresAt)}` : "sin vencimiento",
    l.maxDownloads ? `${l.downloads}/${l.maxDownloads} descargas` : `${l.downloads} descargas`,
  ].filter(Boolean);
  return (
    <Row label={<span className="tset-uname">{l.name} {l.expired && <Badge tone="warn">Vencido</Badge>}</span>} hint={`${l.path} · ${meta.join(" · ")}`}>
      <Button variant="plain" onClick={onCopy} title="Copiar enlace" aria-label="Copiar enlace">
        <Copy size={14} />
      </Button>
      <Button
        variant="plain"
        title="Revocar enlace"
        aria-label="Revocar enlace"
        onClick={async () => {
          try {
            await tr.call("shares.links.revoke", { id: l.id });
            toast({ level: "ok", title: "Enlace revocado", body: `«${l.name}» ya no se puede abrir.`, app: "tango.settings" });
            onRevoked();
          } catch (e) {
            toast({ level: "crit", title: "No se pudo revocar", body: (e as Error).message, app: "tango.settings" });
          }
        }}
      >
        <Trash2 size={14} />
      </Button>
    </Row>
  );
}

function PairSheet({ onClose, portal }: { onClose: () => void; portal: string }) {
  const tr = useTransport();
  const [code, setCode] = useState<{ code: string; expiresAt: string } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [left, setLeft] = useState(0);

  const ask = async () => {
    setErr(null);
    try {
      setCode(await tr.call("connect.pair.start", undefined));
    } catch (e) {
      setErr((e as Error).message);
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
          {expired && <Button onClick={() => void ask()}>Pedir otro código</Button>}
          <Button variant="primary" onClick={onClose}>
            Listo
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">Vincular un navegador</h2>
      <p className="tg-lead" style={{ marginTop: 4 }}>
        En el navegador que querés vincular, entrá a <strong>{safeHost(portal)}</strong>, tocá «Agregar NAS» y escribí este código.
      </p>
      {err ? (
        <EmptyState icon={Link2} title="No se pudo pedir el código">
          {err}
        </EmptyState>
      ) : (
        <div className="tset-paircode" aria-live="polite">
          <span className="tset-paircode-digits" data-expired={expired || undefined}>
            {code ? code.code : "··· ··· ···"}
          </span>
          <span className="tg-muted">{code ? (expired ? "Venció: pedí otro." : `Vence en ${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`) : "Pidiendo código…"}</span>
        </div>
      )}
    </Sheet>
  );
}

function AdvancedSheet({ signal, portal, enabled, onClose }: { signal: string; portal: string; enabled: boolean; onClose: () => void }) {
  const tr = useTransport();
  const [sig, setSig] = useState(signal);
  const [por, setPor] = useState(portal);
  const [err, setErr] = useState<string | null>(null);
  const save = async () => {
    setErr(null);
    try {
      await tr.call("connect.enable", { enabled, signalURL: sig.trim(), portalURL: por.trim() });
      toast({ level: "ok", title: "Se guardó la configuración de TangoConnect", app: "tango.settings" });
      onClose();
    } catch (e) {
      setErr((e as Error).message);
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
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="primary" onClick={() => void save()}>
            Guardar
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">Servidores de TangoConnect</h2>
      <p className="tg-lead" style={{ marginTop: 4 }}>Cambialos solo si usás tu propio servidor de señalización (signalhub) o tu propio portal.</p>
      <form className="tset-form" onSubmit={(e) => (e.preventDefault(), void save())}>
        <label>
          <span>Servidor de señalización</span>
          <TextInput value={sig} onChange={(e) => setSig(e.target.value)} placeholder="wss://signal.go-link.org/ws?v=1" spellCheck={false} />
        </label>
        <label>
          <span>Portal</span>
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
