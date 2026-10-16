import { useEffect, useState } from "react";
import { Bell, LayoutGrid, Loader2, Search, ShieldAlert, ShieldCheck, ArrowDownUp } from "lucide-react";
import { locale, logout, t, tn, useIsReal, useJobs, useLive, useTransport, useTransportState } from "@tango/client";
import { closeOverlay, closeWin, cycleWindows, focusWin, minimizeWin, openApp, registry, snapWin, toggleMaximize, toggleOverlay, ui, wm } from "./store";
import type { Menu } from "./store";
import { MenuList } from "./ContextMenu";
import { askPower } from "./Power";

export const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
export const MOD = isMac ? "⌥" : "Alt+";

function useClock() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const i = setInterval(() => setNow(new Date()), 15_000);
    return () => clearInterval(i);
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
  const tp = useTransport();
  // Peor alerta activa (§11); con el servidor real solo si implementa alerts.list, para no mezclar con la demo.
  const alertsReal = useIsReal("alerts.list");
  const mockOnly = tp.kind === "mock";
  const alertsOk = alertsReal || mockOnly;
  const alerts = useLive("alerts.list", undefined, ["alerts.changed", "storage.changed"]).data ?? [];
  const worst = alertsOk ? alerts.filter((a) => !a.resolved).reduce<"crit" | "warn" | null>((w, a) => (a.level === "crit" || w === "crit" ? "crit" : a.level === "warn" ? "warn" : w), null) : null;
  const degraded = vols.some((v) => v.state === "degraded") || worst === "crit";

  const tangoMenu: Menu = {
    label: "TangoOS",
    items: [
      { label: t("Acerca de este NAS"), action: () => openApp("tango.settings", { section: "general" }) },
      { separator: true },
      { label: t("Ajustes…"), action: () => openApp("tango.settings") },
      { label: t("Actualizaciones…"), action: () => openApp("tango.settings", { section: "updates" }) },
      { separator: true },
      { label: t("Bloquear pantalla"), shortcut: `${MOD}L`, action: () => ui.set((s) => ({ ...s, session: "locked", overlay: null })) },
      { label: t("Cerrar sesión de {name}…", { name: user?.full ?? "" }), action: () => void logout(tp).then(() => ui.set((s) => ({ ...s, session: "login", overlay: null }))) },
      { separator: true },
      { label: t("Reiniciar…"), action: () => askPower("reboot") },
      { label: t("Apagar…"), action: () => askPower("shutdown") },
    ],
  };

  const appMenu: Menu | null = app
    ? { label: app.hidden && fw ? fw.title : app.name, items: [{ label: t("Acerca de {name}", { name: app.hidden && fw ? fw.title : app.name }), disabled: true }, { separator: true }, { label: t("Cerrar {name}", { name: app.name }), shortcut: `${MOD}W`, action: () => windows.filter((w) => w.app === app.id).forEach((w) => closeWin(w.id)) }] }
    : { label: t("Escritorio"), items: [{ label: t("Cambiar fondo…"), action: () => openApp("tango.settings", { section: "appearance" }) }] };

  const windowMenu: Menu = {
    label: t("Ventana"),
    items: [
      { label: t("Minimizar"), shortcut: `${MOD}M`, disabled: !fw, action: () => fw && minimizeWin(fw.id) },
      { label: t("Ampliar"), disabled: !fw, action: () => fw && toggleMaximize(fw.id) },
      { label: t("Mover a la izquierda"), shortcut: `${MOD}←`, disabled: !fw, action: () => fw && snapWin(fw.id, "left") },
      { label: t("Mover a la derecha"), shortcut: `${MOD}→`, disabled: !fw, action: () => fw && snapWin(fw.id, "right") },
      { label: t("Ciclar ventanas"), shortcut: `${MOD}\``, action: cycleWindows },
      { label: t("Mostrar todas las ventanas"), shortcut: `${MOD}↑`, action: () => toggleOverlay("mission") },
      { separator: true },
      ...windows.map((w) => ({ label: w.title, checked: w.id === focused, action: () => focusWin(w.id) })),
    ],
  };

  const helpMenu: Menu = { label: t("Ayuda"), items: [{ label: t("Atajos de teclado"), action: () => openApp("tango.settings", { section: "keyboard" }) }, { label: t("Documentación de TangoOS"), disabled: true }] };

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
        {state !== "connected" && <span className="tg-mb-chip">{t("Conectando…")}</span>}
        {jobs.length > 0 && (
          <button type="button" className="tg-mb-icon" aria-label={tn(jobs.length, "{n} tarea en curso", "{n} tareas en curso")} data-open={overlay === "activity" || undefined} onClick={() => toggleOverlay("activity")}>
            <Loader2 size={15} className="tg-spin" />
            <span className="tg-mb-count">{jobs.length}</span>
          </button>
        )}
        <button
          type="button"
          className="tg-mb-icon"
          aria-label={degraded ? t("Almacenamiento con problemas") : worst === "warn" ? t("Almacenamiento con advertencias") : t("Almacenamiento en orden")}
          onClick={() => openApp("tango.storage")}
          style={degraded ? { color: "var(--tg-crit)" } : worst === "warn" ? { color: "var(--tg-warn)" } : undefined}
        >
          {degraded || worst === "warn" ? <ShieldAlert size={15} /> : <ShieldCheck size={15} />}
        </button>
        <button type="button" className="tg-mb-icon" aria-label={t("Transferencias y actividad")} data-open={overlay === "activity" || undefined} onClick={() => toggleOverlay("activity")}>
          <ArrowDownUp size={15} />
        </button>
        <button type="button" className="tg-mb-icon" aria-label={t("Buscar")} onClick={() => toggleOverlay("spotlight")}>
          <Search size={15} />
        </button>
        <button type="button" className="tg-mb-icon" aria-label={t("Centro de control")} data-open={overlay === "control" || undefined} onClick={() => toggleOverlay("control")}>
          <LayoutGrid size={15} />
        </button>
        <button type="button" className="tg-mb-icon" aria-label={t("Notificaciones")} data-open={overlay === "notifications" || undefined} onClick={() => toggleOverlay("notifications")}>
          <Bell size={15} />
        </button>
        <button type="button" className="tg-mb-clock" onClick={() => toggleOverlay("notifications")}>
          {now.toLocaleDateString(locale(), { weekday: "short", day: "numeric", month: "short" }).replace(".", "")}
          <span>{now.toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit", hourCycle: "h23" })}</span>
        </button>
      </div>
    </header>
  );
}
