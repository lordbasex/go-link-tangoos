import { useState } from "react";
import { Copy, KeyRound, Plus, Trash2 } from "lucide-react";
import { RpcError, fmtAgo, t, tr, useIsReal, useLive, useTransport } from "@tango/client";
import type { AppPassword } from "@tango/client";
import { toast } from "@tango/shell";
import { Badge, Button, DemoBadge, Row, Sheet, TextInput, Toggle } from "@tango/ui";

/** Contraseñas de app (protocol/README.md §14): para WebDAV, SFTP y SMB cuando hay 2FA. */
export function AppPasswords() {
  const q = useLive("users.appPasswords.list", {});
  const list = q.data ?? [];
  const real = useIsReal("users.appPasswords.list");
  const tp = useTransport();
  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState<AppPassword | null>(null);

  const revoke = async (p: AppPassword) => {
    try {
      await tp.call("users.appPasswords.revoke", { id: p.id });
      q.reload();
      toast({ level: "ok", title: t("Se revocó «{name}»", { name: p.label }), body: p.smb ? t("SMB queda cerrado hasta que crees otra contraseña de app para SMB.") : t("Las apps que la usaban tienen que volver a configurarse."), app: "tango.settings" });
    } catch (e) {
      toast({ level: "crit", title: t("No se pudo revocar"), body: tr((e as Error).message), app: "tango.settings" });
    }
    setRevoking(null);
  };

  return (
    <>
      <Row
        icon={
          <span className="tset-row-icon" style={{ background: "#8B5CF6" }}>
            <KeyRound size={14} />
          </span>
        }
        label={
          <span className="tset-uname">
            {t("Contraseñas de app")} {!real && <DemoBadge />}
          </span>
        }
        hint={t("Para WebDAV y SFTP (y SMB si activás la verificación en dos pasos): esos protocolos no pueden pedir el código.")}
      >
        <Button onClick={() => setCreating(true)}>
          <Plus size={14} /> {t("Crear…")}
        </Button>
      </Row>
      {list.map((p) => (
        <Row key={p.id} label={<span className="tset-uname">{p.label}{p.smb && <Badge tone="info">SMB</Badge>}</span>} hint={`${t("creada {ago}", { ago: fmtAgo(p.created) })} · ${p.lastUsed ? t("usada {ago}", { ago: fmtAgo(p.lastUsed) }) : t("sin usar")}`}>
          <Button variant="plain" onClick={() => setRevoking(p)} title={t("Revocar «{name}»", { name: p.label })} aria-label={t("Revocar «{name}»", { name: p.label })}>
            <Trash2 size={14} />
          </Button>
        </Row>
      ))}
      {creating && <CreateAppPassword onClose={() => setCreating(false)} onCreated={() => q.reload()} />}
      {revoking && (
        <Sheet
          open
          onClose={() => setRevoking(null)}
          width={420}
          footer={
            <>
              <Button onClick={() => setRevoking(null)}>{t("Cancelar")}</Button>
              <Button variant="danger" onClick={() => void revoke(revoking)}>
                {t("Revocar")}
              </Button>
            </>
          }
        >
          <h2 className="tg-h2">{t("¿Revocar «{name}»?", { name: revoking.label })}</h2>
          <p className="tg-lead" style={{ marginTop: 4 }}>
            {revoking.smb ? t("Lo que la use deja de conectarse enseguida. Es tu contraseña de SMB: vas a necesitar otra para entrar por SMB.") : t("Lo que la use deja de conectarse enseguida.")}
          </p>
        </Sheet>
      )}
    </>
  );
}

function CreateAppPassword({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const tp = useTransport();
  const [label, setLabel] = useState("");
  const [smb, setSmb] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [created, setCreated] = useState<{ label: string; password: string } | null>(null);

  const submit = async () => {
    if (!label.trim() || busy) return;
    setBusy(true);
    setErr(null);
    try {
      const r = await tp.call("users.appPasswords.create", { label: label.trim(), smb });
      setCreated({ label: r.label, password: r.password });
      onCreated();
    } catch (e) {
      setErr(e instanceof RpcError && e.code === "TOO_MANY" ? t("Ya tenés 20 contraseñas de app: revocá alguna que no uses.") : tr((e as Error).message));
    }
    setBusy(false);
  };
  const copy = async () => {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.password);
      toast({ level: "ok", title: t("Contraseña copiada"), app: "tango.settings" });
    } catch {
      toast({ level: "warn", title: t("No se pudo copiar"), body: t("Seleccionala y copiala a mano."), app: "tango.settings" });
    }
  };

  return (
    <Sheet
      open
      onClose={onClose}
      width={460}
      footer={
        created ? (
          <Button variant="primary" onClick={onClose}>
            {t("Listo")}
          </Button>
        ) : (
          <>
            {err && (
              <span className="tset-err" style={{ marginRight: "auto", marginTop: 0 }}>
                {err}
              </span>
            )}
            <Button onClick={onClose}>{t("Cancelar")}</Button>
            <Button variant="primary" onClick={() => void submit()} disabled={!label.trim() || busy}>
              {t("Crear contraseña")}
            </Button>
          </>
        )
      }
    >
      {created ? (
        <>
          <h2 className="tg-h2">{t("Contraseña para «{name}»", { name: created.label })}</h2>
          <p className="tg-lead" style={{ marginTop: 4 }}>
            {t("Copiala ahora: no se vuelve a mostrar. Usala en lugar de tu contraseña en la app o equipo que vas a conectar.")}
          </p>
          <div className="tset-apppw">
            <code>{created.password}</code>
            <Button onClick={() => void copy()}>
              <Copy size={14} /> {t("Copiar")}
            </Button>
          </div>
        </>
      ) : (
        <>
          <h2 className="tg-h2">{t("Nueva contraseña de app")}</h2>
          <p className="tg-lead" style={{ marginTop: 4 }}>
            {t("Cada equipo o app con su propia contraseña: si perdés uno, revocás solo esa.")}
          </p>
          <form className="tset-form" onSubmit={(e) => (e.preventDefault(), void submit())}>
            <label>
              <span>{t("Para qué es")}</span>
              <TextInput value={label} onChange={(e) => setLabel(e.target.value)} placeholder={t("Notebook de trabajo · WebDAV")} autoFocus maxLength={64} />
            </label>
            <div className="tset-form-row">
              <span>{t("Usar para SMB")}</span>
              <Toggle checked={smb} onChange={setSmb} label={t("Usar para SMB")} />
            </div>
            <small className="tg-muted">{t("Con SMB activado pasa a ser tu contraseña de las carpetas compartidas en Windows y Mac (reemplaza la anterior).")}</small>
            <button type="submit" hidden />
          </form>
        </>
      )}
    </Sheet>
  );
}
