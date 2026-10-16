import "./i18n.en";
import "@fontsource-variable/inter";
import { StrictMode, lazy } from "react";
import { createRoot } from "react-dom/client";
import { HybridTransport, MockTransport, RtcTransport, TransportProvider, installDCStream, t } from "@tango/client";
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
  { id: "tango.files", name: t("Archivos"), icon: FilesIcon, pinned: true, keywords: ["finder", "explorador", "carpetas", "files", "explorer", "folders"], window: { width: 980, height: 620, minWidth: 560, minHeight: 360 }, component: lazy(() => import("@tango/app-files")) },
  { id: "tango.storage", name: t("Almacenamiento"), icon: StorageIcon, pinned: true, singleInstance: true, keywords: ["discos", "volumen", "raid", "smart", "bahías", "storage", "disks", "volume", "bays"], window: { width: 1040, height: 680, minWidth: 760, minHeight: 480 }, component: lazy(() => import("@tango/app-storage")) },
  { id: "tango.monitor", name: t("Monitor"), icon: MonitorIcon, pinned: true, singleInstance: true, keywords: ["cpu", "memoria", "red", "rendimiento", "webrtc", "memory", "network", "performance"], window: { width: 900, height: 600, minWidth: 640, minHeight: 420 }, component: lazy(() => import("@tango/app-monitor")) },
  { id: "tango.settings", name: t("Ajustes"), icon: SettingsIcon, pinned: true, singleInstance: true, keywords: ["preferencias", "usuarios", "red", "apariencia", "panel de control", "settings", "preferences", "users", "network", "appearance", "control panel"], window: { width: 860, height: 640, minWidth: 700, minHeight: 460 }, component: lazy(() => import("@tango/app-settings")) },
  { id: "tango.snapshots", name: t("Instantáneas"), icon: SnapshotsIcon, pinned: true, singleInstance: true, keywords: ["snapshot", "versiones", "restaurar", "programar", "snapshots", "versions", "restore", "schedule"], window: { width: 920, height: 620, minWidth: 640, minHeight: 420 }, component: lazy(() => import("@tango/app-snapshots")) },
  { id: "tango.log", name: t("Registro"), icon: LogIcon, pinned: true, singleInstance: true, keywords: ["auditoría", "logs", "journal", "eventos", "log", "audit", "events"], window: { width: 980, height: 620, minWidth: 680, minHeight: 400 }, component: lazy(() => import("@tango/app-log")) },
  { id: "tango.terminal", name: t("Terminal"), icon: TerminalIcon, pinned: true, keywords: ["shell", "ssh", "consola", "bash", "console"], window: { width: 820, height: 520, minWidth: 480, minHeight: 300 }, component: lazy(() => import("@tango/app-terminal")) },
  { id: "tango.apps", name: t("Centro de apps"), icon: AppsIcon, pinned: true, singleInstance: true, keywords: ["docker", "contenedores", "paquetes", "jellyfin", "immich", "aplicaciones", "containers", "packages", "applications", "app center", "app store"], window: { width: 980, height: 660, minWidth: 720, minHeight: 460 }, component: lazy(() => import("@tango/app-apps")) },
  { id: "tango.cluster", name: t("Clúster"), icon: ClusterIcon, pinned: false, singleInstance: true, keywords: ["gluster", "glusterfs", "réplica", "nodos", "alta disponibilidad", "cluster", "replica", "nodes", "high availability"], window: { width: 1020, height: 660, minWidth: 760, minHeight: 460 }, component: lazy(() => import("@tango/app-cluster")) },
  { id: "tango.appview", name: t("App"), icon: AppsIcon, pinned: false, hidden: true, window: { width: 1100, height: 720, minWidth: 480, minHeight: 320 }, component: lazy(() => import("@tango/app-apps").then((m) => ({ default: m.AppView }))) },
];
registerApps(apps);

registerCommands([
  { id: "cmd.mission", section: "Acciones", title: t("Mostrar todas las ventanas"), keywords: "mission control ventanas expose show all windows", run: () => toggleOverlay("mission") },
  { id: "cmd.reboot", section: "Acciones", title: t("Reiniciar el NAS…"), keywords: "reiniciar reboot restart", run: () => askPower("reboot") },
  { id: "cmd.shutdown", section: "Acciones", title: t("Apagar el NAS…"), keywords: "apagar shutdown power off", run: () => askPower("shutdown") },
  { id: "cmd.terminal", section: "Acciones", title: t("Abrir una terminal"), keywords: "shell consola bash ssh open terminal console", run: () => openApp("tango.terminal") },
  { id: "set.cert", section: "Ajustes", title: t("Certificado HTTPS"), keywords: "certificado ca tls https advertencia certificate warning", run: () => openApp("tango.settings", { section: "security" }) },
  { id: "set.services", section: "Ajustes", title: t("Servicios de archivos (SMB, NFS, SFTP, WebDAV, iSCSI)"), keywords: "smb nfs sftp webdav iscsi servicios file services", run: () => openApp("tango.settings", { section: "services" }) },
  { id: "set.notify", section: "Ajustes", title: t("Notificaciones por correo, ntfy y Telegram"), keywords: "notificaciones email correo ntfy telegram alertas notifications mail alerts", run: () => openApp("tango.settings", { section: "notifications" }) },
  { id: "set.iscsi", section: "Ajustes", title: t("Destinos iSCSI"), keywords: "iscsi lun disco red vm targets disk network", run: () => openApp("tango.storage", { section: "iscsi" }) },
  { id: "cmd.snapshot.fotos", section: "Acciones", title: t("Crear instantánea de «Fotos»"), keywords: "snapshot instantanea fotos create photos", run: () => transport.call("snapshots.create", { share: "fotos" }) },
  { id: "cmd.snapshot.docs", section: "Acciones", title: t("Crear instantánea de «Documentos»"), keywords: "snapshot instantanea documentos create documents", run: () => transport.call("snapshots.create", { share: "documentos" }) },
  { id: "cmd.scrub", section: "Acciones", title: t("Verificar volume1 (scrub)"), keywords: "scrub verificar integridad volumen verify check integrity volume", run: () => transport.call("storage.scrub.start", { volume: "vol1" }) },
  { id: "cmd.newvol", section: "Acciones", title: t("Crear volumen…"), keywords: "volumen raid discos crear create volume disks", run: () => openApp("tango.storage", { action: "create" }) },
  { id: "set.appearance", section: "Ajustes", title: t("Apariencia y fondo de pantalla"), keywords: "tema oscuro claro acento wallpaper appearance theme dark light accent background", run: () => openApp("tango.settings", { section: "appearance" }) },
  { id: "set.users", section: "Ajustes", title: t("Usuarios y grupos"), keywords: "usuario cuenta permisos users groups account permissions", run: () => openApp("tango.settings", { section: "users" }) },
  { id: "set.shares", section: "Ajustes", title: t("Carpetas compartidas"), keywords: "share smb nfs permisos shared folders shares permissions", run: () => openApp("tango.settings", { section: "shares" }) },
  { id: "set.updates", section: "Ajustes", title: t("Actualizaciones"), keywords: "update apt debian actualizaciones updates upgrade", run: () => openApp("tango.settings", { section: "updates" }) },
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
          { key: "downloads", label: t("Descargas"), icon: DownloadsIcon, onClick: () => openApp("tango.files", { path: "/homes/federico/Descargas" }) },
          { key: "trash", label: t("Papelera"), icon: TrashIcon, onClick: () => openApp("tango.files", { path: "trash" }) },
        ]}
      />
    </TransportProvider>
  </StrictMode>,
);
