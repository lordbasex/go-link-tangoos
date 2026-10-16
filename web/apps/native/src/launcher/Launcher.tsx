import { useEffect, useState } from "react";
import { Globe, HardDrive, Plus, ShieldCheck, Trash2, Wifi } from "lucide-react";
import { LANGS, getLang, setLang, t } from "@tango/client";
import { Button, TextInput } from "@tango/ui";
import { TangoLogo } from "@tango/theme-milonga";

interface SavedNas {
  id: string;
  name: string;
  host: string;
  port: number;
  fingerprint: string;
  lastOpened?: string;
}
interface Found {
  name: string;
  host: string;
  port: number;
}
interface Probe {
  host: string;
  port: number;
  fingerprint: string;
  subject: string;
  known: string | null;
}
interface Bridge {
  state(): Promise<{ saved: SavedNas[]; found: Found[]; version: string }>;
  probe(address: string): Promise<Probe>;
  add(a: { host: string; port: number; fingerprint: string; name: string }): Promise<string>;
  remove(id: string): Promise<void>;
  open(id: string): Promise<void>;
  portal(): Promise<void>;
  onState(f: (s: { saved: SavedNas[]; found: Found[] }) => void): () => void;
}
const bridge = (window as unknown as { tangoLauncher: Bridge }).tangoLauncher;

const errText = (e: unknown) => String((e as Error)?.message ?? e).replace(/^Error invoking remote method '[^']+': (Error: )?/, "");

export function Launcher() {
  const [saved, setSaved] = useState<SavedNas[]>([]);
  const [found, setFound] = useState<Found[]>([]);
  const [version, setVersion] = useState("");
  const [address, setAddress] = useState("");
  const [adding, setAdding] = useState(false);
  const [probe, setProbe] = useState<Probe & { name: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void bridge.state().then((s) => {
      setSaved(s.saved);
      setFound(s.found);
      setVersion(s.version);
    });
    return bridge.onState((s) => {
      setSaved(s.saved);
      setFound(s.found);
    });
  }, []);

  const savedHost = (f: Found) => saved.find((s) => s.host === f.host && s.port === f.port);
  const fresh = found.filter((f) => !savedHost(f));

  const check = async (addr: string, name = "") => {
    setBusy(true);
    setError(null);
    try {
      const p = await bridge.probe(addr);
      const known = saved.find((s) => s.host === p.host && s.port === p.port);
      if (known && known.fingerprint === p.fingerprint) return void (await bridge.open(known.id));
      setProbe({ ...p, name: name || known?.name || p.subject || p.host });
    } catch (e) {
      setError(t("No se pudo conectar a {addr}: {err}", { addr, err: errText(e) }));
    } finally {
      setBusy(false);
    }
  };
  const confirm = async () => {
    if (!probe) return;
    setBusy(true);
    try {
      const id = await bridge.add({ host: probe.host, port: probe.port, fingerprint: probe.fingerprint, name: probe.name });
      setProbe(null);
      setAdding(false);
      setAddress("");
      await bridge.open(id);
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="nl">
      <header className="nl-head">
        <div className="nl-logo">
          <TangoLogo size={40} />
        </div>
        <h1>{t("Elegí tu NAS")}</h1>
        <p>{t("Abrí el escritorio de un NAS de tu red, o entrá desde afuera con TangoConnect.")}</p>
      </header>

      {probe ? (
        <section className="nl-card nl-confirm" aria-live="polite">
          <ShieldCheck size={22} />
          <h2>{probe.known ? t("El certificado de {name} cambió", { name: probe.name }) : t("¿Es tu NAS?")}</h2>
          <p>
            {probe.known
              ? t("Esta app conocía otra huella para este NAS. Si cambiaste su certificado, confirmá la nueva; si no, no sigas: alguien podría estar haciéndose pasar por él.")
              : t("Compará esta huella con la que muestra el NAS en Ajustes → Seguridad → Certificado. Si coinciden, la app la recuerda y no acepta otra.")}
          </p>
          <code className="nl-fp">{probe.fingerprint.replace(/^sha-256 /, "")}</code>
          <label className="nl-field">
            <span>{t("Nombre")}</span>
            <TextInput value={probe.name} onChange={(e) => setProbe({ ...probe, name: e.target.value })} aria-label={t("Nombre")} />
          </label>
          <div className="nl-row">
            <Button onClick={() => setProbe(null)}>{t("Cancelar")}</Button>
            <Button variant="primary" disabled={busy} onClick={() => void confirm()}>
              {t("Es la misma: abrir")}
            </Button>
          </div>
        </section>
      ) : (
        <section className="nl-card">
          <ul className="nl-list">
            {saved.map((n) => {
              const online = found.some((f) => f.host === n.host && f.port === n.port);
              return (
                <li key={n.id}>
                  <button type="button" className="nl-nas" onClick={() => void bridge.open(n.id)} disabled={busy}>
                    <HardDrive size={26} strokeWidth={1.5} />
                    <span>
                      <strong>{n.name}</strong>
                      <small>
                        {n.host}
                        {n.port !== 443 ? `:${n.port}` : ""}
                        {online ? ` · ${t("en esta red")}` : ""}
                      </small>
                    </span>
                  </button>
                  <button type="button" className="nl-remove" aria-label={t("Quitar {name}", { name: n.name })} title={t("Quitar de la lista")} onClick={() => void bridge.remove(n.id)}>
                    <Trash2 size={14} />
                  </button>
                </li>
              );
            })}
            {fresh.map((f) => (
              <li key={`${f.host}:${f.port}`}>
                <button type="button" className="nl-nas nl-new" onClick={() => void check(`${f.host}:${f.port}`, f.name)} disabled={busy}>
                  <Wifi size={26} strokeWidth={1.5} />
                  <span>
                    <strong>{f.name}</strong>
                    <small>
                      {f.host} · {t("encontrado en la red")}
                    </small>
                  </span>
                </button>
              </li>
            ))}
            {!saved.length && !fresh.length && <li className="nl-empty">{t("Buscando NAS TangoOS en la red…")}</li>}
          </ul>

          {adding ? (
            <form
              className="nl-add"
              onSubmit={(e) => {
                e.preventDefault();
                if (address.trim()) void check(address.trim());
              }}
            >
              <TextInput value={address} autoFocus onChange={(e) => setAddress(e.target.value)} placeholder="192.168.1.20 · tango.local" aria-label={t("Dirección del NAS")} />
              <Button type="submit" variant="primary" disabled={busy || !address.trim()}>
                {t("Conectar")}
              </Button>
            </form>
          ) : (
            <div className="nl-row">
              <Button onClick={() => setAdding(true)}>
                <Plus size={14} /> {t("Agregar por dirección…")}
              </Button>
              <Button onClick={() => void bridge.portal()}>
                <Globe size={14} /> {t("Desde fuera de casa (TangoConnect)")}
              </Button>
            </div>
          )}
          {error && (
            <p className="nl-error" role="alert">
              {error}
            </p>
          )}
        </section>
      )}

      <footer className="nl-foot">
        <span>TangoOS {version}</span>
        <span className="nl-lang">
          {LANGS.map((l) => (
            <button key={l.id} type="button" aria-pressed={getLang() === l.id} onClick={() => getLang() !== l.id && setLang(l.id)}>
              {l.name}
            </button>
          ))}
        </span>
      </footer>
    </div>
  );
}
