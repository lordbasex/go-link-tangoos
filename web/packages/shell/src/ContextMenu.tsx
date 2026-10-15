import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Check } from "lucide-react";
import { createStore } from "./store";
import type { MenuItem } from "./store";

/** Lista de menú reutilizada por la MenuBar y los menús contextuales. Teclado completo. */
export function MenuList({ items, onClose, autoFocus = true }: { items: MenuItem[]; onClose: () => void; autoFocus?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState(-1);
  const enabled = items.map((it, i) => (!it.separator && !it.disabled ? i : -1)).filter((i) => i >= 0);

  useEffect(() => {
    if (autoFocus) ref.current?.focus();
  }, [autoFocus]);

  const onKey = (e: React.KeyboardEvent) => {
    const pos = enabled.indexOf(active);
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive(enabled[(pos + 1) % enabled.length]);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive(enabled[(pos - 1 + enabled.length) % enabled.length]);
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      const it = items[active];
      if (it && !it.disabled) {
        onClose();
        it.action?.();
      }
    } else if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    }
  };

  return (
    <div className="tg-menu" role="menu" tabIndex={-1} ref={ref} onKeyDown={onKey}>
      {items.map((it, i) =>
        it.separator ? (
          <div key={i} className="tg-menu-sep" role="separator" />
        ) : (
          <div
            key={i}
            role="menuitem"
            aria-disabled={it.disabled || undefined}
            className="tg-menu-item"
            data-active={i === active || undefined}
            onMouseEnter={() => setActive(it.disabled ? -1 : i)}
            onMouseLeave={() => setActive(-1)}
            onMouseUp={() => {
              if (it.disabled) return;
              onClose();
              it.action?.();
            }}
          >
            <span className="tg-menu-check">{it.checked && <Check size={12} strokeWidth={2.6} />}</span>
            <span className="tg-menu-label">{it.label}</span>
            {it.shortcut && <span className="tg-menu-shortcut">{it.shortcut}</span>}
          </div>
        ),
      )}
    </div>
  );
}

const ctx = createStore<{ x: number; y: number; items: MenuItem[] } | null>(null);

export function showContextMenu(x: number, y: number, items: MenuItem[]) {
  ctx.set({ x, y, items });
}

export function ContextMenuHost() {
  const st = ctx.use();
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ x: 0, y: 0 });

  useLayoutEffect(() => {
    if (!st || !ref.current) return;
    const r = ref.current.getBoundingClientRect();
    setPos({ x: Math.min(st.x, innerWidth - r.width - 6), y: Math.min(st.y, innerHeight - r.height - 6) });
  }, [st]);

  useEffect(() => {
    if (!st) return;
    const close = (e: Event) => {
      if (ref.current?.contains(e.target as Node)) return;
      ctx.set(null);
    };
    addEventListener("pointerdown", close, true);
    addEventListener("blur", close);
    return () => {
      removeEventListener("pointerdown", close, true);
      removeEventListener("blur", close);
    };
  }, [st]);

  if (!st) return null;
  return (
    <div ref={ref} className="tg-popover tg-ctx" style={{ left: pos.x || st.x, top: pos.y || st.y }}>
      <MenuList items={st.items} onClose={() => ctx.set(null)} />
    </div>
  );
}
