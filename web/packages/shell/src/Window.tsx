import { Component, Suspense, memo, useLayoutEffect, useMemo, useRef } from "react";
import type { ErrorInfo, PointerEvent as RPointerEvent, ReactNode } from "react";
import { t, tr } from "@tango/client";
import { closeWin, focusWin, minimizeWin, patch, registry, snapRect, toggleMaximize, ui, workArea } from "./store";
import type { Menu, Rect, SnapZone, WinState, WindowHandle } from "./store";

const EDGE = 6;
type Dir = "n" | "s" | "e" | "w" | "ne" | "nw" | "se" | "sw";

/** Rect del ícono del Dock de la app: origen/destino de abrir y minimizar. */
function dockTarget(app: string): DOMRect | null {
  return document.querySelector(`[data-dock-app="${app}"]`)?.getBoundingClientRect() ?? null;
}

function snapZoneAt(x: number, y: number): SnapZone | null {
  const W = window.innerWidth;
  const top = 28;
  const corner = 60;
  if (y <= top + 2) return x < corner ? "tl" : x > W - corner ? "tr" : "max";
  if (x <= 2) return y < top + corner ? "tl" : y > window.innerHeight - corner ? "bl" : "left";
  if (x >= W - 3) return y < top + corner ? "tr" : y > window.innerHeight - corner ? "br" : "right";
  return null;
}

export const Window = memo(function Window({ w, focused }: { w: WinState; focused: boolean }) {
  const app = registry.get(w.app)!;
  const ref = useRef<HTMLDivElement>(null);
  const min = { w: app.window.minWidth ?? 420, h: app.window.minHeight ?? 280 };

  // Animaciones de abrir / cerrar / minimizar / restaurar con Web Animations API.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
    const ease = "cubic-bezier(.2,.8,.2,1)";
    if (w.phase === "opening") {
      el.animate(reduce ? [{ opacity: 0 }, { opacity: 1 }] : [{ opacity: 0, transform: "scale(.94)" }, { opacity: 1, transform: "none" }], { duration: reduce ? 100 : 210, easing: ease });
    } else if (w.phase === "closing") {
      el.animate([{ opacity: 1, transform: "none" }, { opacity: 0, transform: "scale(.96)" }], { duration: 150, easing: "ease-in", fill: "forwards" });
    } else if (w.phase === "minimizing" || w.phase === "restoring") {
      const t = dockTarget(w.app);
      const r = el.getBoundingClientRect();
      const dx = t ? t.x + t.width / 2 - (r.x + r.width / 2) : 0;
      const dy = t ? t.y + t.height / 2 - (r.y + r.height / 2) : r.height;
      const sx = t ? Math.max(0.05, t.width / r.width) : 0.2;
      const sy = t ? Math.max(0.03, t.height / r.height) : 0.2;
      // "Genie" simplificado: la ventana se angosta abajo mientras viaja al Dock.
      const frames: Keyframe[] = [
        { transform: "none", opacity: 1, clipPath: "polygon(0 0,100% 0,100% 100%,0 100%)" },
        { transform: `translate(${dx * 0.45}px, ${dy * 0.6}px) scale(${Math.min(1, sx * 3)}, ${Math.min(1, sy * 2.2)})`, opacity: 0.9, clipPath: "polygon(0 0,100% 0,74% 100%,26% 100%)", offset: 0.5 },
        { transform: `translate(${dx}px, ${dy}px) scale(${sx}, ${sy})`, opacity: 0, clipPath: "polygon(0 0,100% 0,62% 100%,38% 100%)" },
      ];
      if (reduce) el.animate([{ opacity: w.phase === "minimizing" ? 1 : 0 }, { opacity: w.phase === "minimizing" ? 0 : 1 }], { duration: 120, fill: "forwards" });
      else el.animate(w.phase === "minimizing" ? frames : [...frames].reverse(), { duration: 330, easing: "cubic-bezier(.4,0,.2,1)", fill: w.phase === "minimizing" ? "forwards" : "none" });
    }
  }, [w.phase, w.app]);

  // ---- Arrastre: se escribe el transform directo en el DOM, React solo al soltar ----
  const startDrag = (e: RPointerEvent) => {
    if (e.button !== 0 || (e.target as HTMLElement).closest("button,input,[data-no-drag]")) return;
    const el = ref.current!;
    const start = { px: e.clientX, py: e.clientY, x: w.x, y: w.y };
    let restoreOffset: number | null = null;
    let moved = false;
    let zone: SnapZone | null = null;
    el.setPointerCapture(e.pointerId);
    const onMove = (ev: PointerEvent) => {
      const dx = ev.clientX - start.px;
      const dy = ev.clientY - start.py;
      if (!moved && Math.abs(dx) + Math.abs(dy) < 3) return;
      moved = true;
      // Arrastrar una ventana maximizada/ajustada la restaura bajo el cursor.
      if (w.restore && restoreOffset === null) {
        restoreOffset = (start.px - w.x) / w.w;
        const r = w.restore;
        patch(w.id, { w: r.w, h: r.h, maximized: false, restore: undefined });
        start.x = start.px - r.w * restoreOffset;
        start.y = w.y;
      }
      const area = workArea();
      const nx = start.x + dx;
      const ny = Math.min(Math.max(area.y, start.y + dy), window.innerHeight - 60);
      el.style.transform = `translate3d(${nx - w.x}px, ${ny - w.y}px, 0)`;
      zone = snapZoneAt(ev.clientX, ev.clientY);
      const prev = ui.get().snapPreview;
      const next = zone ? snapRect(zone) : null;
      if (JSON.stringify(prev) !== JSON.stringify(next)) ui.set((s) => ({ ...s, snapPreview: next }));
      (el as HTMLDivElement & { _pos?: Rect })._pos = { x: nx, y: ny, w: 0, h: 0 };
    };
    const onUp = () => {
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerup", onUp);
      el.removeEventListener("pointercancel", onUp);
      ui.set((s) => ({ ...s, snapPreview: null }));
      if (!moved) return;
      const pos = (el as HTMLDivElement & { _pos?: Rect })._pos;
      el.style.transform = "";
      if (zone) {
        const r = snapRect(zone);
        patch(w.id, { ...r, maximized: zone === "max", restore: { x: pos?.x ?? w.x, y: pos?.y ?? w.y, w: w.restore?.w ?? w.w, h: w.restore?.h ?? w.h } });
      } else if (pos) patch(w.id, { x: pos.x, y: pos.y });
    };
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerup", onUp);
    el.addEventListener("pointercancel", onUp);
  };

  const startResize = (dir: Dir) => (e: RPointerEvent) => {
    e.stopPropagation();
    if (e.button !== 0) return;
    focusWin(w.id);
    const el = ref.current!;
    const s = { px: e.clientX, py: e.clientY, ...{ x: w.x, y: w.y, w: w.w, h: w.h } };
    const target = e.currentTarget as HTMLElement;
    target.setPointerCapture(e.pointerId);
    let r: Rect = { x: w.x, y: w.y, w: w.w, h: w.h };
    const area = workArea();
    const onMove = (ev: PointerEvent) => {
      const dx = ev.clientX - s.px;
      const dy = ev.clientY - s.py;
      r = { x: s.x, y: s.y, w: s.w, h: s.h };
      if (dir.includes("e")) r.w = Math.max(min.w, s.w + dx);
      if (dir.includes("s")) r.h = Math.max(min.h, s.h + dy);
      if (dir.includes("w")) {
        r.w = Math.max(min.w, s.w - dx);
        r.x = s.x + s.w - r.w;
      }
      if (dir.includes("n")) {
        r.h = Math.max(min.h, s.h - dy);
        r.y = Math.max(area.y, s.y + s.h - r.h);
        r.h = s.y + s.h - r.y;
      }
      Object.assign(el.style, { left: `${r.x}px`, top: `${r.y}px`, width: `${r.w}px`, height: `${r.h}px` });
    };
    const onUp = () => {
      target.removeEventListener("pointermove", onMove);
      target.removeEventListener("pointerup", onUp);
      patch(w.id, { ...r, maximized: false, restore: undefined });
    };
    target.addEventListener("pointermove", onMove);
    target.addEventListener("pointerup", onUp);
  };

  const handle = useMemo<WindowHandle>(
    () => ({
      id: w.id,
      args: w.args,
      focused,
      setTitle: (t: string) => patch(w.id, { title: t }),
      setMenus: (m: Menu[]) => patch(w.id, { menus: m }),
      close: () => closeWin(w.id),
    }),
    [w.id, w.args, focused],
  );

  const AppComp = app.component;
  const hidden = w.minimized;

  return (
    <div
      ref={ref}
      className="tg-window"
      data-win={w.id}
      data-focused={focused || undefined}
      data-maximized={w.maximized || undefined}
      data-plain={app.plainTitlebar || undefined}
      role="dialog"
      aria-label={w.title}
      style={{ left: w.x, top: w.y, width: w.w, height: w.h, zIndex: w.z, visibility: hidden ? "hidden" : undefined }}
      onPointerDownCapture={() => focusWin(w.id)}
    >
      <div className="tg-titlebar" onPointerDown={startDrag} onDoubleClick={(e) => !(e.target as HTMLElement).closest("button,input,[data-no-drag]") && toggleMaximize(w.id)}>
        <div className="tg-traffic" data-no-drag>
          <button type="button" className="tg-tl tg-tl-close" aria-label={t("Cerrar")} onClick={() => closeWin(w.id)}>
            <svg viewBox="0 0 12 12"><path d="M3.5 3.5l5 5m0-5l-5 5" /></svg>
          </button>
          <button type="button" className="tg-tl tg-tl-min" aria-label={t("Minimizar")} onClick={() => minimizeWin(w.id)}>
            <svg viewBox="0 0 12 12"><path d="M3 6h6" /></svg>
          </button>
          <button type="button" className="tg-tl tg-tl-max" aria-label={t("Ampliar")} onClick={() => toggleMaximize(w.id)}>
            <svg viewBox="0 0 12 12"><path d="M3.6 8.4V4.8l3.6 3.6Z M8.4 3.6v3.6L4.8 3.6Z" fill="currentColor" stroke="none" /></svg>
          </button>
        </div>
        <div className="tg-title-slot" id={`tb-${w.id}`} />
        <div className="tg-title-text">{w.title}</div>
      </div>
      <div className="tg-window-body">
        <AppBoundary name={app.name}>
          <Suspense fallback={<div className="tg-window-loading" />}>
            <AppComp win={handle} />
          </Suspense>
        </AppBoundary>
      </div>
      {!w.maximized &&
        (["n", "s", "e", "w", "ne", "nw", "se", "sw"] as Dir[]).map((d) => <div key={d} className={`tg-rz tg-rz-${d}`} style={{ ["--edge" as string]: `${EDGE}px` }} onPointerDown={startResize(d)} />)}
    </div>
  );
});

class AppBoundary extends Component<{ name: string; children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {};
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  componentDidCatch(e: Error, info: ErrorInfo) {
    console.error(`[${this.props.name}]`, e, info.componentStack);
  }
  render() {
    if (this.state.error)
      return (
        <div className="tg-empty">
          <strong>{t("{name} se cerró por un error", { name: this.props.name })}</strong>
          <p>{tr(this.state.error.message)}</p>
        </div>
      );
    return this.props.children;
  }
}
