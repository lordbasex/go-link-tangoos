import "./apps.css";
import { useEffect, useRef, useState } from "react";
import { ExternalLink, RotateCw, ShieldAlert } from "lucide-react";
import { Button, EmptyState, IconButton } from "@tango/ui";
import { WindowToolbar } from "@tango/shell";
import { t } from "@tango/client";
import type { AppProps } from "@tango/shell";

const LOAD_TIMEOUT = 8000;

/**
 * Ventana de una app instalada: iframe hacia el proxy HTTPS que tangod abre por app
 * (protocol/README.md §9). Si no carga (típicamente porque el navegador todavía no
 * aceptó el certificado de ese puerto), explica qué hacer.
 */
export default function AppView({ win }: AppProps) {
  const url = typeof win.args.url === "string" ? win.args.url : null;
  const title = typeof win.args.title === "string" ? win.args.title : "App";
  const icon = typeof win.args.icon === "string" ? win.args.icon : null;
  const [nonce, setNonce] = useState(0);
  const [state, setState] = useState<"loading" | "ready" | "failed">("loading");
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => {
    win.setTitle(title);
  }, [win, title]);

  useEffect(() => {
    if (!url) return;
    setState("loading");
    timer.current = setTimeout(() => setState((s) => (s === "loading" ? "failed" : s)), LOAD_TIMEOUT);
    return () => clearTimeout(timer.current);
  }, [url, nonce]);

  const openTab = () => url && !url.startsWith("data:") && window.open(url, "_blank", "noopener");
  const display = url && !url.startsWith("data:") ? url : "demo";

  return (
    <div className="tg-app">
      <div className="tg-app-main">
        <WindowToolbar win={win}>
          {icon && <img src={icon} alt="" width={20} height={20} className="tapp-view-icon" />}
          <span className="tg-tb-title">{title}</span>
          <span className="tapp-view-url" title={display}>
            {display}
          </span>
          <span className="tg-tb-spacer" />
          <IconButton icon={RotateCw} label={t("Recargar")} disabled={!url} onClick={() => setNonce((n) => n + 1)} />
          <IconButton icon={ExternalLink} label={t("Abrir en pestaña nueva")} disabled={!url || url.startsWith("data:")} onClick={openTab} />
        </WindowToolbar>
        <div className="tg-app-content tapp-view">
          {!url ? (
            <EmptyState icon={ShieldAlert} title={t("{name} no tiene interfaz web", { name: title })}>
              {t("Esta app funciona en segundo plano. Administrala desde el Centro de apps.")}
            </EmptyState>
          ) : (
            <>
              <iframe
                key={nonce}
                src={url}
                title={title}
                className="tapp-frame"
                data-state={state}
                sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads allow-modals"
                allow="fullscreen; clipboard-read; clipboard-write"
                onLoad={() => {
                  clearTimeout(timer.current);
                  setState("ready");
                }}
              />
              {state === "loading" && <div className="tapp-view-loading" aria-label={t("Cargando")} />}
              {state === "failed" && (
                <div className="tapp-view-fail">
                  <EmptyState
                    icon={ShieldAlert}
                    title={t("{name} no respondió", { name: title })}
                    action={
                      <div className="tapp-view-fail-actions">
                        <Button variant="primary" onClick={openTab}>
                          {t("Abrir en pestaña nueva")}
                        </Button>
                        <Button onClick={() => setNonce((n) => n + 1)}>{t("Reintentar")}</Button>
                      </div>
                    }
                  >
                    {t("Abrí la app en una pestaña nueva una vez para aceptar el certificado del NAS y después tocá Reintentar. Si sigue sin responder, revisá su registro en el Centro de apps.")}
                  </EmptyState>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
