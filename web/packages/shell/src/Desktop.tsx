import { useCallback, useEffect, useLayoutEffect, useState } from "react";
import type { ComponentType, ReactNode } from "react";
import { createPortal } from "react-dom";
import { resume, useTransport } from "@tango/client";
import { SetupAssistant } from "./Setup";
import { PowerHost } from "./Power";
import { MissionControl } from "./MissionControl";
import { Window } from "./Window";
import { Dock } from "./Dock";
import type { DockExtra } from "./Dock";
import { MenuBar, isMac } from "./MenuBar";
import { ContextMenuHost, showContextMenu } from "./ContextMenu";
import { Activity, ControlCenter, Launcher, NoticeToaster, NotificationCenter, Spotlight, Toasts, useOverlayDismiss } from "./Overlays";
import { Boot, Login } from "./Session";
import { closeOverlay, closeWin, cycleWindows, minimizeWin, openApp, prefs, snapWin, toggleOverlay, ui, wm } from "./store";
import type { Menu, WindowHandle } from "./store";

export interface DesktopProps {
  logo: ComponentType<{ size?: number; color?: string }>;
  launcherIcon: ComponentType<{ size?: number }>;
  wallpaper: (id: string) => string;
  dockExtras: DockExtra[];
  widgets?: ReactNode;
}

function useThemeMode() {
  const { mode, accent } = prefs.use();
  useLayoutEffect(() => {
    const root = document.documentElement;
    if (mode === "auto") root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", mode);
    const n = parseInt(accent.slice(1), 16);
    root.style.setProperty("--tg-accent", accent);
    root.style.setProperty("--tg-accent-rgb", `${(n >> 16) & 255} ${(n >> 8) & 255} ${n & 255}`);
  }, [mode, accent]);
}

function useShortcuts() {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (ui.get().session !== "desktop") return;
      const mod = e.metaKey || e.ctrlKey;
      const f = wm.get().focused;
      if (mod && e.key.toLowerCase() === "k") {
        e.preventDefault();
        return toggleOverlay("spotlight");
      }
      if (e.key === "Escape") return closeOverlay();
      if (e.key === "F3") {
        e.preventDefault();
        return toggleOverlay("mission");
      }
      if (!e.altKey || mod) return;
      // En el navegador Ctrl+W/T/N están reservados: usamos Alt/⌥ (docs/13 §9).
      const k = e.code;
      const act: Record<string, () => void> = {
        KeyW: () => f && closeWin(f),
        KeyM: () => f && minimizeWin(f),
        Backquote: cycleWindows,
        ArrowLeft: () => f && snapWin(f, "left"),
        ArrowRight: () => f && snapWin(f, "right"),
        ArrowUp: () => toggleOverlay("mission"),
        Space: () => toggleOverlay("launcher"),
        KeyL: () => ui.set((s) => ({ ...s, session: "locked", overlay: null })),
        KeyN: () => {
          const w = wm.get().windows.find((x) => x.id === f);
          if (w) openApp(w.app);
        },
      };
      if (act[k]) {
        e.preventDefault();
        act[k]();
      }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, []);
}

export function Desktop({ logo, launcherIcon, wallpaper, dockExtras, widgets }: DesktopProps) {
  useThemeMode();
  useShortcuts();
  useOverlayDismiss();
  const t = useTransport();
  const { session, overlay, snapPreview } = ui.use();
  const { windows, focused } = wm.use();
  const { wallpaper: wp, widgets: showWidgets } = prefs.use();
  const search = useCallback((q: string) => t.call("files.search", { q }), [t]);
  // Al terminar el arranque: si hay una sesión guardada válida, directo al escritorio.
  const [suggestedHostname, setSuggestedHostname] = useState("tango");
  const bootDone = useCallback(() => {
    if (ui.get().session !== "boot") return; // idempotente: puede llamarse por rAF y por el respaldo
    ui.set((s) => ({ ...s, session: "resuming" }));
    void (async () => {
      // Primer arranque: si el NAS todavía no está configurado, el asistente va antes que el login.
      try {
        const st = await t.call("setup.state", undefined);
        if (st.state === "pending") {
          setSuggestedHostname(st.suggestedHostname || st.hostname || "tango");
          ui.set((s) => ({ ...s, session: "setup" }));
          return;
        }
      } catch {
        /* backend sin asistente: seguimos con el login normal */
      }
      const u = await resume(t);
      ui.set((s) => (u ? { ...s, session: "desktop", user: u } : { ...s, session: "login" }));
    })();
  }, [t]);
  // El NAS rechazó la sesión (token vencido o revocado): volver al login.
  useEffect(() => t.onAuthLost(() => ui.set((s) => (s.session === "desktop" || s.session === "locked" ? { ...s, session: "login", overlay: null } : s))), [t]);

  const onDesktopContext = (e: React.MouseEvent) => {
    if (e.target !== e.currentTarget && !(e.target as HTMLElement).classList.contains("tg-widgets")) return;
    e.preventDefault();
    showContextMenu(e.clientX, e.clientY, [
      { label: "Cambiar fondo de pantalla…", action: () => openApp("tango.settings", { section: "appearance" }) },
      { label: showWidgets ? "Ocultar widgets" : "Mostrar widgets", action: () => prefs.set((p) => ({ ...p, widgets: !p.widgets })) },
      { separator: true },
      { label: "Ordenar ventanas en cascada", disabled: windows.length < 2, action: cascade },
      { label: "Mostrar escritorio", disabled: !windows.some((w) => !w.minimized), action: () => windows.forEach((w) => !w.minimized && minimizeWin(w.id)) },
    ]);
  };

  return (
    <div className="tg-desktop" style={{ backgroundImage: wallpaper(wp) }} data-session={session}>
      {(session === "boot" || session === "resuming") && <Boot logo={logo} onDone={bootDone} />}
      {session === "setup" && <SetupAssistant logo={logo} suggestedHostname={suggestedHostname} />}
      {session === "login" && <Login />}
      {(session === "desktop" || session === "locked") && (
        <div className="tg-stage" aria-hidden={session === "locked"} data-locked={session === "locked" || undefined}>
          <MenuBar logo={logo} />
          <main className="tg-workspace" onContextMenu={onDesktopContext}>
            {showWidgets && widgets}
          </main>
          {windows.map((w) => (
            <Window key={w.id} w={w} focused={w.id === focused} />
          ))}
          {snapPreview && <div className="tg-snap-preview" style={{ left: snapPreview.x + 6, top: snapPreview.y + 6, width: snapPreview.w - 12, height: snapPreview.h - 12 }} />}
          <Dock launcherIcon={launcherIcon} extras={dockExtras} />
          {overlay === "launcher" && <Launcher />}
          {overlay === "spotlight" && <Spotlight search={search} />}
          {overlay === "control" && <ControlCenter />}
          {overlay === "activity" && <Activity />}
          {overlay === "notifications" && <NotificationCenter />}
          {overlay === "mission" && <MissionControl />}
          <Toasts />
          <NoticeToaster />
          <ContextMenuHost />
        </div>
      )}
      {session === "locked" && <Login locked />}
      <PowerHost />
    </div>
  );
}

function cascade() {
  const ws = wm.get().windows.filter((w) => !w.minimized);
  wm.set((s) => ({
    ...s,
    windows: s.windows.map((w) => {
      const i = ws.findIndex((x) => x.id === w.id);
      return i < 0 ? w : { ...w, x: 80 + i * 32, y: 60 + i * 32, maximized: false, restore: undefined };
    }),
  }));
}

/** Toolbar unificada: el contenido se renderiza dentro de la barra de título. */
/** `inset`: ancho de la sidebar, para que la toolbar empiece donde empieza el contenido. */
export function WindowToolbar({ win, children, inset = 0 }: { win: WindowHandle; children: ReactNode; inset?: number }) {
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  useLayoutEffect(() => setSlot(document.getElementById(`tb-${win.id}`)), [win.id]);
  return slot
    ? createPortal(
        <>
          {inset > 0 && <div style={{ width: Math.max(0, inset - 88), flex: "none" }} />}
          {children}
        </>,
        slot,
      )
    : null;
}

/** Menús propios de la app en la MenuBar mientras su ventana tiene el foco. */
export function useWindowMenus(win: WindowHandle, menus: Menu[], deps: unknown[]) {
  useEffect(() => {
    win.setMenus(menus);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
}

export const shortcutLabel = (k: string) => (isMac ? `⌥${k}` : `Alt+${k}`);
