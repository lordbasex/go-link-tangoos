// Mission Control (Alt+↑ en la web, F3 en la app nativa): todas las ventanas a la vista.
// Las ventanas reales se transforman con CSS (sin re-render); encima va una capa con títulos
// y zonas de clic. Esc o clic en el vacío cierra.
import { useEffect, useLayoutEffect, useState } from "react";
import { closeOverlay, focusWin, registry, restoreWin, wm, workArea } from "./store";
import { missionLayout } from "./missionLayout";
import type { Placed } from "./missionLayout";

const EASE = "transform 320ms cubic-bezier(.2,.8,.2,1)";

export function MissionControl() {
  const [placed, setPlaced] = useState<Placed[]>([]);
  const { windows } = wm.get();
  const minimized = windows.filter((w) => w.minimized);

  useLayoutEffect(() => {
    const vis = wm.get().windows.filter((w) => !w.minimized && w.phase !== "closing");
    const a = workArea();
    const area = { x: a.x + 8, y: a.y + 8, w: a.w - 16, h: a.h - (minimized.length ? 110 : 16) };
    const p = missionLayout(vis, area);
    const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
    for (const it of p) {
      const w = vis.find((x) => x.id === it.id)!;
      const el = document.querySelector<HTMLElement>(`.tg-window[data-win="${it.id}"]`);
      if (!el) continue;
      el.style.transformOrigin = "0 0";
      el.style.transition = reduce ? "" : EASE;
      el.style.transform = `translate(${it.to.x - w.x}px, ${it.to.y - w.y}px) scale(${it.scale})`;
      el.dataset.mission = "";
    }
    setPlaced(p);
    return () => {
      for (const it of p) {
        const el = document.querySelector<HTMLElement>(`.tg-window[data-win="${it.id}"]`);
        if (!el) continue;
        el.style.transform = "";
        delete el.dataset.mission;
        setTimeout(() => {
          el.style.transition = "";
          el.style.transformOrigin = "";
        }, 340);
      }
    };
    // Se calcula una vez al abrir: es una "foto" del escritorio.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        closeOverlay();
      }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, []);

  const pick = (id: string) => {
    closeOverlay();
    focusWin(id);
  };

  return (
    <div className="tg-mission" onMouseDown={(e) => e.target === e.currentTarget && closeOverlay()} role="dialog" aria-label="Todas las ventanas">
      {placed.map((p) => {
        const w = windows.find((x) => x.id === p.id);
        if (!w) return null;
        const app = registry.get(w.app);
        return (
          <button key={p.id} type="button" className="tg-mission-hit" style={{ left: p.to.x, top: p.to.y, width: p.to.w, height: p.to.h }} onClick={() => pick(p.id)} aria-label={w.title}>
            <span className="tg-mission-label" style={{ top: p.to.h + 8 }}>
              {app && <app.icon size={18} />}
              {w.title}
            </span>
          </button>
        );
      })}
      {placed.length === 0 && <div className="tg-mission-empty">No hay ventanas abiertas.</div>}
      {minimized.length > 0 && (
        <div className="tg-mission-min" aria-label="Ventanas minimizadas">
          {minimized.map((w) => {
            const app = registry.get(w.app);
            return (
              <button
                key={w.id}
                type="button"
                onClick={() => {
                  closeOverlay();
                  restoreWin(w.id);
                }}
              >
                {app && <app.icon size={36} />}
                <span>{w.title}</span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
