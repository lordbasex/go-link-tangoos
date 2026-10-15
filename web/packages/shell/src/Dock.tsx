import { useEffect, useRef, useState } from "react";
import type { ComponentType, MouseEvent as RMouseEvent } from "react";
import { closeWin, focusWin, openApp, prefs, registry, restoreWin, toggleOverlay, ui, wm } from "./store";
import { showContextMenu } from "./ContextMenu";

interface Item {
  key: string;
  label: string;
  icon: ComponentType<{ size?: number }>;
  app?: string;
  running?: boolean;
  onClick: () => void;
  menu?: () => Parameters<typeof showContextMenu>[2];
}

const S_MAX = 1.6;
const RADIUS = 140;

export function Dock({ launcherIcon, extras }: { launcherIcon: ComponentType<{ size?: number }>; extras: Item[] }) {
  const { windows } = wm.use();
  const { dockSize, magnify } = prefs.use();
  const overlay = ui.use((s) => s.overlay);
  const [bouncing, setBouncing] = useState<string | null>(null);
  const [hint, setHint] = useState<string | null>(null);
  const bar = useRef<HTMLDivElement>(null);
  // La magnificación mueve los íconos entre pointerdown y pointerup: recordamos cuál se presionó.
  const pressed = useRef<string | null>(null);

  const pinned = [...registry.values()].filter((a) => a.pinned);
  const runningExtra = [...new Set(windows.map((w) => w.app))].filter((id) => !registry.get(id)?.pinned);
  const minimized = windows.filter((w) => w.minimized);

  const launch = (appId: string) => {
    const wins = windows.filter((w) => w.app === appId);
    if (!wins.length) {
      setBouncing(appId);
      setTimeout(() => setBouncing(null), 620);
      openApp(appId);
      return;
    }
    const vis = wins.filter((w) => !w.minimized).sort((a, b) => b.z - a.z);
    if (vis.length) focusWin(vis[0].id);
    else restoreWin(wins[wins.length - 1].id);
  };

  const appItem = (id: string): Item => {
    const a = registry.get(id)!;
    const wins = windows.filter((w) => w.app === id);
    return {
      key: id, app: id, label: a.name, icon: a.icon, running: wins.length > 0, onClick: () => launch(id),
      menu: () => [
        ...wins.map((w) => ({ label: w.title, checked: !w.minimized, action: () => (w.minimized ? restoreWin(w.id) : focusWin(w.id)) })),
        ...(wins.length ? [{ separator: true }] : []),
        { label: wins.length ? "Nueva ventana" : "Abrir", action: () => openApp(id), disabled: !!(a.singleInstance && wins.length) },
        ...(wins.length ? [{ separator: true }, { label: "Cerrar", action: () => wins.forEach((w) => closeWin(w.id)) }] : []),
      ],
    };
  };

  const items: (Item | "sep")[] = [
    { key: "launcher", label: "Aplicaciones", icon: launcherIcon, onClick: () => toggleOverlay("launcher") },
    ...pinned.map((a) => appItem(a.id)),
    ...runningExtra.map(appItem),
    "sep",
    ...minimized.map((w) => {
      const a = registry.get(w.app)!;
      return { key: `min-${w.id}`, label: w.title, icon: a.icon, onClick: () => restoreWin(w.id), app: undefined } as Item;
    }),
    ...extras,
  ];

  // ---- Magnificación: rAF + lerp, escribe estilos directo (sin re-render) ----
  useEffect(() => {
    const el = bar.current;
    if (!el) return;
    const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
    let mouseX: number | null = null;
    let raf = 0;
    let centers: number[] = [];
    const cells = () => [...el.querySelectorAll<HTMLElement>(".tg-dock-cell")];
    const scales = new Map<HTMLElement, number>();
    const measure = () => {
      const cs = cells();
      cs.forEach((c) => (c.style.width = `${dockSize}px`));
      centers = cs.map((c) => {
        const r = c.getBoundingClientRect();
        return r.left + r.width / 2;
      });
    };
    const frame = () => {
      let active = false;
      cells().forEach((c, i) => {
        const d = mouseX === null ? Infinity : Math.abs(mouseX - centers[i]);
        const target = mouseX === null || !magnify || reduce ? 1 : 1 + (S_MAX - 1) * ((Math.cos(Math.min(d / RADIUS, 1) * Math.PI) + 1) / 2);
        const cur = scales.get(c) ?? 1;
        const next = cur + (target - cur) * 0.32;
        const v = Math.abs(next - target) < 0.002 ? target : next;
        if (v !== target) active = true;
        scales.set(c, v);
        c.style.width = `${dockSize * v}px`;
        c.style.setProperty("--s", String(v));
        (c.firstElementChild as HTMLElement).style.transform = `scale(${v})`;
      });
      raf = active || mouseX !== null ? requestAnimationFrame(frame) : 0;
    };
    const kick = () => {
      if (!raf) raf = requestAnimationFrame(frame);
    };
    const onEnter = (e: MouseEvent) => {
      measure();
      mouseX = e.clientX;
      kick();
    };
    const onMove = (e: MouseEvent) => {
      if (!centers.length) measure();
      mouseX = e.clientX;
      kick();
    };
    const onLeave = () => {
      mouseX = null;
      centers = [];
      kick();
    };
    el.addEventListener("mouseenter", onEnter);
    el.addEventListener("mousemove", onMove);
    el.addEventListener("mouseleave", onLeave);
    return () => {
      cancelAnimationFrame(raf);
      el.removeEventListener("mouseenter", onEnter);
      el.removeEventListener("mousemove", onMove);
      el.removeEventListener("mouseleave", onLeave);
    };
  }, [dockSize, magnify, items.length]);

  const onContext = (it: Item) => (e: RMouseEvent) => {
    if (!it.menu) return;
    e.preventDefault();
    showContextMenu(e.clientX, e.clientY - 8, it.menu());
  };

  return (
    <nav className="tg-dock-wrap" aria-label="Dock" data-hidden={overlay === "launcher" || undefined}>
      <div
        className="tg-dock"
        ref={bar}
        style={{ ["--dock" as string]: `${dockSize}px` }}
        onPointerUp={(e) => {
          const key = pressed.current;
          pressed.current = null;
          if (e.button !== 0 || !key) return;
          const it = items.find((x) => x !== "sep" && x.key === key) as Item | undefined;
          it?.onClick();
        }}
        onPointerLeave={() => (pressed.current = null)}
      >
        {items.map((it) =>
          it === "sep" ? (
            <div key="sep" className="tg-dock-sep" role="separator" />
          ) : (
            <button
              key={it.key}
              type="button"
              className="tg-dock-cell"
              data-dock-app={it.app}
              data-running={it.running || undefined}
              data-bounce={bouncing === it.app || undefined}
              aria-label={it.label}
              onPointerDown={(e) => e.button === 0 && (pressed.current = it.key)}
              onClick={(e) => e.detail === 0 && it.onClick()}
              onContextMenu={onContext(it)}
              onMouseEnter={() => setHint(it.key)}
              onMouseLeave={() => setHint((h) => (h === it.key ? null : h))}
              onFocus={() => setHint(it.key)}
              onBlur={() => setHint(null)}
              style={{ width: dockSize }}
            >
              <span className="tg-dock-icon">
                <it.icon size={dockSize} />
              </span>
              {hint === it.key && <span className="tg-dock-tip">{it.label}</span>}
            </button>
          ),
        )}
      </div>
    </nav>
  );
}

export type DockExtra = Item;
