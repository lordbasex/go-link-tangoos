// /s/<deviceId>.<linkId>#<secreto>: enlace público para compartir archivos (protocol/README.md §15,
// internal/connect). Quien lo abre no necesita cuenta: llega al NAS por signalhub (reach), abre el
// enlace con guest.open {link, secret} y baja el archivo o la carpeta directo del NAS por WebRTC.
// El secreto va en el fragmento: nunca llega al servidor del portal.
import { useEffect, useRef, useState, type FormEvent } from "react";
import { fmtBytes } from "@tango/client";
import { openRemote, type RemoteSession, type RemoteStage } from "../connect/remote";
import { TangoMark } from "../components/site";
import { Link } from "../router";

interface GuestInfo {
  nas: string;
  name: string;
  /** "dir" o el tipo del archivo (image, video, pdf…; FileKind del NAS). */
  kind: string;
  size: number;
  needsPassword: boolean;
  unlocked: boolean;
  expiresAt?: string | null;
  downloadsLeft?: number | null;
}
interface GuestEntry {
  name: string;
  path: string;
  kind: string;
  size: number;
}

type Call = <T>(method: string, params?: unknown) => Promise<T>;

function caller(s: RemoteSession): Call {
  return <T,>(m: string, p: unknown = null) => (s.transport.call as unknown as (m: string, p: unknown) => Promise<T>)(m, p);
}

function errorText(e: unknown): string {
  const code = (e as { code?: string })?.code ?? "";
  const map: Record<string, string> = {
    SHARE_LINK_EXPIRED: "Este enlace venció o ya alcanzó su límite de descargas.",
    SHARE_LINK_NOT_FOUND: "Este enlace no existe o quien lo compartió lo revocó.",
    SHARE_LINK_LOCKED: "Este enlace pide contraseña.",
    INVALID_CREDENTIALS: "La contraseña no es correcta.",
    RATE_LIMITED: "Demasiados intentos. Esperá un rato y volvé a abrir el enlace.",
  };
  return map[code] ?? (e as Error)?.message ?? "No se pudo completar.";
}

async function saveBlob(name: string, data: Uint8Array) {
  const url = URL.createObjectURL(new Blob([data as BlobPart]));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 30_000);
}

export function Share({ invite }: { invite: string }) {
  const [stage, setStage] = useState<RemoteStage | null>("signal");
  const [fatal, setFatal] = useState<string | null>(null);
  const [info, setInfo] = useState<GuestInfo | null>(null);
  const [entries, setEntries] = useState<GuestEntry[] | null>(null);
  const [password, setPassword] = useState("");
  const [locked, setLocked] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [progress, setProgress] = useState<{ name: string; got: number; size: number } | null>(null);
  const s = useRef<RemoteSession | null>(null);

  const refresh = async (sess: RemoteSession) => {
    const call = caller(sess);
    const i = await call<GuestInfo>("guest.info");
    setInfo(i);
    const locked = i.needsPassword && !i.unlocked;
    setLocked(locked);
    if (!locked && i.kind === "dir") setEntries(await call<GuestEntry[]>("guest.list", { path: "" }));
  };

  useEffect(() => {
    const [deviceId, link] = invite.split(".");
    const secret = location.hash.replace(/^#/, "");
    if (!/^[A-Za-z0-9_-]{16,64}$/.test(secret)) {
      setFatal("El enlace está incompleto: falta la parte que va después de “#”. Copialo entero.");
      return;
    }
    const sess = openRemote({ target: { mode: "guest", deviceId, link, secret }, label: "Invitado", onStage: setStage, onFatal: setFatal });
    s.current = sess;
    const off = sess.transport.onState((st) => {
      if (st === "connected") void refresh(sess).catch((e) => setMsg(errorText(e)));
    });
    return () => {
      off();
      sess.close();
    };
  }, [invite]);

  const unlock = async (e: FormEvent) => {
    e.preventDefault();
    if (!s.current) return;
    setMsg(null);
    try {
      await caller(s.current)("guest.unlock", { password });
      setPassword("");
      await refresh(s.current);
      setLocked(false);
    } catch (err) {
      setMsg(errorText(err));
    }
  };

  const download = async (path: string, name: string) => {
    if (!s.current) return;
    setMsg(null);
    try {
      const r = await caller(s.current)<{ xfer: number; size: number; length?: number }>("guest.download.begin", { path });
      const len = r.length ?? r.size;
      setProgress({ name, got: 0, size: len });
      const data = await s.current.collector.expect(r.xfer, len, (got, size) => setProgress({ name, got, size }));
      await saveBlob(name, data);
      setProgress(null);
      setInfo((i) => (i && typeof i.downloadsLeft === "number" ? { ...i, downloadsLeft: Math.max(0, i.downloadsLeft - 1) } : i));
    } catch (err) {
      setProgress(null);
      setMsg(errorText(err));
    }
  };

  return (
    <div className="ios">
      <div className="ios-col">
        <header className="ios-head">
          <TangoMark size={44} />
          <div>
            <h1 className="ios-title">{info ? info.name : "Archivo compartido"}</h1>
            <p className="ios-subtitle">{info ? `Compartido desde ${info.nas} con TangoOS` : "TangoOS Connect"}</p>
          </div>
        </header>

        {fatal ? (
          <div className="ios-card ios-connect">
            <p className="ios-card-title">Este enlace no está disponible</p>
            <p className="ios-note">{fatal}</p>
            <p className="ios-note">Puede que haya vencido, que lo hayan revocado o que el NAS esté apagado. Pedile a quien te lo mandó uno nuevo.</p>
          </div>
        ) : !info ? (
          <div className="ios-card ios-connect">
            <p className="ios-card-title">Conectando con el NAS…</p>
            <p className="ios-note">{stage === "retrying" ? "Reintentando…" : "Buscando el archivo compartido. Va directo del NAS a tu navegador, cifrado."}</p>
          </div>
        ) : locked ? (
          <form className="ios-card ios-connect" onSubmit={unlock}>
            <label className="ios-card-title" htmlFor="tc-share-pw">
              Este enlace tiene contraseña
            </label>
            <input id="tc-share-pw" className="ios-field" type="password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Contraseña" />
            {msg && (
              <p className="ios-error" role="alert">
                {msg}
              </p>
            )}
            <button type="submit" className="ios-primary" disabled={!password}>
              Abrir
            </button>
          </form>
        ) : (
          <section className="ios-sec">
            {info.kind !== "dir" ? (
              <div className="ios-card ios-connect">
                <p className="ios-card-title">{info.name}</p>
                <p className="ios-note">
                  {fmtBytes(info.size)}
                  {info.expiresAt ? ` · vence el ${new Date(info.expiresAt).toLocaleDateString("es-AR")}` : ""}
                  {typeof info.downloadsLeft === "number" ? ` · quedan ${info.downloadsLeft} descargas` : ""}
                </p>
                <button type="button" className="ios-primary" disabled={!!progress} onClick={() => void download("", info.name)}>
                  {progress ? "Descargando…" : "Descargar"}
                </button>
              </div>
            ) : (
              <ul className="ios-list">
                {(entries ?? []).map((e) => (
                  <li key={e.path} className="ios-row">
                    <span className="ios-row-link">
                      <span className="ios-row-text">
                        <span className="ios-row-title">{e.name}</span>
                        <span className="ios-row-detail">{e.kind === "dir" ? "Carpeta" : fmtBytes(e.size)}</span>
                      </span>
                      {e.kind !== "dir" && (
                        <button type="button" className="ios-chip-button" disabled={!!progress} onClick={() => void download(e.path, e.name)}>
                          Descargar
                        </button>
                      )}
                    </span>
                  </li>
                ))}
                {entries?.length === 0 && <li className="ios-empty">La carpeta está vacía.</li>}
              </ul>
            )}
            {progress && (
              <div className="ios-card ios-connect">
                <p className="ios-note">
                  {progress.name}: {fmtBytes(progress.got)} de {fmtBytes(progress.size)}
                </p>
                <progress className="tc-progress" max={progress.size || 1} value={progress.got} />
              </div>
            )}
            {msg && (
              <p className="ios-error" role="alert">
                {msg}
              </p>
            )}
          </section>
        )}
        <p className="ios-note">
          Los archivos viajan cifrados directo desde el NAS de quien te los compartió. <Link to="/">¿Qué es TangoOS?</Link>
        </p>
      </div>
    </div>
  );
}
