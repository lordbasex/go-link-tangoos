// Piezas del sitio con la misma estructura que desk.go-link.org (SiteNav, SiteFooter, Head, Bullets…),
// con la marca TangoOS. Las clases (lp-*) vienen de styles/landing.css de go-link.
import { useEffect, useState, type ReactNode } from "react";
import { SIGNAL_URL } from "../config";
import { Link } from "../router";
import { setTheme, useTheme } from "../theme";

export const REPO_URL = "https://github.com/lordbasex/go-link-tangoos";

/** El ícono de TangoOS (el fueye de bandoneón) en el cuadrado redondeado de la marca. */
export function TangoMark({ size = 32 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" style={{ borderRadius: size / 4, flex: "none" }}>
      <rect width="24" height="24" rx="6" fill="var(--color-accent)" />
      <path d="M5 9 8 7.2v9.6L5 15Z M9.2 6.4 12 4.8v14.4l-2.8-1.6Z M14.8 6.4 12 4.8v14.4l2.8-1.6Z M19 9l-3-1.8v9.6l3-1.8Z" fill="var(--color-on-accent)" />
    </svg>
  );
}

export function Brand() {
  return (
    <Link className="lp-brand" to="/" aria-label="TangoOS Connect, inicio">
      <TangoMark />
      TangoOS <b>Connect</b>
    </Link>
  );
}

/**
 * Captura real del escritorio TangoOS. Sirve las versiones livianas de /img/opt (WebP 800w/1600w, ver
 * scripts/optimize-connect-img.sh); si faltan, el PNG original; si tampoco está, un espacio rotulado con
 * el nombre del archivo (así se sabe qué captura falta) en vez de una imagen rota.
 */
export function Shot({ name, alt, className, sizes = "(max-width: 980px) 100vw, 640px", eager }: { name: string; alt: string; className?: string; sizes?: string; eager?: boolean }) {
  const base = name.replace(/\.(png|jpe?g|webp)$/i, "");
  const [stage, setStage] = useState<"opt" | "orig" | "missing">("opt");
  if (stage === "missing")
    return (
      <div className={`tc-shot-missing ${className ?? ""}`} role="img" aria-label={alt}>
        <span>{alt}</span>
        <code>/img/{name}</code>
      </div>
    );
  if (stage === "orig") return <img className={className} src={`/img/${name}`} alt={alt} loading="lazy" decoding="async" onError={() => setStage("missing")} />;
  return (
    <img
      className={className}
      src={`/img/opt/${base}-1600.webp`}
      srcSet={`/img/opt/${base}-800.webp 800w, /img/opt/${base}-1600.webp 1600w`}
      sizes={sizes}
      alt={alt}
      loading={eager ? "eager" : "lazy"}
      fetchPriority={eager ? "high" : undefined}
      decoding="async"
      onError={() => setStage("orig")}
    />
  );
}

export function Head({ eyebrow, title, text }: { eyebrow: string; title: string; text?: string }) {
  return (
    <div className="lp-head">
      <p className="lp-eyebrow">{eyebrow}</p>
      <h2>{title}</h2>
      {text && <p>{text}</p>}
    </div>
  );
}

export function Bullets({ items }: { items: { b: string; t: string }[] }) {
  return (
    <ul className="lp-bullets">
      {items.map((i) => (
        <li key={i.b}>
          <span>
            <b>{i.b}</b> {i.t}
          </span>
        </li>
      ))}
    </ul>
  );
}

export function ThemeToggle() {
  const theme = useTheme();
  return (
    <button type="button" className="lp-foot-link" onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
      {theme === "dark" ? "Tema claro" : "Tema oscuro"}
    </button>
  );
}

export function SiteNav() {
  return (
    <header className="lp-nav">
      <Brand />
      <nav className="lp-nav-links" aria-label="Secciones">
        <a href="/#como">Cómo funciona</a>
        <a href="/#funciones">Funciones</a>
        <a href="/#galeria">Galería</a>
        <a href="/#seguridad">Seguridad</a>
        <a href={REPO_URL} rel="noreferrer">
          Código
        </a>
      </nav>
      <div className="lp-nav-cta">
        <Link className="lp-btn lp-btn-ghost" to="/nas">
          Conectar mi NAS
        </Link>
        {/* La demo entra con recarga completa: allí la red queda bloqueada (pages/Demo.tsx). */}
        <a className="lp-btn lp-btn-primary" href="/demo">
          Probar la demo
        </a>
      </div>
    </header>
  );
}

type Status = "checking" | "ok" | "down";

/** Si el servidor de señalización responde (un hello), una vez. */
function useSignalStatus(): Status {
  const [status, setStatus] = useState<Status>("checking");
  useEffect(() => {
    let done = false;
    let ws: WebSocket | undefined;
    const finish = (s: Status) => {
      if (done) return;
      done = true;
      setStatus(s);
      ws?.close();
    };
    try {
      ws = new WebSocket(SIGNAL_URL);
      ws.onmessage = (e) => {
        try {
          if (JSON.parse(String(e.data)).type === "hello") finish("ok");
        } catch {
          /* no es JSON */
        }
      };
      ws.onerror = () => finish("down");
    } catch {
      finish("down");
    }
    const timer = window.setTimeout(() => finish("down"), 8000);
    return () => {
      done = true;
      window.clearTimeout(timer);
      ws?.close();
    };
  }, []);
  return status;
}

export function SiteFooter() {
  const status = useSignalStatus();
  return (
    <footer className="lp-foot">
      <div className="lp-wrap">
        <div className="lp-foot-top">
          <div className="lp-foot-brand">
            <Brand />
            <p>Tu NAS TangoOS desde cualquier lado, sin abrir puertos. El escritorio completo, cifrado de punta a punta, sobre la red de go-link.</p>
            <Link className="lp-foot-code" to="/nas/vincular">
              000 000 000 <span>Vincular</span>
            </Link>
          </div>
          <nav className="lp-foot-col" aria-label="Producto">
            <h4>Producto</h4>
            <ul>
              <li>
                <a href="/demo">Probar la demo</a>
              </li>
              <li>
                <Link to="/nas">Mis NAS</Link>
              </li>
              <li>
                <Link to="/nas/vincular">Vincular un NAS</Link>
              </li>
              <li>
                <a href="/#funciones">Funciones</a>
              </li>
              <li>
                <a href="/#galeria">Galería</a>
              </li>
              <li>
                <a href={`${REPO_URL}/releases/latest`}>Descargar TangoOS (ISO)</a>
              </li>
            </ul>
          </nav>
          <nav className="lp-foot-col" aria-label="Ayuda">
            <h4>Ayuda</h4>
            <ul>
              <li>
                <a href="/#como">Cómo funciona</a>
              </li>
              <li>
                <a href="/#seguridad">Seguridad</a>
              </li>
              <li>
                <a href="/#preguntas">Preguntas frecuentes</a>
              </li>
              <li>
                <a href={`${REPO_URL}/blob/main/docs/16-tangoconnect.md`} rel="noreferrer">
                  Documentación
                </a>
              </li>
              <li>
                <a href={`${REPO_URL}/issues`} rel="noreferrer">
                  Reportar un problema
                </a>
              </li>
              <li>
                <ThemeToggle />
              </li>
            </ul>
          </nav>
          <nav className="lp-foot-col" aria-label="Proyecto">
            <h4>Proyecto</h4>
            <ul>
              <li>
                <a href={REPO_URL} rel="noreferrer">
                  Código fuente
                </a>
              </li>
              <li>
                <a href={`${REPO_URL}/blob/main/LICENSE`} rel="noreferrer">
                  Licencia MIT
                </a>
              </li>
              <li>
                <a href="https://desk.go-link.org" rel="noreferrer">
                  go-link Desk
                </a>
              </li>
              <li>
                <a href="https://github.com/lordbasex/signalhub" rel="noreferrer">
                  signalhub
                </a>
              </li>
            </ul>
          </nav>
        </div>
        <div className="lp-foot-warn" role="note">
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M12 3l9 16H3z" />
            <path d="M12 10v4M12 17h.01" />
          </svg>
          <span>
            <b>Privacidad.</b> Este portal no ve tus archivos ni tus contraseñas: todo viaja cifrado directo entre tu navegador y tu NAS. No usamos cookies de
            seguimiento; los vínculos se guardan solo en este navegador.
          </span>
        </div>
        <div className="lp-foot-bottom">
          <span>© 2026 TangoOS · MIT · TangoOS Connect, sobre la red de go-link</span>
          <span className={`lp-status lp-status-${status}`} role="status">
            {status === "ok" ? "Señalización en línea" : status === "down" ? "Señalización sin respuesta" : "Comprobando señalización…"}
          </span>
        </div>
      </div>
    </footer>
  );
}

export function Page({ children }: { children: ReactNode }) {
  return <div className="lp">{children}</div>;
}
