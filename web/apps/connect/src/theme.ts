// Oscuro (predeterminado) o claro, igual que desk.go-link.org. public/theme.js aplica lo guardado antes de pintar.
import { useSyncExternalStore } from "react";

export type Theme = "dark" | "light";
const KEY = "tangoos-connect.theme";
const COLOR: Record<Theme, string> = { dark: "#0e1016", light: "#f3f4f7" };
const listeners = new Set<() => void>();

export function getTheme(): Theme {
  return typeof document !== "undefined" && document.documentElement.dataset.theme === "light" ? "light" : "dark";
}

export function setTheme(t: Theme) {
  const root = document.documentElement;
  if (t === "light") root.dataset.theme = "light";
  else delete root.dataset.theme;
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", COLOR[t]);
  try {
    localStorage.setItem(KEY, t);
  } catch {
    /* almacenamiento bloqueado */
  }
  listeners.forEach((f) => f());
}

export function useTheme(): Theme {
  return useSyncExternalStore(
    (f) => {
      listeners.add(f);
      return () => listeners.delete(f);
    },
    getTheme,
    () => "dark",
  );
}
