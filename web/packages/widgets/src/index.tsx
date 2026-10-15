import "./widgets.css";
import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { KeyboardEvent as RKeyboardEvent, MouseEvent as RMouseEvent, PointerEvent as RPointerEvent } from "react";
import { Minus, Plus } from "lucide-react";
import { openApp, showContextMenu } from "@tango/shell";
import { catalog, sizeUnits } from "./widgets";
import type { WidgetDef } from "./widgets";

// ---- Layout persistido (orden + quitados) -------------------------------------------

interface Layout {
  order: string[];
  removed: string[];
}

const KEY = "tango.widgets.v1";
const defaults: Layout = { order: catalog.filter((w) => !w.optional).map((w) => w.id), removed: catalog.filter((w) => w.optional).map((w) => w.id) };
const byId = new Map(catalog.map((w) => [w.id, w]));

function loadLayout(): Layout {
  try {
    const raw = JSON.parse(localStorage.getItem(KEY) ?? "null") as Layout | null;
    if (!raw || !Array.isArray(raw.order)) return defaults;
    const removed = (raw.removed ?? []).filter((id) => byId.has(id));
    const order = raw.order.filter((id) => byId.has(id) && !removed.includes(id));
    // widgets nuevos del catálogo que el layout guardado no conoce
    for (const w of catalog) if (!order.includes(w.id) && !removed.includes(w.id)) (w.optional ? removed : order).push(w.id);
    return { order, removed };
  } catch {
    return defaults;
  }
}

function saveLayout(l: Layout) {
  try {
    localStorage.setItem(KEY, JSON.stringify(l));
  } catch {
    /* almacenamiento bloqueado: el orden vive solo en memoria */
  }
}

const reducedMotion = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

// ---- Columnas según el alto disponible ------------------------------------------------

const CELL = 170;
const GAP = 16;

function useColumns(units: number) {
  const calc = useCallback(() => {
    const avail = window.innerHeight - 28 - 14 - 100; // menubar, margen superior y Dock
    const rowsFit = Math.max(1, Math.floor((avail + GAP) / (CELL + GAP)));
    let cols = 2;
    while (Math.ceil(units / cols) > rowsFit && cols < 6) cols += 2;
    return cols;
  }, [units]);
  const [cols, setCols] = useState(calc);
  useEffect(() => {
    const on = () => setCols(calc());
    on();
    addEventListener("resize", on);
    return () => removeEventListener("resize", on);
  }, [calc]);
  return cols;
}

// ---- Raíz ----------------------------------------------------------------------------

export function Widgets() {
  const [layout, setLayout] = useState<Layout>(loadLayout);
  const [editing, setEditing] = useState(false);
  const [gallery, setGallery] = useState(false);
  const [dragId, setDragId] = useState<string | null>(null);
  const grid = useRef<HTMLDivElement>(null);
  const flipFrom = useRef<Map<string, DOMRect> | null>(null);
  const placeDragged = useRef<(() => void) | null>(null);

  const units = layout.order.reduce((a, id) => a + sizeUnits[byId.get(id)!.size], 0);
  const cols = useColumns(units + (editing ? 2 : 0));

  const update = useCallback((fn: (l: Layout) => Layout) => {
    setLayout((prev) => {
      const next = fn(prev);
      saveLayout(next);
      return next;
    });
  }, []);

  /** Guarda dónde estaban los widgets para animar el reacomodo (FLIP). */
  const recordFlip = (except?: string) => {
    const m = new Map<string, DOMRect>();
    grid.current?.querySelectorAll<HTMLElement>("[data-wid]").forEach((el) => {
      if (el.dataset.wid !== except) m.set(el.dataset.wid!, el.getBoundingClientRect());
    });
    flipFrom.current = m;
  };

  useLayoutEffect(() => {
    placeDragged.current?.();
    const from = flipFrom.current;
    flipFrom.current = null;
    if (!from || reducedMotion()) return;
    grid.current?.querySelectorAll<HTMLElement>("[data-wid]").forEach((el) => {
      const old = from.get(el.dataset.wid!);
      if (!old) return;
      const now = el.getBoundingClientRect();
      const dx = old.left - now.left;
      const dy = old.top - now.top;
      if (Math.abs(dx) < 1 && Math.abs(dy) < 1) return;
      el.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: "none" }], { duration: 240, easing: "cubic-bezier(.2,.8,.2,1)" });
    });
  }, [layout.order, cols]);

  const move = useCallback(
    (id: string, before: string) => {
      recordFlip(id);
      update((l) => {
        const order = l.order.filter((x) => x !== id);
        const from = l.order.indexOf(id);
        const to = l.order.indexOf(before);
        // al bajar se ubica después del destino, al subir antes
        const idx = order.indexOf(before) + (from < to ? 1 : 0);
        order.splice(idx, 0, id);
        return { ...l, order };
      });
    },
    [update],
  );

  const remove = useCallback(
    (id: string) => {
      recordFlip(id);
      update((l) => ({ order: l.order.filter((x) => x !== id), removed: [...l.removed, id] }));
    },
    [update],
  );

  const restore = (id: string) => {
    recordFlip();
    update((l) => ({ order: [...l.order, id], removed: l.removed.filter((x) => x !== id) }));
  };

  // Esc sale del modo edición; un clic fuera de los widgets también.
  useEffect(() => {
    if (!editing) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && (setEditing(false), setGallery(false));
    const onDown = (e: PointerEvent) => {
      if (!grid.current?.contains(e.target as Node) && !(e.target as HTMLElement).closest(".tg-popover")) {
        setEditing(false);
        setGallery(false);
      }
    };
    addEventListener("keydown", onKey);
    addEventListener("pointerdown", onDown, true);
    return () => {
      removeEventListener("keydown", onKey);
      removeEventListener("pointerdown", onDown, true);
    };
  }, [editing]);

  // ---- Arrastrar para reordenar: el widget sigue al puntero, el resto se acomoda ----
  const onPointerDown = useCallback(
    (id: string) => (e: RPointerEvent<HTMLDivElement>) => {
      if (e.button !== 0 || (e.target as HTMLElement).closest(".tw-remove")) return;
      const el = e.currentTarget;
      const g = grid.current!;
      const start = { x: e.clientX, y: e.clientY };
      const r = el.getBoundingClientRect();
      const grab = { x: e.clientX - r.left, y: e.clientY - r.top };
      const pos = { x: e.clientX, y: e.clientY };
      let dragging = false;
      let lastTarget: string | null = null;
      // Escuchamos en window y no con pointer capture: al reordenar, React puede mover
      // el nodo arrastrado en el DOM y eso suelta la captura.

      const place = () => {
        const gr = g.getBoundingClientRect();
        // offsetLeft/Top no incluyen transforms: es la celda real del grid
        el.style.transform = `translate(${pos.x - grab.x - (gr.left + el.offsetLeft)}px, ${pos.y - grab.y - (gr.top + el.offsetTop)}px) scale(1.03)`;
      };

      const onMove = (ev: PointerEvent) => {
        pos.x = ev.clientX;
        pos.y = ev.clientY;
        if (!dragging) {
          if (Math.hypot(ev.clientX - start.x, ev.clientY - start.y) < 5) return;
          dragging = true;
          placeDragged.current = place;
          setDragId(id);
        }
        place();
        let over: string | null = null;
        g.querySelectorAll<HTMLElement>("[data-wid]").forEach((n) => {
          if (n === el || over) return;
          const rr = n.getBoundingClientRect();
          if (ev.clientX >= rr.left && ev.clientX <= rr.right && ev.clientY >= rr.top && ev.clientY <= rr.bottom) over = n.dataset.wid!;
        });
        if (over && over !== lastTarget) move(id, over);
        lastTarget = over;
      };

      const onUp = () => {
        removeEventListener("pointermove", onMove);
        removeEventListener("pointerup", onUp);
        removeEventListener("pointercancel", onUp);
        placeDragged.current = null;
        if (!dragging) {
          if (!editing) {
            const app = byId.get(id)?.app;
            if (app) openApp(app.id);
          }
          return;
        }
        const from = el.style.transform;
        el.style.transform = "";
        if (!reducedMotion()) el.animate([{ transform: from }, { transform: "none" }], { duration: 220, easing: "cubic-bezier(.2,.8,.2,1)" });
        setDragId(null);
      };

      addEventListener("pointermove", onMove);
      addEventListener("pointerup", onUp);
      addEventListener("pointercancel", onUp);
    },
    [editing, move],
  );

  const onContextMenu = useCallback(
    (w: WidgetDef) => (e: RMouseEvent) => {
      e.preventDefault();
      e.stopPropagation();
      showContextMenu(e.clientX, e.clientY, [
        ...(w.app ? [{ label: `Abrir ${w.app.name}`, action: () => openApp(w.app!.id) }, { separator: true }] : []),
        { label: `Quitar «${w.name}»`, action: () => remove(w.id) },
        { label: editing ? "Terminar de editar" : "Editar widgets…", action: () => setEditing((v) => !v) },
      ]);
    },
    [editing, remove],
  );

  const onKey = useCallback(
    (w: WidgetDef) => (e: RKeyboardEvent) => {
      if ((e.key === "Enter" || e.key === " ") && w.app && !editing) {
        e.preventDefault();
        openApp(w.app.id);
      }
      if ((e.key === "Delete" || e.key === "Backspace") && editing) remove(w.id);
    },
    [editing, remove],
  );

  return (
    <div className="tg-widgets" ref={grid} data-editing={editing || undefined} style={{ gridTemplateColumns: `repeat(${cols}, ${CELL}px)` }} aria-label="Widgets">
      {editing && (
        <div className="tw-editbar">
          <button type="button" className="tw-pill" onClick={() => setGallery((g) => !g)} aria-expanded={gallery}>
            <Plus size={14} strokeWidth={2.6} /> Agregar widgets
          </button>
          <button type="button" className="tw-pill" data-primary onClick={() => (setEditing(false), setGallery(false))}>
            Listo
          </button>
          {gallery && (
            <div className="tw-gallery" role="dialog" aria-label="Agregar widgets">
              <div className="tw-gallery-title">Agregar widgets</div>
              {layout.removed.length === 0 && <div className="tw-gallery-empty">Todos los widgets ya están en el escritorio.</div>}
              {layout.removed.map((id) => {
                const w = byId.get(id)!;
                return (
                  <button key={id} type="button" className="tw-gallery-item" onClick={() => restore(id)}>
                    <span className="tw-gallery-icon">
                      <w.icon size={16} strokeWidth={2} />
                    </span>
                    <span>
                      {w.name}
                      <small>{w.description}</small>
                    </span>
                    <Plus size={16} strokeWidth={2.4} className="tw-gallery-add" />
                  </button>
                );
              })}
            </div>
          )}
        </div>
      )}
      {layout.order.map((id) => {
        const w = byId.get(id)!;
        return (
          <WidgetFrame
            key={id}
            def={w}
            dragging={dragId === id}
            editing={editing}
            onPointerDown={onPointerDown(id)}
            onContextMenu={onContextMenu(w)}
            onKeyDown={onKey(w)}
            onRemove={remove}
          />
        );
      })}
    </div>
  );
}

// ---- Marco de cada widget: aísla los re-render de las métricas (4 Hz) ----------------

const Body = memo(function Body({ def }: { def: WidgetDef }) {
  return <def.Component />;
});

const WidgetFrame = memo(function WidgetFrame({
  def,
  dragging,
  editing,
  onPointerDown,
  onContextMenu,
  onKeyDown,
  onRemove,
}: {
  def: WidgetDef;
  dragging: boolean;
  editing: boolean;
  onPointerDown: (e: RPointerEvent<HTMLDivElement>) => void;
  onContextMenu: (e: RMouseEvent) => void;
  onKeyDown: (e: RKeyboardEvent) => void;
  onRemove: (id: string) => void;
}) {
  return (
    <div
      className={`tw-w${def.className ? ` ${def.className}` : ""}`}
      data-wid={def.id}
      data-size={def.size}
      data-dragging={dragging || undefined}
      data-clickable={def.app && !editing ? true : undefined}
      role={def.app && !editing ? "button" : "group"}
      tabIndex={0}
      aria-label={def.app && !editing ? `${def.name}: abrir ${def.app.name}` : def.name}
      onPointerDown={onPointerDown}
      onContextMenu={onContextMenu}
      onKeyDown={onKeyDown}
    >
      {editing && (
        <button type="button" className="tw-remove" aria-label={`Quitar ${def.name}`} onClick={() => onRemove(def.id)}>
          <Minus size={13} strokeWidth={3} />
        </button>
      )}
      <Body def={def} />
    </div>
  );
});
