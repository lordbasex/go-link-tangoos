// /demo: el escritorio completo de TangoOS con datos simulados (MockTransport, escenario "lab": 12
// bahías, RAID 1 y RAID 5, carpetas, apps). Corre entero en el navegador: no se conecta a nada.
// Se entra y se sale con recarga completa (no con navegación SPA): la red queda bloqueada y los
// estilos del escritorio no se mezclan con los del sitio.
import { useEffect, useRef, useState } from "react";

/** Corta toda salida de red de la página mientras dura la demo. */
export function blockNetwork() {
  const deny = () => {
    throw new Error("Demo: sin red");
  };
  const w = window as unknown as Record<string, unknown>;
  w.WebSocket = function DemoWebSocket() {
    deny();
  };
  w.RTCPeerConnection = function DemoPeerConnection() {
    deny();
  };
  w.EventSource = function DemoEventSource() {
    deny();
  };
  const realFetch = window.fetch.bind(window);
  window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, location.href);
    // Solo archivos propios del sitio (chunks, fuentes, imágenes): nunca otro origen.
    if (url.origin !== location.origin) return Promise.reject(new TypeError("Demo: sin red"));
    return realFetch(input, init);
  };
  navigator.sendBeacon = () => false;
}

export function Demo() {
  const host = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    blockNetwork();
    let cancelled = false;
    Promise.all([import("../desktop/mountDesktop"), import("@tango/client")])
      .then(([{ mountTangoDesktop }, { MockTransport }]) => {
        if (cancelled || !host.current) return;
        const t = new MockTransport("lab");
        mountTangoDesktop(host.current, t, { skipLogin: { name: "federico", full: "Federico Pereira", color: "#2F7CF6" } });
      })
      .catch((e: Error) => setError(e.message));
    return () => {
      cancelled = true;
    };
  }, []);
  return (
    <>
      <div className="tc-demo-pill" role="note">
        <span>Demo: datos simulados en tu navegador</span>
        <a href="/">Conectá tu NAS</a>
      </div>
      {error && <p className="tc-banner">No se pudo abrir la demo: {error}</p>}
      <div className="tc-desktop-host" ref={host} />
    </>
  );
}
