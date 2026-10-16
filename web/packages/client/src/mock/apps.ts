// Centro de apps simulado (protocol/README.md §9). Datos DEMO.
import { RpcError } from "../protocol";
import { t } from "../i18n";
import type { Job, Method, Notice, Params, Result, Scenario, Topic, Topics, Volume } from "../protocol";
import type { CatalogApp, InstalledApp } from "../protocol-apps";

const MB = 1e6;

export interface MockAppsCtx {
  /** Función: los handlers se construyen antes de que el constructor asigne el escenario. */
  scenario: () => Scenario;
  emit: <T extends Topic>(topic: T, d: Topics[T]) => void;
  startJob: (type: string, title: string, detail: string, progress: number, step: number, rate?: string, onDone?: () => void) => Job & { onTick?: (p: number) => void };
  notify: (level: Notice["level"], app: string, title: string, body: string) => void;
  volumes: () => Volume[];
}

/** Ícono demo: squircle con degradé + glifo simple, como data-URL SVG. */
function icon(from: string, to: string, glyph: string): string {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${from}"/><stop offset="1" stop-color="${to}"/></linearGradient></defs><rect x="4" y="4" width="92" height="92" rx="24" fill="url(#g)"/><g fill="none" stroke="#fff" stroke-width="6" stroke-linecap="round" stroke-linejoin="round">${glyph}</g></svg>`;
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

// Función: las descripciones se traducen al pedir el catálogo.
const catalog = (): CatalogApp[] => [
  {
    id: "jellyfin", name: "Jellyfin", category: "Multimedia", version: "10.10.3", webPort: 8096, sizeHint: 420 * MB,
    description: t("Tu propio servidor de películas, series y música. Mirá tu biblioteca desde el TV, el celular o el navegador, con transcodificación por hardware cuando el equipo la soporta."),
    notes: t("Si tu CPU es Intel, TangoOS habilita la aceleración por hardware (QSV) automáticamente."),
    icon: icon("#aa5cc3", "#00a4dc", '<path d="M50 22 L74 70 H26 Z"/><path d="M50 42 L62 64 H38 Z" fill="#fff" stroke="none"/>'),
    options: [{ key: "media", label: t("Carpeta de medios"), type: "string", default: "Media", required: true }],
  },
  {
    id: "immich", name: "Immich", category: "Fotos", version: "1.122.0", webPort: 2283, sizeHint: 1800 * MB,
    description: t("Respaldo automático de fotos y videos desde el celular, con línea de tiempo, reconocimiento de caras y búsqueda. La alternativa libre a Google Fotos."),
    icon: icon("#ffb400", "#fa2921", '<circle cx="38" cy="40" r="12"/><circle cx="62" cy="40" r="12"/><circle cx="38" cy="62" r="12"/><circle cx="62" cy="62" r="12"/>'),
    options: [
      { key: "photos", label: t("Carpeta de fotos"), type: "string", default: "Fotos", required: true },
      { key: "dbPassword", label: t("Contraseña de la base de datos"), type: "password", required: true },
    ],
  },
  {
    id: "homeassistant", name: "Home Assistant", category: "Hogar", version: "2026.10.1", webPort: 8123, sizeHint: 1500 * MB, requiresHostNetwork: true,
    description: t("Domótica local: luces, enchufes, sensores y automatizaciones, sin depender de la nube del fabricante."),
    notes: t("Usa la red del NAS directamente para descubrir dispositivos (mDNS, UPnP)."),
    icon: icon("#41bdf5", "#038fc7", '<path d="M24 52 L50 28 L76 52 V76 H24 Z"/><circle cx="50" cy="56" r="7"/>'),
    options: [],
  },
  {
    id: "nextcloud", name: "Nextcloud", category: "Productividad", version: "30.0.2", webPort: 8080, sizeHint: 1100 * MB,
    description: t("Archivos, calendario, contactos y documentos colaborativos en tu propia nube."),
    icon: icon("#2f9bff", "#0058b8", '<circle cx="30" cy="50" r="9"/><circle cx="50" cy="50" r="14"/><circle cx="70" cy="50" r="9"/>'),
    options: [
      { key: "adminUser", label: t("Usuario administrador"), type: "string", default: "admin", required: true },
      { key: "adminPassword", label: t("Contraseña del administrador"), type: "password", required: true },
    ],
  },
  {
    id: "pihole", name: "Pi-hole", category: "Red", version: "2024.07.0", webPort: 8081, sizeHint: 110 * MB,
    description: t("Bloqueador de publicidad y rastreadores para toda la casa, a nivel DNS."),
    notes: t("Necesita el puerto 53 libre. Después configurá tu router para usar el NAS como DNS."),
    icon: icon("#96060c", "#f60d1a", '<path d="M50 24 C60 34 60 46 50 56 C40 46 40 34 50 24 Z"/><path d="M50 56 V78"/>'),
    options: [{ key: "webPassword", label: t("Contraseña del panel"), type: "password", required: true }],
  },
  {
    id: "vaultwarden", name: "Vaultwarden", category: "Seguridad", version: "1.32.5", webPort: 8082, sizeHint: 90 * MB,
    description: t("Gestor de contraseñas compatible con las apps de Bitwarden, alojado en tu NAS."),
    icon: icon("#3c8dbc", "#175ddc", '<path d="M50 22 L74 32 V52 C74 66 64 74 50 80 C36 74 26 66 26 52 V32 Z"/>'),
    options: [{ key: "signups", label: t("Permitir registros nuevos"), type: "bool", default: false }],
  },
  {
    id: "syncthing", name: "Syncthing", category: "Utilidades", version: "1.28.1", webPort: 8384, sizeHint: 40 * MB,
    description: t("Sincronización continua de carpetas entre tus equipos, cifrada y sin servidores de terceros."),
    icon: icon("#0891d1", "#155e8b", '<circle cx="50" cy="50" r="22"/><circle cx="50" cy="28" r="5" fill="#fff"/><circle cx="31" cy="61" r="5" fill="#fff"/><circle cx="69" cy="61" r="5" fill="#fff"/>'),
    options: [],
  },
  {
    id: "uptime-kuma", name: "Uptime Kuma", category: "Utilidades", version: "1.23.15", webPort: 3001, sizeHint: 160 * MB,
    description: t("Monitoreo de servicios y sitios web con alertas por Telegram, email y más."),
    icon: icon("#5cdd8b", "#1f9d55", '<path d="M22 54 H36 L44 36 L56 70 L64 50 H78"/>'),
    options: [{ key: "port", label: t("Puerto web"), type: "port", default: 3001, required: true }],
  },
];

/** Página demo que se muestra dentro de la ventana de la app (el contenedor no existe en el mock). */
function demoPage(app: CatalogApp): string {
  const html = `<!doctype html><meta charset="utf-8"><body style="margin:0;font:15px system-ui;background:#101418;color:#e8eaee;display:grid;place-items:center;height:100vh"><div style="text-align:center"><img src="${app.icon}" width="96" height="96"><h1 style="margin:16px 0 6px;font-weight:650">${app.name}</h1><p style="opacity:.7;margin:0">${t("Demo: acá se vería la interfaz web de {name} (puerto {port}).", { name: app.name, port: app.webPort ?? "—" })}</p></div></body>`;
  return `data:text/html;charset=utf-8,${encodeURIComponent(html)}`;
}

const pick = (id: string) => catalog().find((a) => a.id === id);

export function appsHandlers(ctx: MockAppsCtx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  const docker = { installed: false, running: false, version: null as string | null };
  const installed: InstalledApp[] = [];
  const toInstalled = (a: CatalogApp, volume: string, state: InstalledApp["state"]): InstalledApp => ({
    id: a.id, name: a.name, icon: a.icon, version: a.version, volume, dataPath: `/${volume}/@apps/${a.id}`, state,
    url: a.webPort ? demoPage(a) : null, cpu: state === "running" ? 0.02 : null, mem: state === "running" ? 180 * MB : null,
  });
  // Estado inicial perezoso: depende del escenario, que todavía no existe al construir los handlers.
  let ready = false;
  const init = () => {
    if (ready) return;
    ready = true;
    const fresh = ctx.scenario() === "empty" || ctx.scenario() === "migrate";
    if (fresh) return;
    Object.assign(docker, { installed: true, running: true, version: "26.1.5+dfsg1" });
    installed.push(toInstalled(pick("jellyfin")!, "volume1", "running"), toInstalled(pick("uptime-kuma")!, "volume1", "stopped"));
  };

  const changed = (id?: string) => ctx.emit("apps.changed", { id });
  const find = (id: string) => {
    init();
    const a = installed.find((x) => x.id === id);
    if (!a) throw new RpcError("APP_NOT_FOUND", "La app no está instalada");
    return a;
  };
  const requireDocker = () => {
    init();
    if (!docker.installed) throw new RpcError("DOCKER_NOT_INSTALLED", "Primero instalá Docker");
  };

  /** Job con líneas de log que avanzan con el progreso (job.detail). */
  const logged = (type: string, title: string, lines: string[], step: number, onDone: () => void) => {
    const job = ctx.startJob(type, title, lines[0], 0, step, undefined, onDone);
    job.onTick = (p) => {
      job.detail = lines[Math.min(lines.length - 1, Math.floor(p * lines.length))];
    };
    return { job: job.id };
  };

  const simple = (id: string, verb: string, title: string, next: InstalledApp["state"], during?: InstalledApp["state"]) => {
    requireDocker();
    const a = find(id);
    if (during) {
      a.state = during;
      changed(id);
    }
    const j = ctx.startJob(`apps.${verb}`, t(title, { name: a.name }), `docker compose -p tango-${id}`, 0, 0.2, undefined, () => {
      a.state = next;
      a.cpu = next === "running" ? 0.02 : null;
      a.mem = next === "running" ? 180 * MB : null;
      changed(id);
    });
    return { job: j.id };
  };

  return {
    "apps.docker.status": () => (init(), { ...docker }),
    "apps.docker.install": () => {
      init();
      if (docker.installed) throw new RpcError("DOCKER_INSTALLED", "Docker ya está instalado");
      return logged(
        "apps.docker.install",
        t("Instalando Docker"),
        [t("Actualizando la lista de paquetes…"), t("Descargando docker.io y docker-compose…"), t("Instalando paquetes…"), t("Configurando aislamiento (userns-remap)…"), t("Iniciando el servicio docker…"), t("Docker listo")],
        0.025,
        () => {
          Object.assign(docker, { installed: true, running: true, version: "26.1.5+dfsg1" });
          changed();
          ctx.notify("ok", "tango.apps", t("Docker está listo"), t("Ya podés instalar apps desde el Centro de apps."));
        },
      );
    },
    "apps.catalog": () => catalog(),
    "apps.list": () => (init(), installed),
    "apps.install": ({ id, volume, options }) => {
      requireDocker();
      const app = pick(id);
      if (!app) throw new RpcError("APP_NOT_FOUND", "La app no está en el catálogo");
      if (installed.some((a) => a.id === id)) throw new RpcError("APP_INSTALLED", t("{name} ya está instalada", { name: app.name }));
      if (!ctx.volumes().some((v) => v.id === volume || v.name === volume)) throw new RpcError("VOLUME_NOT_FOUND", "El volumen no existe");
      for (const o of app.options) if (o.required && (options[o.key] === undefined || options[o.key] === "")) throw new RpcError("INVALID_PARAMS", t("Falta «{field}»", { field: o.label }));
      const volName = ctx.volumes().find((v) => v.id === volume || v.name === volume)!.name;
      const entry = toInstalled(app, volName, "installing");
      installed.push(entry);
      changed(id);
      return logged(
        "apps.install",
        t("Instalando {name}", { name: app.name }),
        [
          t("Creando subvolumen {path}", { path: `/${volName}/@apps/${id}` }),
          t("Descargando imagen {image}…", { image: `${id}:${app.version}` }),
          t("Descargando capas ({size} MB)…", { size: Math.round(app.sizeHint / MB) }),
          t("Verificando imagen…"),
          `docker compose -p tango-${id} up -d`,
          t("Abriendo proxy HTTPS de {name}…", { name: app.name }),
          t("{name} está corriendo", { name: app.name }),
        ],
        0.03,
        () => {
          entry.state = "running";
          entry.cpu = 0.03;
          entry.mem = 210 * MB;
          changed(id);
          ctx.notify("ok", "tango.apps", t("{name} está lista", { name: app.name }), t("Abrila desde el Centro de apps o el Launcher."));
        },
      );
    },
    "apps.start": ({ id }) => simple(id, "iniciar", "Iniciar {name}", "running"),
    "apps.stop": ({ id }) => simple(id, "detener", "Detener {name}", "stopped"),
    "apps.restart": ({ id }) => simple(id, "reiniciar", "Reiniciar {name}", "running"),
    "apps.update": ({ id }) => simple(id, "actualizar", "Actualizar {name}", "running", "updating"),
    "apps.uninstall": ({ id, keepData }) => {
      requireDocker();
      const a = find(id);
      const j = ctx.startJob("apps.uninstall", t("Desinstalando {name}", { name: a.name }), keepData ? t("Se conservan los datos") : t("Borrando datos"), 0, 0.12, undefined, () => {
        installed.splice(installed.indexOf(a), 1);
        changed(id);
        ctx.notify("info", "tango.apps", t("{name} se desinstaló", { name: a.name }), keepData ? t("Los datos siguen en {path}.", { path: a.dataPath }) : t("También se borraron sus datos."));
      });
      return { job: j.id };
    },
    "apps.logs": ({ id, tail }) => {
      const a = find(id);
      const now = Date.now();
      const msgs = a.state === "running"
        ? ["Startup complete", "Listening on 0.0.0.0", "Health check OK", "GET / 200 4ms", "Scheduled task completed", "Cache warmed"]
        : ["Received SIGTERM, shutting down", "Stopped"];
      const n = Math.min(tail, 40);
      return {
        lines: Array.from({ length: n }, (_, i) => {
          const ts = new Date(now - (n - i) * 7000).toISOString().replace("T", " ").slice(0, 19);
          return `${ts}  ${a.id}  ${msgs[(i * 7 + Math.floor(now / 2000)) % msgs.length]}`;
        }),
      };
    },
  };
}
