// El escritorio completo de TangoOS (los mismos paquetes que web/apps/desktop) montado con el transporte
// que se le pase. Lo usan la demo pública (/demo, MockTransport) y el modo desarrollo del escritorio
// remoto (RtcTransport sobre signalhub, cuando el NAS todavía no entrega su escritorio por `bundle`).
// Se carga en un chunk aparte: la landing no lo baja.
import "@fontsource-variable/inter";
import { StrictMode, lazy } from "react";
import { createRoot, type Root } from "react-dom/client";
import { TransportProvider, t, type Transport } from "@tango/client";
import { Desktop, askPower, openApp, registerApps, registerCommands, toggleOverlay, ui } from "@tango/shell";
import type { AppManifest } from "@tango/shell";
import {
  AppsIcon, DownloadsIcon, FilesIcon, LauncherIcon, LogIcon, MonitorIcon, SettingsIcon, SnapshotsIcon, StorageIcon, TangoLogo, TerminalIcon, TrashIcon, wallpaperURL,
} from "@tango/theme-milonga";
import { Widgets } from "@tango/widgets";

const apps: AppManifest[] = [
  { id: "tango.files", name: t("Archivos"), icon: FilesIcon, pinned: true, keywords: ["finder", "explorador", "carpetas"], window: { width: 980, height: 620, minWidth: 560, minHeight: 360 }, component: lazy(() => import("@tango/app-files")) },
  { id: "tango.storage", name: t("Almacenamiento"), icon: StorageIcon, pinned: true, singleInstance: true, keywords: ["discos", "volumen", "raid", "smart", "bahías"], window: { width: 1040, height: 680, minWidth: 760, minHeight: 480 }, component: lazy(() => import("@tango/app-storage")) },
  { id: "tango.monitor", name: "Monitor", icon: MonitorIcon, pinned: true, singleInstance: true, keywords: ["cpu", "memoria", "red", "rendimiento", "webrtc"], window: { width: 900, height: 600, minWidth: 640, minHeight: 420 }, component: lazy(() => import("@tango/app-monitor")) },
  { id: "tango.settings", name: t("Ajustes"), icon: SettingsIcon, pinned: true, singleInstance: true, keywords: ["preferencias", "usuarios", "red", "apariencia", "panel de control"], window: { width: 860, height: 640, minWidth: 700, minHeight: 460 }, component: lazy(() => import("@tango/app-settings")) },
  { id: "tango.snapshots", name: t("Instantáneas"), icon: SnapshotsIcon, pinned: true, singleInstance: true, keywords: ["snapshot", "versiones", "restaurar", "programar"], window: { width: 920, height: 620, minWidth: 640, minHeight: 420 }, component: lazy(() => import("@tango/app-snapshots")) },
  { id: "tango.log", name: t("Registro"), icon: LogIcon, pinned: true, singleInstance: true, keywords: ["auditoría", "logs", "journal", "eventos"], window: { width: 980, height: 620, minWidth: 680, minHeight: 400 }, component: lazy(() => import("@tango/app-log")) },
  { id: "tango.terminal", name: "Terminal", icon: TerminalIcon, pinned: true, keywords: ["shell", "ssh", "consola", "bash"], window: { width: 820, height: 520, minWidth: 480, minHeight: 300 }, component: lazy(() => import("@tango/app-terminal")) },
  { id: "tango.apps", name: t("Centro de apps"), icon: AppsIcon, pinned: true, singleInstance: true, keywords: ["docker", "contenedores", "paquetes", "jellyfin", "immich", "aplicaciones"], window: { width: 980, height: 660, minWidth: 720, minHeight: 460 }, component: lazy(() => import("@tango/app-apps")) },
  { id: "tango.appview", name: "App", icon: AppsIcon, pinned: false, hidden: true, window: { width: 1100, height: 720, minWidth: 480, minHeight: 320 }, component: lazy(() => import("@tango/app-apps").then((m) => ({ default: m.AppView }))) },
];

let registered = false;

function registerAll(transport: Transport) {
  if (registered) return;
  registered = true;
  registerApps(apps);
  registerCommands([
    { id: "cmd.mission", section: "Acciones", title: t("Mostrar todas las ventanas"), keywords: "mission control ventanas expose", run: () => toggleOverlay("mission") },
    { id: "cmd.reboot", section: "Acciones", title: t("Reiniciar el NAS…"), keywords: "reiniciar reboot", run: () => askPower("reboot") },
    { id: "cmd.terminal", section: "Acciones", title: t("Abrir una terminal"), keywords: "shell consola bash ssh", run: () => openApp("tango.terminal") },
    { id: "cmd.newvol", section: "Acciones", title: t("Crear volumen…"), keywords: "volumen raid discos crear", run: () => openApp("tango.storage", { action: "create" }) },
    { id: "cmd.scrub", section: "Acciones", title: t("Verificar volume1 (scrub)"), keywords: "scrub verificar integridad volumen", run: () => void transport.call("storage.scrub.start", { volume: "vol1" }).catch(() => {}) },
    { id: "set.appearance", section: "Ajustes", title: t("Apariencia y fondo de pantalla"), keywords: "tema oscuro claro acento wallpaper", run: () => openApp("tango.settings", { section: "appearance" }) },
    { id: "set.users", section: "Ajustes", title: t("Usuarios y grupos"), keywords: "usuario cuenta permisos", run: () => openApp("tango.settings", { section: "users" }) },
    { id: "set.shares", section: "Ajustes", title: t("Carpetas compartidas"), keywords: "share smb nfs permisos", run: () => openApp("tango.settings", { section: "shares" }) },
  ]);
}

export interface MountOptions {
  /** Entrar directo al escritorio (demo): sin arranque ni login. */
  skipLogin?: { name: string; full: string; color: string };
}

export function mountTangoDesktop(el: HTMLElement, transport: Transport, opts: MountOptions = {}): Root {
  registerAll(transport);
  if (opts.skipLogin) ui.set((s) => ({ ...s, session: "desktop", user: opts.skipLogin! }));
  const root = createRoot(el);
  root.render(
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
  return root;
}
