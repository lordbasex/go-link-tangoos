// tangoos.go-link.org: misma estructura que la landing de desk.go-link.org (hero con el código,
// formas, pasos, funciones con capturas, seguridad, preguntas, pie), con TangoOS.
import { useRef, useState, type FormEvent } from "react";
import { formatCode, isCompleteCode, normalizeCode } from "../connect/code";
import { Bullets, Head, Page, Shot, SiteFooter, SiteNav } from "../components/site";
import { navigate } from "../router";

const GALLERY: { name: string; alt: string }[] = [
  { name: "desktop-dark.png", alt: "Escritorio de TangoOS con Dock, ventanas y widgets" },
  { name: "storage-rack.png", alt: "Almacenamiento: rack de 12 bahías con volúmenes RAID" },
  { name: "raid-wizard.png", alt: "Asistente para crear un volumen RAID" },
  { name: "files-photos.png", alt: "Archivos: fotos con miniaturas" },
  { name: "apps-center.png", alt: "Centro de apps con Jellyfin, Immich y más" },
  { name: "monitor.png", alt: "Monitor de recursos en vivo" },
  { name: "widgets.png", alt: "Widgets de CPU, memoria, red y almacenamiento" },
  { name: "mission-control.png", alt: "Mission Control: todas las ventanas a la vista" },
  { name: "settings.png", alt: "Ajustes del NAS" },
  { name: "login.png", alt: "Pantalla de inicio de sesión" },
  { name: "share-link.png", alt: "Compartir un archivo con un enlace" },
];

export function Landing() {
  return (
    <Page>
      <div className="lp-wrap">
        <SiteNav />
        <div className="lp-hero">
          <div>
            <p className="lp-eyebrow">TangoOS Connect · sobre la red de go-link</p>
            <h1>
              Tu NAS, <em>en cualquier lado</em>.
            </h1>
            <p className="lp-lead">
              Entrá al escritorio completo de tu NAS TangoOS desde cualquier navegador: almacenamiento, archivos y apps, como si estuvieras en casa. Sin abrir
              puertos, sin VPN, cifrado de punta a punta.
            </p>
            <CodeBox />
            <div className="tc-hero-ctas">
              <a className="lp-btn lp-btn-primary" href="/demo">
                Probar la demo
              </a>
              <a className="lp-btn lp-btn-ghost" href="/nas">
                Mis NAS
              </a>
            </div>
            <ul className="lp-notes">
              <li>Sin cuentas</li>
              <li>Directo entre tu navegador y tu NAS</li>
              <li>Open source (MIT)</li>
            </ul>
          </div>
          <div className="lp-stage" role="img" aria-label="El escritorio de TangoOS en una laptop y el rack de almacenamiento">
            <div className="lp-laptop">
              <div className="lp-screen">
                <Shot name="desktop-dark.png" alt="Escritorio de TangoOS" eager />
              </div>
              <div className="lp-base" />
            </div>
            <div className="lp-phone lp-phone-land">
              <Shot name="storage-rack.png" alt="Rack de almacenamiento de TangoOS" />
            </div>
            <span className="lp-tag lp-tag-a">WebRTC · DTLS</span>
            <span className="lp-tag lp-tag-b">0 puertos abiertos</span>
          </div>
        </div>
      </div>

      <section id="funciones" className="lp-section">
        <div className="lp-wrap">
          <Head
            eyebrow="Todo tu NAS"
            title="No es un visor: es tu NAS entero."
            text="TangoConnect abre el mismo escritorio que ves en tu casa. Lo que podés hacer en la red local, lo podés hacer desde el trabajo, el celular o un hotel."
          />
          <div className="lp-ways">
            <Way chip="Discos y RAID" kind="ok" title="Almacenamiento" text="Volúmenes RAID 1, 5, 6 y 10, reemplazo de discos, instantáneas y alertas." shot="storage-rack.png" />
            <Way chip="Archivos" kind="ok" title="Archivos" text="Subí, bajá, previsualizá y recuperá versiones anteriores de lo que guardás." shot="files-photos.png" />
            <Way chip="Docker" kind="ok" title="Centro de apps" text="Jellyfin, Immich, Home Assistant y más, cada una en su ventana." shot="apps-center.png" />
            <Way chip="En vivo" kind="info" title="Monitor" text="CPU, memoria, red y discos a 4 actualizaciones por segundo." shot="monitor.png" />
          </div>
          <div className="lp-hosts">
            <strong>Funciona en</strong>
            <span className="lp-os lp-os-on">
              <i>hoy</i> Chrome, Edge, Firefox y Safari
            </span>
            <span className="lp-os lp-os-on">
              <i>hoy</i> macOS, Windows, Linux, iPadOS y Android
            </span>
            <span className="lp-os">
              <i>pronto</i> App nativa TangoOS Desk
            </span>
          </div>
        </div>
      </section>

      <section id="como" className="lp-section">
        <div className="lp-wrap">
          <Head eyebrow="Cómo funciona" title="Tres pasos, una sola vez." />
          <ol className="lp-steps">
            <li className="lp-step">
              <h3>Activá TangoConnect</h3>
              <p>En tu NAS: Ajustes → TangoConnect → Activar. El NAS se conecta hacia afuera; no hace falta tocar el router.</p>
            </li>
            <li className="lp-step">
              <h3>Vinculá con el código</h3>
              <p>«Vincular navegador» muestra un código de 9 dígitos que dura 10 minutos. Escribilo acá.</p>
              <span className="lp-code">915 355 636</span>
            </li>
            <li className="lp-step">
              <h3>Entrá</h3>
              <p>Tu NAS queda en «Mis NAS». Desde ahí entrás con tu usuario de TangoOS (y tu segundo factor, si lo activaste).</p>
            </li>
          </ol>
        </div>
      </section>

      <section className="lp-section lp-sunken">
        <div className="lp-wrap">
          <Head eyebrow="Funciones" title="Hecho para el día a día de tu NAS." />
          <div className="lp-feature">
            <div className="lp-media">
              <Shot name="widgets.png" alt="Widgets de CPU, memoria, red y almacenamiento sobre el escritorio" />
            </div>
            <div>
              <h3>Un escritorio de verdad, en tiempo real</h3>
              <p className="lp-muted">Ventanas, Dock, buscador y widgets: todo se actualiza solo, sin recargar la página.</p>
              <Bullets
                items={[
                  { b: "Todo en vivo.", t: "Discos, tareas, archivos que cambian y usuarios conectados, al instante." },
                  { b: "La misma versión que tu NAS.", t: "El escritorio lo entrega el propio NAS, firmado; el portal solo lo verifica y lo abre." },
                  { b: "Claro u oscuro.", t: "Con tu color de acento y tu fondo, igual que en casa." },
                ]}
              />
            </div>
          </div>
          <div className="lp-feature lp-flip">
            <div className="lp-media">
              <Shot name="raid-wizard.png" alt="Asistente para crear un volumen RAID" />
            </div>
            <div>
              <h3>Almacenamiento sin miedo</h3>
              <Bullets
                items={[
                  { b: "Nada se borra sin confirmar.", t: "Cada operación muestra qué discos toca y te pide escribir el nombre del volumen." },
                  { b: "RAID 1, 5, 6 y 10.", t: "Con el mismo mdadm + Btrfs que usan los NAS comerciales." },
                  { b: "Reemplazá un disco.", t: "Te avisa si un disco falla y te guía para reconstruir." },
                ]}
              />
            </div>
          </div>
          <div className="lp-feature">
            <div className="lp-media">
              <Shot name="share-link.png" alt="Compartir un archivo con un enlace" />
            </div>
            <div>
              <h3>Compartí con un enlace</h3>
              <p className="lp-muted">Desde Archivos, «Compartir enlace» crea una dirección tangoos.go-link.org/s/… para quien vos quieras.</p>
              <Bullets
                items={[
                  { b: "Sin cuenta para quien recibe.", t: "Abre el enlace y baja el archivo directo desde tu NAS." },
                  { b: "Con vencimiento y contraseña.", t: "Y un límite de descargas, si querés." },
                  { b: "Revocable.", t: "Lo cerrás desde tu NAS y deja de funcionar al instante." },
                ]}
              />
            </div>
          </div>
        </div>
      </section>

      <section id="galeria" className="lp-section">
        <div className="lp-wrap">
          <Head eyebrow="Galería" title="Así se ve TangoOS." text="Capturas reales del escritorio, sin retoques." />
          <Gallery />
        </div>
      </section>

      <section id="seguridad" className="lp-section lp-sunken">
        <div className="lp-wrap">
          <Head
            eyebrow="Seguridad"
            title="El portal presenta; tu NAS decide."
            text="TangoConnect usa el servidor de señalización de go-link solo para que tu navegador y tu NAS se encuentren. Después hablan directo y cifrado."
          />
          <div className="lp-cards">
            <div className="lp-card">
              <span className="lp-k">Cifrado</span>
              <h3>De punta a punta</h3>
              <p>WebRTC con DTLS entre tu navegador y tu NAS. Ni el portal ni el servidor de señalización ven tus archivos.</p>
            </div>
            <div className="lp-card">
              <span className="lp-k">Identidad</span>
              <h3>Tu NAS prueba primero</h3>
              <p>Antes de mostrar tu vínculo, el NAS demuestra que es el mismo de siempre. Un impostor no pasa ese control.</p>
            </div>
            <div className="lp-card">
              <span className="lp-k">Acceso</span>
              <h3>Tu usuario, tu 2FA</h3>
              <p>Vincular no alcanza: además entrás con tu usuario de TangoOS y tu segundo factor. Podés revocar cada navegador.</p>
            </div>
          </div>
          <div className="lp-path">
            <svg viewBox="0 0 900 140" role="img" aria-label="Tu navegador y tu NAS conectados directo; el servidor de señalización solo los presenta">
              <rect x="10" y="40" width="200" height="60" rx="12" className="lp-svg-box" />
              <text x="110" y="76" textAnchor="middle" className="lp-svg-title">
                Tu navegador
              </text>
              <rect x="690" y="40" width="200" height="60" rx="12" className="lp-svg-box" />
              <text x="790" y="76" textAnchor="middle" className="lp-svg-title">
                Tu NAS
              </text>
              <line x1="210" y1="70" x2="690" y2="70" className="lp-svg-line" />
              <text x="450" y="58" textAnchor="middle" className="lp-svg-data">
                escritorio · archivos · cifrado DTLS
              </text>
              <rect x="350" y="104" width="200" height="30" rx="8" className="lp-svg-hub" />
              <text x="450" y="124" textAnchor="middle" className="lp-svg-small">
                signal.go-link.org
              </text>
              <line x1="300" y1="72" x2="370" y2="104" className="lp-svg-dash" />
              <line x1="600" y1="72" x2="530" y2="104" className="lp-svg-dash" />
            </svg>
          </div>
        </div>
      </section>

      <section id="preguntas" className="lp-section">
        <div className="lp-wrap">
          <Head eyebrow="Preguntas" title="Lo que todos preguntan." />
          <div className="lp-faq">
            {FAQ.map((f, i) => (
              <details key={f.q} open={i === 0}>
                <summary>{f.q}</summary>
                <p>{f.a}</p>
              </details>
            ))}
          </div>
        </div>
      </section>

      <SiteFooter />
    </Page>
  );
}

const FAQ = [
  {
    q: "¿Tengo que abrir puertos o configurar el router?",
    a: "No. Tu NAS se conecta hacia afuera al servidor de señalización de go-link y espera. Cuando entrás desde acá, navegador y NAS arman una conexión directa; si la red no lo permite, pasa por un relé cifrado.",
  },
  {
    q: "¿El portal guarda mis archivos o mi contraseña?",
    a: "No. El portal es una página estática. Tus archivos y tu contraseña viajan cifrados directo entre tu navegador y tu NAS. Lo único que se guarda, en este navegador, es el vínculo con tu NAS.",
  },
  {
    q: "¿Qué pasa si pierdo este navegador?",
    a: "Revocá el vínculo desde tu NAS en Ajustes → TangoConnect → Navegadores vinculados. Aunque alguien tenga el vínculo, igual necesita tu usuario y tu segundo factor para entrar.",
  },
  {
    q: "¿Necesito una cuenta?",
    a: "No. TangoConnect funciona como go-link Desk: un código de 9 dígitos para vincular y tu usuario de TangoOS para entrar.",
  },
  {
    q: "¿Puedo usar mi propio servidor de señalización?",
    a: "Sí. TangoOS y el portal son open source (MIT): podés publicar el portal en tu dominio y apuntar tu NAS a tu propio signalhub.",
  },
];

function Way({ chip, kind, title, text, shot }: { chip: string; kind: "ok" | "soon" | "info"; title: string; text: string; shot: string }) {
  return (
    <article className="lp-way">
      <div className="lp-shot">
        <Shot name={shot} alt={title} />
      </div>
      <div className="lp-way-body">
        <span className={`lp-chip lp-chip-${kind}`}>{chip}</span>
        <h3>{title}</h3>
        <p>{text}</p>
      </div>
    </article>
  );
}

/** Carrusel con scroll-snap: flechas, teclado y gesto táctil nativo. */
function Gallery() {
  const ref = useRef<HTMLDivElement>(null);
  const [i, setI] = useState(0);
  const go = (n: number) => {
    const el = ref.current;
    if (!el) return;
    const next = (n + GALLERY.length) % GALLERY.length;
    setI(next);
    const slide = el.children[next] as HTMLElement | undefined;
    el.scrollTo({ left: slide ? slide.offsetLeft - el.offsetLeft : 0, behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  };
  return (
    <div className="tc-gallery">
      <div
        className="tc-gallery-track"
        ref={ref}
        tabIndex={0}
        aria-label="Capturas de TangoOS"
        onKeyDown={(e) => {
          if (e.key === "ArrowRight") (e.preventDefault(), go(i + 1));
          if (e.key === "ArrowLeft") (e.preventDefault(), go(i - 1));
        }}
        onScroll={(e) => {
          const el = e.currentTarget;
          const w = (el.firstElementChild as HTMLElement | null)?.offsetWidth ?? 1;
          setI(Math.round(el.scrollLeft / (w + 18)));
        }}
      >
        {GALLERY.map((g) => (
          <figure key={g.name} className="tc-slide">
            <Shot name={g.name} alt={g.alt} />
            <figcaption>{g.alt}</figcaption>
          </figure>
        ))}
      </div>
      <div className="tc-gallery-bar">
        <button type="button" className="lp-btn lp-btn-ghost" onClick={() => go(i - 1)} aria-label="Anterior">
          ←
        </button>
        <span className="lp-meta">
          {Math.min(i + 1, GALLERY.length)} / {GALLERY.length}
        </span>
        <button type="button" className="lp-btn lp-btn-ghost" onClick={() => go(i + 1)} aria-label="Siguiente">
          →
        </button>
      </div>
    </div>
  );
}

/** El código del hero: se agrupa al escribir y va directo a vincular. */
function CodeBox() {
  const [code, setCode] = useState("");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    navigate(isCompleteCode(code) ? `/nas/vincular?code=${normalizeCode(code)}` : "/nas/vincular");
  };
  return (
    <form className="lp-codebox" onSubmit={submit}>
      <label htmlFor="lp-code" className="visually-hidden">
        Código de vinculación de tu NAS
      </label>
      <input
        id="lp-code"
        inputMode="numeric"
        autoComplete="off"
        spellCheck={false}
        placeholder="000 000 000"
        value={formatCode(code)}
        onChange={(e) => setCode(normalizeCode(e.target.value))}
      />
      <button type="submit" className="lp-btn lp-btn-primary">
        Vincular
      </button>
    </form>
  );
}
