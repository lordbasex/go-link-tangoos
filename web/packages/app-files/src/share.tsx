import { useState } from "react";
import { Copy, Link2, Trash2 } from "lucide-react";
import { fmtAgo, fmtBytes, useLive, useTransport } from "@tango/client";
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
    toast({ level: "ok", title: "Enlace copiado", app: "tango.files" });
  } catch {
    toast({ level: "warn", title: "No se pudo copiar", body: "Seleccionalo y copialo a mano.", app: "tango.files" });
  }
}

export function ShareLinkSheet({ entry, onClose }: { entry: FileEntry; onClose: () => void }) {
  const tr = useTransport();
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
      if (max !== undefined && (!Number.isInteger(max) || max < 1)) throw new Error("La cantidad de descargas tiene que ser un número entero mayor que cero.");
      if (usePw && pw.length < 6) throw new Error("Usá una contraseña de al menos 6 caracteres.");
      const l = await tr.call("shares.links.create", {
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
            Listo
          </Button>
        ) : (
          <>
            {err && (
              <span className="tf-share-err" role="alert">
                {err}
              </span>
            )}
            <Button onClick={onClose}>Cancelar</Button>
            <Button variant="primary" disabled={busy} onClick={() => void create()}>
              <Link2 size={14} /> Crear enlace
            </Button>
          </>
        )
      }
    >
      <h2 className="tg-h2">Compartir «{entry.name}»</h2>
      {made ? (
        <>
          <p className="tg-lead" style={{ marginTop: 4 }}>
            Ya está copiado. Quien lo abra {made.kind === "dir" ? "puede ver y bajar lo que hay en la carpeta" : "puede bajar el archivo"}
            {made.hasPassword ? " después de escribir la contraseña" : ""}.
          </p>
          <div className="tf-share-url">
            <code>{made.url}</code>
            <Button onClick={() => void copyText(made.url)}>
              <Copy size={14} /> Copiar
            </Button>
          </div>
          {off && <p className="tf-share-warn">TangoConnect está apagado: el enlace va a funcionar recién cuando lo actives en Ajustes.</p>}
        </>
      ) : (
        <>
          <p className="tg-lead" style={{ marginTop: 4 }}>
            Cualquiera con el enlace {entry.kind === "dir" ? "ve y baja el contenido de la carpeta" : "baja el archivo"}, sin cuenta. La descarga va directo desde tu NAS.
          </p>
          {off && (
            <p className="tf-share-warn">
              TangoConnect está apagado. Podés crear el enlace igual, pero no va a abrir hasta que lo actives.{" "}
              <button className="tf-linkbtn" onClick={() => openApp("tango.settings", { section: "connect" })}>
                Abrir Ajustes
              </button>
            </p>
          )}
          <div className="tf-share-form">
            <div className="tf-share-row">
              <span>Vence</span>
              <div className="tf-share-seg" role="group" aria-label="Vencimiento">
                {EXPIRY.map((o) => (
                  <button key={o.label} type="button" aria-pressed={days === o.days} onClick={() => setDays(o.days)}>
                    {o.label}
                  </button>
                ))}
              </div>
            </div>
            <div className="tf-share-row">
              <span>Pedir contraseña</span>
              <Toggle checked={usePw} onChange={setUsePw} label="Pedir contraseña" />
            </div>
            {usePw && <TextInput type="password" value={pw} onChange={(e) => setPw(e.target.value)} placeholder="Contraseña del enlace" autoFocus />}
            <div className="tf-share-row">
              <span>Límite de descargas</span>
              <TextInput value={limit} onChange={(e) => setLimit(e.target.value.replace(/\D/g, ""))} placeholder="Sin límite" inputMode="numeric" style={{ width: 110 }} />
            </div>
          </div>
        </>
      )}
    </Sheet>
  );
}

export function SharedLinksSheet({ onClose }: { onClose: () => void }) {
  const tr = useTransport();
  const q = useLive("shares.links.list", {}, ["connect.changed"]);
  const list = q.data ?? [];
  const revoke = async (l: ShareLinkInfo) => {
    try {
      await tr.call("shares.links.revoke", { id: l.id });
      q.reload();
      toast({ level: "ok", title: "Enlace revocado", body: `«${l.name}» ya no se puede abrir.`, app: "tango.files" });
    } catch (e) {
      toast({ level: "crit", title: "No se pudo revocar", body: (e as Error).message, app: "tango.files" });
    }
  };
  return (
    <Sheet
      open
      onClose={onClose}
      width={620}
      footer={
        <Button variant="primary" onClick={onClose}>
          Listo
        </Button>
      }
    >
      <h2 className="tg-h2">Enlaces compartidos</h2>
      {list.length === 0 ? (
        <EmptyState icon={Link2} title="No compartiste nada todavía">
          Clic derecho sobre un archivo o carpeta → Compartir enlace…
        </EmptyState>
      ) : (
        <div className="tf-links">
          {list.map((l) => (
            <div key={l.id} className="tf-link">
              <div className="tf-link-main">
                <strong>
                  {l.name} {l.expired && <Badge tone="warn">Vencido</Badge>} {l.hasPassword && <Badge>Con contraseña</Badge>}
                </strong>
                <small>
                  {l.path} · {l.kind === "dir" ? "carpeta" : fmtBytes(l.size)} · {l.expiresAt ? `vence ${fmtAgo(l.expiresAt)}` : "sin vencimiento"} ·{" "}
                  {l.maxDownloads ? `${l.downloads}/${l.maxDownloads} descargas` : `${l.downloads} descargas`}
                </small>
              </div>
              <Button variant="plain" onClick={() => void copyText(l.url)} title="Copiar enlace" aria-label={`Copiar enlace de ${l.name}`}>
                <Copy size={14} />
              </Button>
              <Button variant="plain" onClick={() => void revoke(l)} title="Revocar" aria-label={`Revocar enlace de ${l.name}`}>
                <Trash2 size={14} />
              </Button>
            </div>
          ))}
        </div>
      )}
    </Sheet>
  );
}
