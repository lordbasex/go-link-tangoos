import "@fontsource-variable/inter";
import { StrictMode, lazy } from "react";
import { createRoot } from "react-dom/client";
import { HybridTransport, MockTransport, RtcTransport, TransportProvider, installDCStream } from "@tango/client";
import type { Scenario, Transport } from "@tango/client";
import { Desktop, askPower, openApp, registerApps, registerCommands, toggleOverlay } from "@tango/shell";
import type { AppManifest } from "@tango/shell";
import {
  AppsIcon, DownloadsIcon, FilesIcon, LauncherIcon, LogIcon, MonitorIcon, SettingsIcon, SnapshotsIcon, StorageIcon, TangoLogo, TerminalIcon, TrashIcon, wallpaperURL,
} from "@tango/theme-milonga";
import { Widgets } from "@tango/widgets";
import { ClusterIcon } from "@tango/app-cluster/Icon";

const params = new URLSearchParams(location.search);
const scenario = (params.get("scenario") ?? "healthy") as Scenario;
const mock = new MockTransport(scenario);

/**
 * `?mock` fuerza la demo; `?backend=rtc` fuerza WebRTC (en dev, vía el proxy de Vite a la VM).
 * Por defecto: servido por tangod (build de producción) → híbrido; `vite dev` → demo.
 */
/** Transporte ya abierto que deja el portal TangoConnect al montar este escritorio (bundle del NAS). */
const injected = (window as unknown as { __TANGO_TRANSPORT__?: RtcTransport }).__TANGO_TRANSPORT__;

function pickTransport(): Transport {
  if (injected) return new HybridTransport(injected, mock);
  if (params.has("mock")) return mock;
  if (params.get("backend") === "rtc" || import.meta.env.PROD) return new HybridTransport(new RtcTransport(), mock);
  return mock;
}
const transport = pickTransport();

const apps: AppManifest[] = [
  { id: "tango.files", name: "Archivos", icon: FilesIcon, pinned: true, keywords: ["finder", "explorador", "carpetas"], window: { width: 980, height: 620, minWidth: 560, minHeight: 360 }, component: lazy(() => import("@tango/app-files")) },
  { id: "tango.storage", name: "Almacenamiento", icon: StorageIcon, pinned: true, singleInstance: true, keywords: ["discos", "volumen", "raid", "smart", "bahías"], window: { width: 1040, height: 680, minWidth: 760, minHeight: 480 }, component: lazy(() => import("@tango/app-storage")) },
  { id: "tango.monitor", name: "Monitor", icon: MonitorIcon, pinned: true, singleInstance: true, keywords: ["cpu", "memoria", "red", "rendimiento", "webrtc"], window: { width: 900, height: 600, minWidth: 640, minHeight: 420 }, component: lazy(() => import("@tango/app-monitor")) },
  { id: "tango.settings", name: "Ajustes", icon: SettingsIcon, pinned: true, singleInstance: true, keywords: ["preferencias", "usuarios", "red", "apariencia", "panel de control"], window: { width: 860, height: 640, minWidth: 700, minHeight: 460 }, component: lazy(() => import("@tango/app-settings")) },
  { id: "tango.snapshots", name: "Instantáneas", icon: SnapshotsIcon, pinned: true, singleInstance: true, keywords: ["snapshot", "versiones", "restaurar", "programar"], window: { width: 920, height: 620, minWidth: 640, minHeight: 420 }, component: lazy(() => import("@tango/app-snapshots")) },
  { id: "tango.log", name: "Registro", icon: LogIcon, pinned: true, singleInstance: true, keywords: ["auditoría", "logs", "journal", "eventos"], window: { width: 980, height: 620, minWidth: 680, minHeight: 400 }, component: lazy(() => import("@tango/app-log")) },
  { id: "tango.terminal", name: "Terminal", icon: TerminalIcon, pinned: true, keywords: ["shell", "ssh", "consola", "bash"], window: { width: 820, height: 520, minWidth: 480, minHeight: 300 }, component: lazy(() => import("@tango/app-terminal")) },
  { id: "tango.apps", name: "Centro de apps", icon: AppsIcon, pinned: true, singleInstance: true, keywords: ["docker", "contenedores", "paquetes", "jellyfin", "immich", "aplicaciones"], window: { width: 980, height: 660, minWidth: 720, minHeight: 460 }, component: lazy(() => import("@tango/app-apps")) },
  { id: "tango.cluster", name: "Clúster", icon: ClusterIcon, pinned: false, singleInstance: true, keywords: ["gluster", "glusterfs", "réplica", "nodos", "alta disponibilidad", "cluster"], window: { width: 1020, height: 660, minWidth: 760, minHeight: 460 }, component: lazy(() => import("@tango/app-cluster")) },
  { id: "tango.appview", name: "App", icon: AppsIcon, pinned: false, hidden: true, window: { width: 1100, height: 720, minWidth: 480, minHeight: 320 }, component: lazy(() => import("@tango/app-apps").then((m) => ({ default: m.AppView }))) },
];
registerApps(apps);

registerCommands([
  { id: "cmd.mission", section: "Acciones", title: "Mostrar todas las ventanas", keywords: "mission control ventanas expose", run: () => toggleOverlay("mission") },
  { id: "cmd.reboot", section: "Acciones", title: "Reiniciar el NAS…", keywords: "reiniciar reboot", run: () => askPower("reboot") },
  { id: "cmd.shutdown", section: "Acciones", title: "Apagar el NAS…", keywords: "apagar shutdown", run: () => askPower("shutdown") },
  { id: "cmd.terminal", section: "Acciones", title: "Abrir una terminal", keywords: "shell consola bash ssh", run: () => openApp("tango.terminal") },
  { id: "set.cert", section: "Ajustes", title: "Certificado HTTPS", keywords: "certificado ca tls https advertencia", run: () => openApp("tango.settings", { section: "security" }) },
  { id: "set.services", section: "Ajustes", title: "Servicios de archivos (SMB, NFS, SFTP, WebDAV, iSCSI)", keywords: "smb nfs sftp webdav iscsi servicios", run: () => openApp("tango.settings", { section: "services" }) },
  { id: "set.notify", section: "Ajustes", title: "Notificaciones por correo, ntfy y Telegram", keywords: "notificaciones email correo ntfy telegram alertas", run: () => openApp("tango.settings", { section: "notifications" }) },
  { id: "set.iscsi", section: "Ajustes", title: "Destinos iSCSI", keywords: "iscsi lun disco red vm", run: () => openApp("tango.storage", { section: "iscsi" }) },
  { id: "cmd.snapshot.fotos", section: "Acciones", title: "Crear instantánea de «Fotos»", keywords: "snapshot instantanea fotos", run: () => transport.call("snapshots.create", { share: "fotos" }) },
  { id: "cmd.snapshot.docs", section: "Acciones", title: "Crear instantánea de «Documentos»", keywords: "snapshot instantanea documentos", run: () => transport.call("snapshots.create", { share: "documentos" }) },
  { id: "cmd.scrub", section: "Acciones", title: "Verificar volume1 (scrub)", keywords: "scrub verificar integridad volumen", run: () => transport.call("storage.scrub.start", { volume: "vol1" }) },
  { id: "cmd.newvol", section: "Acciones", title: "Crear volumen…", keywords: "volumen raid discos crear", run: () => openApp("tango.storage", { action: "create" }) },
  { id: "set.appearance", section: "Ajustes", title: "Apariencia y fondo de pantalla", keywords: "tema oscuro claro acento wallpaper", run: () => openApp("tango.settings", { section: "appearance" }) },
  { id: "set.users", section: "Ajustes", title: "Usuarios y grupos", keywords: "usuario cuenta permisos", run: () => openApp("tango.settings", { section: "users" }) },
  { id: "set.shares", section: "Ajustes", title: "Carpetas compartidas", keywords: "share smb nfs permisos", run: () => openApp("tango.settings", { section: "shares" }) },
  { id: "set.updates", section: "Ajustes", title: "Actualizaciones", keywords: "update apt debian", run: () => openApp("tango.settings", { section: "updates" }) },
]);

// En el portal, /sw.js es del portal (no del NAS): el streaming por Service Worker queda para la red local.
if (!injected) installDCStream();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <TransportProvider transport={transport}>
      <Desktop
        logo={TangoLogo}
        launcherIcon={LauncherIcon}
        wallpaper={(id) => wallpaperURL(id as Parameters<typeof wallpaperURL>[0])}
        widgets={<Widgets />}
        dockExtras={[
          { key: "downloads", label: "Descargas", icon: DownloadsIcon, onClick: () => openApp("tango.files", { path: "/homes/federico/Descargas" }) },
          { key: "trash", label: "Papelera", icon: TrashIcon, onClick: () => openApp("tango.files", { path: "trash" }) },
        ]}
      />
    </TransportProvider>
  </StrictMode>,
);
