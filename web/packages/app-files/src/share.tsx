import { useState } from "react";
import { Copy, Link2, Trash2 } from "lucide-react";
import { fmtAgo, fmtBytes, t, useLive, useTransport } from "@tango/client";
import type { FileEntry, ShareLinkInfo } from "@tango/client";
import { openApp, toast } from "@tango/shell";
import { Badge, Button, EmptyState, Sheet, TextInput, Toggle } from "@tango/ui";

// Enlaces públicos (TangoConnect, protocol/README.md §15): quien tenga el enlace baja el
// archivo (o navega la carpeta) directo desde el NAS, sin cuenta, por WebRTC.

const EXPIRY: { label: string; days: number | null }[] = [
  { label: "1 día", days: 1 },
  { label: "7 días", days: 7 },
  { label: "30 días", days: 30 },
  { label: "Nunca", days: null },
];

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    toast({ level: "ok", title: t("Enlace copiado"), app: "tango.files" });
  } catch {
    toast({ level: "warn", title: t("No se pudo copiar"), body: t("Seleccionalo y copialo a mano."), app: "tango.files" });
  }
}

export function ShareLinkSheet({ entry, onClose }: { entry: FileEntry; onClose: () => void }) {
  const tp = useTransport();
  const status = useLive("connect.status", undefined).data;
  const [days, setDays] = useState<number | null>(7);
  const [usePw, setUsePw] = useState(false);
  const [pw, setPw] = useState("");
  const [limit, setLimit] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [made, setMade] = useState<ShareLinkInfo | null>(null);

  const create = async () => {
    setBusy(true);
    setErr(null);
    try {
      const max = limit.trim() ? Number(limit) : undefined;
      if (max !== undefined && (!Number.isInteger(max) || max < 1)) throw new Error(t("La cantidad de descargas tiene que ser un número entero mayor que cero."));
      if (usePw && pw.length < 6) throw new Error(t("Usá una contraseña de al menos 6 caracteres."));
      const l = await tp.call("shares.links.create", {
        path: entry.path,
        expiresAt: days ? new Date(Date.now() + days * 864e5).toISOString() : undefined,
        password: usePw ? pw : undefined,
        maxDownloads: max,
      });
      setMade(l);
      void copyText(l.url);
    } catch (e) {
      setErr((e as Error).message);
    }
    setBusy(false);
  };

  const off = status && !status.enabled;
  return (
    <Sheet
      open
      onClose={onClose}
      width={500}
      footer={
        made ? (
          <Button variant="primary" onClick={onClose}>
            {t("Listo")}
          </Button>
        ) : (
          <>
            {err && (
              <span className="tf-share-err" role="alert">
                {err}
              </span>
            )}
            <Button onClick={onClose}>{t("Cancelar")}</Button>
            <Button variant="primary" disabled={busy} onClick={() => void create()}>
              <Link2 size={14} /> {t("Crear enlace")}
            </Button>
          </>
        )
      }
    >
      <h2 className="tg-h2">{t("Compartir «{name}»", { name: entry.name })}</h2>
      {made ? (
        <>
          <p className="tg-lead" style={{ marginTop: 4 }}>
            {made.kind === "dir"
              ? made.hasPassword
                ? t("Ya está copiado. Quien lo abra puede ver y bajar lo que hay en la carpeta después de escribir la contraseña.")
                : t("Ya está copiado. Quien lo abra puede ver y bajar lo que hay en la carpeta.")
              : made.hasPassword
                ? t("Ya está copiado. Quien lo abra puede bajar el archivo después de escribir la contraseña.")
                : t("Ya está copiado. Quien lo abra puede bajar el archivo.")}
          </p>
          <div className="tf-share-url">
            <code>{made.url}</code>
            <Button onClick={() => void copyText(made.url)}>
              <Copy size={14} /> {t("Copiar")}
            </Button>
          </div>
          {off && <p className="tf-share-warn">{t("TangoConnect está apagado: el enlace va a funcionar recién cuando lo actives en Ajustes.")}</p>}
        </>
      ) : (
        <>
          <p className="tg-lead" style={{ marginTop: 4 }}>
            {entry.kind === "dir"
              ? t("Cualquiera con el enlace ve y baja el contenido de la carpeta, sin cuenta. La descarga va directo desde tu NAS.")
              : t("Cualquiera con el enlace baja el archivo, sin cuenta. La descarga va directo desde tu NAS.")}
          </p>
          {off && (
            <p className="tf-share-warn">
              {t("TangoConnect está apagado. Podés crear el enlace igual, pero no va a abrir hasta que lo actives.")}{" "}
              <button className="tf-linkbtn" onClick={() => openApp("tango.settings", { section: "connect" })}>
                {t("Abrir Ajustes")}
              </button>
            </p>
          )}
          <div className="tf-share-form">
            <div className="tf-share-row">
              <span>{t("Vence")}</span>
              <div className="tf-share-seg" role="group" aria-label={t("Vencimiento")}>
                {EXPIRY.map((o) => (
                  <button key={o.label} type="button" aria-pressed={days === o.days} onClick={() => setDays(o.days)}>
                    {t(o.label)}
                  </button>
                ))}
              </div>
            </div>
            <div className="tf-share-row">
              <span>{t("Pedir contraseña")}</span>
              <Toggle checked={usePw} onChange={setUsePw} label={t("Pedir contraseña")} />
            </div>
            {usePw && <TextInput type="password" value={pw} onChange={(e) => setPw(e.target.value)} placeholder={t("Contraseña del enlace")} autoFocus />}
            <div className="tf-share-row">
              <span>{t("Límite de descargas")}</span>
              <TextInput value={limit} onChange={(e) => setLimit(e.target.value.replace(/\D/g, ""))} placeholder={t("Sin límite")} inputMode="numeric" style={{ width: 110 }} />
            </div>
          </div>
        </>
      )}
    </Sheet>
  );
}

export function SharedLinksSheet({ onClose }: { onClose: () => void }) {
  const tp = useTransport();
  const q = useLive("shares.links.list", {}, ["connect.changed"]);
  const list = q.data ?? [];
  const revoke = async (l: ShareLinkInfo) => {
    try {
      await tp.call("shares.links.revoke", { id: l.id });
      q.reload();
      toast({ level: "ok", title: t("Enlace revocado"), body: t("«{name}» ya no se puede abrir.", { name: l.name }), app: "tango.files" });
    } catch (e) {
      toast({ level: "crit", title: t("No se pudo revocar"), body: (e as Error).message, app: "tango.files" });
    }
  };
  return (
    <Sheet
      open
      onClose={onClose}
      width={620}
      footer={
        <Button variant="primary" onClick={onClose}>
          {t("Listo")}
        </Button>
      }
    >
      <h2 className="tg-h2">{t("Enlaces compartidos")}</h2>
      {list.length === 0 ? (
        <EmptyState icon={Link2} title={t("No compartiste nada todavía")}>
          {t("Clic derecho sobre un archivo o carpeta → Compartir enlace…")}
        </EmptyState>
      ) : (
        <div className="tf-links">
          {list.map((l) => (
            <div key={l.id} className="tf-link">
              <div className="tf-link-main">
                <strong>
                  {l.name} {l.expired && <Badge tone="warn">{t("Vencido")}</Badge>} {l.hasPassword && <Badge>{t("Con contraseña")}</Badge>}
                </strong>
                <small>
                  {l.path} · {l.kind === "dir" ? t("carpeta") : fmtBytes(l.size)} · {l.expiresAt ? t("vence {when}", { when: fmtAgo(l.expiresAt) }) : t("sin vencimiento")} ·{" "}
                  {l.maxDownloads ? t("{n}/{max} descargas", { n: l.downloads, max: l.maxDownloads }) : t("{n} descargas", { n: l.downloads })}
                </small>
              </div>
              <Button variant="plain" onClick={() => void copyText(l.url)} title={t("Copiar enlace")} aria-label={t("Copiar enlace de {name}", { name: l.name })}>
                <Copy size={14} />
              </Button>
              <Button variant="plain" onClick={() => void revoke(l)} title={t("Revocar")} aria-label={t("Revocar enlace de {name}", { name: l.name })}>
                <Trash2 size={14} />
              </Button>
            </div>
          ))}
        </div>
      )}
    </Sheet>
  );
}
