import { useEffect, useState } from "react";
import { Bell, LayoutGrid, Loader2, Search, ShieldAlert, ShieldCheck, ArrowDownUp } from "lucide-react";
import { logout, useIsReal, useJobs, useLive, useTransport, useTransportState } from "@tango/client";
import { closeOverlay, closeWin, cycleWindows, focusWin, minimizeWin, openApp, registry, snapWin, toggleMaximize, toggleOverlay, ui, wm } from "./store";
import type { Menu } from "./store";
import { MenuList } from "./ContextMenu";
import { askPower } from "./Power";

export const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
export const MOD = isMac ? "⌥" : "Alt+";

function useClock() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 15_000);
    return () => clearInterval(t);
  }, []);
  return now;
}

export function MenuBar({ logo: Logo }: { logo: React.ComponentType<{ size?: number; color?: string }> }) {
  const { windows, focused } = wm.use();
  const { overlay, openMenu, user } = ui.use();
  const fw = windows.find((w) => w.id === focused);
  const app = fw ? registry.get(fw.app) : undefined;
  const now = useClock();
  const jobs = useJobs().filter((j) => j.state === "running");
  const vols = useLive("storage.volumes.list", undefined, ["storage.changed"]).data ?? [];
  const state = useTransportState();
  const t = useTransport();
  // Peor alerta activa (§11); con el servidor real solo si implementa alerts.list, para no mezclar con la demo.
  const alertsReal = useIsReal("alerts.list");
  const mockOnly = t.kind === "mock";
  const alertsOk = alertsReal || mockOnly;
  const alerts = useLive("alerts.list", undefined, ["alerts.changed", "storage.changed"]).data ?? [];
  const worst = alertsOk ? alerts.filter((a) => !a.resolved).reduce<"crit" | "warn" | null>((w, a) => (a.level === "crit" || w === "crit" ? "crit" : a.level === "warn" ? "warn" : w), null) : null;
  const degraded = vols.some((v) => v.state === "degraded") || worst === "crit";

  const tangoMenu: Menu = {
    label: "TangoOS",
    items: [
      { label: "Acerca de este NAS", action: () => openApp("tango.settings", { section: "general" }) },
      { separator: true },
      { label: "Ajustes…", action: () => openApp("tango.settings") },
      { label: "Actualizaciones…", action: () => openApp("tango.settings", { section: "updates" }) },
      { separator: true },
      { label: "Bloquear pantalla", shortcut: `${MOD}L`, action: () => ui.set((s) => ({ ...s, session: "locked", overlay: null })) },
      { label: `Cerrar sesión de ${user?.full ?? ""}…`, action: () => void logout(t).then(() => ui.set((s) => ({ ...s, session: "login", overlay: null }))) },
      { separator: true },
      { label: "Reiniciar…", action: () => askPower("reboot") },
      { label: "Apagar…", action: () => askPower("shutdown") },
    ],
  };

  const appMenu: Menu | null = app
    ? { label: app.hidden && fw ? fw.title : app.name, items: [{ label: `Acerca de ${app.hidden && fw ? fw.title : app.name}`, disabled: true }, { separator: true }, { label: `Cerrar ${app.name}`, shortcut: `${MOD}W`, action: () => windows.filter((w) => w.app === app.id).forEach((w) => closeWin(w.id)) }] }
    : { label: "Escritorio", items: [{ label: "Cambiar fondo…", action: () => openApp("tango.settings", { section: "appearance" }) }] };

  const windowMenu: Menu = {
    label: "Ventana",
    items: [
      { label: "Minimizar", shortcut: `${MOD}M`, disabled: !fw, action: () => fw && minimizeWin(fw.id) },
      { label: "Ampliar", disabled: !fw, action: () => fw && toggleMaximize(fw.id) },
      { label: "Mover a la izquierda", shortcut: `${MOD}←`, disabled: !fw, action: () => fw && snapWin(fw.id, "left") },
      { label: "Mover a la derecha", shortcut: `${MOD}→`, disabled: !fw, action: () => fw && snapWin(fw.id, "right") },
      { label: "Ciclar ventanas", shortcut: `${MOD}\``, action: cycleWindows },
      { label: "Mostrar todas las ventanas", shortcut: `${MOD}↑`, action: () => toggleOverlay("mission") },
      { separator: true },
      ...windows.map((w) => ({ label: w.title, checked: w.id === focused, action: () => focusWin(w.id) })),
    ],
  };

  const helpMenu: Menu = { label: "Ayuda", items: [{ label: "Atajos de teclado", action: () => openApp("tango.settings", { section: "keyboard" }) }, { label: "Documentación de TangoOS", disabled: true }] };

  const menus: Menu[] = [tangoMenu, appMenu, ...(fw?.menus ?? []), windowMenu, helpMenu];

  const onMenuClick = (i: number) => ui.set((s) => ({ ...s, openMenu: s.openMenu === i ? null : i, overlay: null }));
  const onMenuHover = (i: number) => openMenu !== null && openMenu !== i && ui.set((s) => ({ ...s, openMenu: i }));

  const onBarKey = (e: React.KeyboardEvent) => {
    if (openMenu === null) return;
    if (e.key === "ArrowRight") ui.set((s) => ({ ...s, openMenu: ((s.openMenu ?? 0) + 1) % menus.length }));
    if (e.key === "ArrowLeft") ui.set((s) => ({ ...s, openMenu: ((s.openMenu ?? 0) - 1 + menus.length) % menus.length }));
  };

  return (
    <header className="tg-menubar" role="menubar" onKeyDown={onBarKey}>
      <div className="tg-mb-left">
        {menus.map((m, i) => (
          <div key={`${m.label}-${i}`} className="tg-mb-menu">
            <button
              type="button"
              className="tg-mb-item"
              data-open={openMenu === i || undefined}
              data-strong={i === 1 || undefined}
              aria-haspopup="menu"
              aria-expanded={openMenu === i}
              onClick={() => onMenuClick(i)}
              onMouseEnter={() => onMenuHover(i)}
            >
              {i === 0 ? <Logo size={15} /> : m.label}
            </button>
            {openMenu === i && (
              <div className="tg-popover tg-mb-drop">
                <MenuList items={m.items} onClose={closeOverlay} />
              </div>
            )}
          </div>
        ))}
      </div>
      <div className="tg-mb-right">
        {state !== "connected" && <span className="tg-mb-chip">Conectando…</span>}
        {jobs.length > 0 && (
          <button type="button" className="tg-mb-icon" aria-label={`${jobs.length} tareas en curso`} data-open={overlay === "activity" || undefined} onClick={() => toggleOverlay("activity")}>
            <Loader2 size={15} className="tg-spin" />
            <span className="tg-mb-count">{jobs.length}</span>
          </button>
        )}
        <button
          type="button"
          className="tg-mb-icon"
          aria-label={degraded ? "Almacenamiento con problemas" : worst === "warn" ? "Almacenamiento con advertencias" : "Almacenamiento en orden"}
          onClick={() => openApp("tango.storage")}
          style={degraded ? { color: "var(--tg-crit)" } : worst === "warn" ? { color: "var(--tg-warn)" } : undefined}
        >
          {degraded || worst === "warn" ? <ShieldAlert size={15} /> : <ShieldCheck size={15} />}
        </button>
        <button type="button" className="tg-mb-icon" aria-label="Transferencias y actividad" data-open={overlay === "activity" || undefined} onClick={() => toggleOverlay("activity")}>
          <ArrowDownUp size={15} />
        </button>
        <button type="button" className="tg-mb-icon" aria-label="Buscar" onClick={() => toggleOverlay("spotlight")}>
          <Search size={15} />
        </button>
        <button type="button" className="tg-mb-icon" aria-label="Centro de control" data-open={overlay === "control" || undefined} onClick={() => toggleOverlay("control")}>
          <LayoutGrid size={15} />
        </button>
        <button type="button" className="tg-mb-icon" aria-label="Notificaciones" data-open={overlay === "notifications" || undefined} onClick={() => toggleOverlay("notifications")}>
          <Bell size={15} />
        </button>
        <button type="button" className="tg-mb-clock" onClick={() => toggleOverlay("notifications")}>
          {now.toLocaleDateString("es-AR", { weekday: "short", day: "numeric", month: "short" }).replace(".", "")}
          <span>{now.toLocaleTimeString("es-AR", { hour: "2-digit", minute: "2-digit", hourCycle: "h23" })}</span>
        </button>
      </div>
    </header>
  );
}
