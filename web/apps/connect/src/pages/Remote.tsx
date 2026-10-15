// /nas/<device_id>: conecta por signalhub (reach), el NAS prueba primero, y abre el escritorio.
// Camino normal: el escritorio lo entrega el propio NAS por el canal `bundle` (firmado, verificado).
// Modo desarrollo: si el NAS todavía no entrega su escritorio, se monta el escritorio de este portal
// sobre la misma conexión (aviso visible).
import { useEffect, useRef, useState } from "react";
import { BUNDLE_PUBKEY } from "../config";
import { BundleError, loadNasDesktop, type LoadStage } from "../bundle/loader";
import { FINGERPRINT_PREFIX, browserLabel, openRemote, type LinkResult, type RemoteSession, type RemoteStage } from "../connect/remote";
import { TangoMark } from "../components/site";
import { Link } from "../router";
import { NasStore, type LinkedNas } from "../store/nasStore";
import { releasePortal } from "../portalRoot";

const store = new NasStore();

type Step = { key: string; label: string };
const STEPS: Step[] = [
  { key: "find", label: "Buscando tu NAS" },
  { key: "direct", label: "Conexión directa o por relé" },
  { key: "verify", label: "Verificando la identidad del NAS" },
  { key: "desktop", label: "Descargando el escritorio" },
];

function stepIndex(stage: RemoteStage | null, load: LoadStage | null): number {
  if (load) return 3;
  switch (stage) {
    case "paired":
      return 1;
    case "verifying":
    case "connected":
      return 2;
    default:
      return 0;
  }
}

export function Remote({ deviceId }: { deviceId: string }) {
  const [nas, setNas] = useState<LinkedNas | null | undefined>(undefined);
  const [stage, setStage] = useState<RemoteStage | null>(null);
  const [load, setLoad] = useState<LoadStage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const fpChanged = !!error && error.startsWith(FINGERPRINT_PREFIX);
  const [devMode, setDevMode] = useState(false);
  const [devReason, setDevReason] = useState("");
  const host = useRef<HTMLDivElement>(null);
  const session = useRef<RemoteSession | null>(null);

  useEffect(() => {
    void store.get(deviceId).then((n) => setNas(n ?? null));
  }, [deviceId]);

  useEffect(() => {
    if (!nas) return;
    const s = openRemote({
      target: { mode: "reach", deviceId: nas.deviceId },
      nas,
      label: browserLabel(),
      onStage: setStage,
      onFatal: setError,
    });
    session.current = s;
    let cancelled = false;
    let handedOff = false;
    s.linked.then(async (r: LinkResult | null) => {
      if (cancelled || !r) return;
      await waitConnected(s);
      if (cancelled) return;
      // Camino normal: el escritorio que entrega el NAS (firmado). El modo desarrollo es solo el respaldo
      // cuando el NAS no lo entrega o no se puede montar; un problema de seguridad (firma, hash, manifiesto)
      // nunca cae al respaldo.
      let reason = "";
      if (!BUNDLE_PUBKEY) reason = "este portal no tiene configurada la clave pública de releases";
      else if (!s.transport.isReal("bundle.manifest" as never)) reason = "tu NAS todavía no entrega su escritorio por TangoConnect";
      else {
        try {
          const d = await loadNasDesktop({ deviceId: nas.deviceId, transport: s.transport, publicKey: BUNDLE_PUBKEY, onStage: setLoad });
          if (cancelled) return;
          // El escritorio del NAS toma #root y se queda con la conexión: el cleanup de este componente
          // (que corre al desmontar el portal) no tiene que cerrarla.
          handedOff = true;
          releasePortal();
          try {
            await d.mount(document.getElementById("root")!);
            return;
          } catch (e) {
            // El portal ya se desmontó: mostramos el respaldo sobre la misma conexión.
            reason = `no se pudo montar el escritorio del NAS (${(e as Error).message})`;
            await mountFallback(s, reason);
            return;
          }
        } catch (e) {
          if (e instanceof BundleError && (e.code === "BAD_SIGNATURE" || e.code === "BAD_HASH" || e.code === "BAD_MANIFEST")) {
            setError(e.message);
            return;
          }
          reason = e instanceof BundleError ? e.message : (e as Error).message;
        }
      }
      if (cancelled) return;
      setDevReason(reason);
      setDevMode(true);
      const { mountTangoDesktop } = await import("../desktop/mountDesktop");
      if (!cancelled && host.current) mountTangoDesktop(host.current, s.transport);
    }, () => {});
    return () => {
      cancelled = true;
      if (!handedOff) s.close();
    };
  }, [nas]);

  if (nas === null)
    return (
      <Shell>
        <p className="ios-card-title">Este navegador no tiene vinculado ese NAS</p>
        <p className="ios-note">Vinculalo con el código que muestra tu NAS en Ajustes → TangoConnect.</p>
        <Link className="ios-primary" to="/nas/vincular" style={{ textAlign: "center", textDecoration: "none" }}>
          Vincular un NAS
        </Link>
      </Shell>
    );

  if (devMode)
    return (
      <>
        <div className="tc-banner" role="note">
          Modo desarrollo: el escritorio es el de este portal ({devReason || "tu NAS todavía no entrega el suyo"}).
        </div>
        <div className="tc-desktop-host" ref={host} />
      </>
    );

  const idx = stepIndex(stage, load);
  return (
    <Shell title={nas?.name ?? "Tu NAS"}>
      <ol className="tc-steps">
        {STEPS.map((s, i) => (
          <li key={s.key} data-state={error && i === idx ? "error" : i < idx ? "done" : i === idx ? "now" : "todo"}>
            <span className="tc-step-dot" aria-hidden="true" />
            <span>
              {s.label}
              {i === 3 && load?.kind === "files" && ` · ${load.done}/${load.total}`}
            </span>
          </li>
        ))}
      </ol>
      {load?.kind === "files" && <progress className="tc-progress" max={load.totalBytes || 1} value={load.bytes} />}
      {stage === "retrying" && !error && <p className="ios-note">Tu NAS no responde todavía; seguimos intentando…</p>}
      {error && (
        <>
          <p className="ios-error" role="alert">
            {error}
          </p>
          <div style={{ display: "flex", gap: 10 }}>
            {fpChanged ? (
              <button
                type="button"
                className="ios-primary"
                onClick={() => void store.remove(deviceId).then(() => (location.href = "/nas/vincular"))}
              >
                Olvidar y volver a vincular
              </button>
            ) : (
              <button type="button" className="ios-secondary" onClick={() => location.reload()}>
                Reintentar
              </button>
            )}
            <Link className="ios-action" to="/nas">
              Volver a Mis NAS
            </Link>
          </div>
        </>
      )}
    </Shell>
  );
}

/** Respaldo cuando el escritorio del NAS no se pudo montar después de desmontar el portal. */
async function mountFallback(s: RemoteSession, reason: string) {
  const el = document.getElementById("root")!;
  const banner = document.createElement("div");
  banner.className = "tc-banner";
  banner.setAttribute("role", "note");
  banner.textContent = `Modo desarrollo: el escritorio es el de este portal (${reason}).`;
  const host = document.createElement("div");
  host.className = "tc-desktop-host";
  el.replaceChildren(banner, host);
  const { mountTangoDesktop } = await import("../desktop/mountDesktop");
  mountTangoDesktop(host, s.transport);
}

function waitConnected(s: RemoteSession): Promise<void> {
  if (s.transport.state() === "connected") return Promise.resolve();
  return new Promise((resolve) => {
    const off = s.transport.onState((st) => {
      if (st === "connected") {
        off();
        resolve();
      }
    });
  });
}

function Shell({ title = "Conectando", children }: { title?: string; children: React.ReactNode }) {
  return (
    <div className="ios">
      <div className="ios-col">
        <header className="ios-head">
          <TangoMark size={44} />
          <div>
            <h1 className="ios-title">{title}</h1>
            <p className="ios-subtitle">TangoOS Connect</p>
          </div>
        </header>
        <div className="ios-card ios-connect">{children}</div>
      </div>
    </div>
  );
}
