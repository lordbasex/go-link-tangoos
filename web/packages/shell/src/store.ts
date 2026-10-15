import { useSyncExternalStore } from "react";
import type { ComponentType, LazyExoticComponent } from "react";

// ---- Store mínimo (sin librería, como go-link) -----------------------------------

export function createStore<T>(initial: T) {
  let state = initial;
  const subs = new Set<() => void>();
  const get = () => state;
  const set = (next: T | ((s: T) => T)) => {
    state = typeof next === "function" ? (next as (s: T) => T)(state) : next;
    subs.forEach((f) => f());
  };
  const subscribe = (f: () => void) => {
    subs.add(f);
    return () => subs.delete(f);
  };
  function use(): T;
  function use<S>(sel: (s: T) => S): S;
  function use<S>(sel?: (s: T) => S) {
    return useSyncExternalStore(subscribe, () => (sel ? sel(state) : state));
  }
  return { get, set, subscribe, use };
}

// ---- Tipos del contrato de app (docs/05) -------------------------------------------

export interface MenuItem {
  label?: string;
  shortcut?: string;
  action?: () => void;
  disabled?: boolean;
  checked?: boolean;
  separator?: boolean;
}

export interface Menu {
  label: string;
  items: MenuItem[];
}

export interface WindowHandle {
  id: string;
  args: Record<string, unknown>;
  focused: boolean;
  setTitle: (t: string) => void;
  setMenus: (m: Menu[]) => void;
  close: () => void;
}

export interface AppProps {
  win: WindowHandle;
}

export interface AppManifest {
  id: string;
  name: string;
  icon: ComponentType<{ size?: number }>;
  window: { width: number; height: number; minWidth?: number; minHeight?: number };
  component: LazyExoticComponent<ComponentType<AppProps>> | ComponentType<AppProps>;
  singleInstance?: boolean;
  /** Ventana sin toolbar unificada (barra de título de 32 px). */
  plainTitlebar?: boolean;
  keywords?: string[];
  pinned?: boolean;
  /** No aparece en el Launcher ni en Spotlight (ventanas que abre otra app, p. ej. tango.appview). */
  hidden?: boolean;
}

// ---- Ventanas -------------------------------------------------------------------

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface WinState extends Rect {
  id: string;
  app: string;
  title: string;
  z: number;
  minimized: boolean;
  maximized: boolean;
  restore?: Rect;
  args: Record<string, unknown>;
  menus: Menu[];
  phase: "opening" | "open" | "closing" | "minimizing" | "restoring";
}

interface WM {
  windows: WinState[];
  focused: string | null;
  zTop: number;
}

export const wm = createStore<WM>({ windows: [], focused: null, zTop: 10 });
export const registry = new Map<string, AppManifest>();

export function registerApps(apps: AppManifest[]) {
  apps.forEach((a) => registry.set(a.id, a));
}

const MENUBAR = 28;
const DOCK_RESERVE = 92;

export function workArea(): Rect {
  return { x: 0, y: MENUBAR, w: window.innerWidth, h: window.innerHeight - MENUBAR - DOCK_RESERVE };
}

let counter = 0;

export function openApp(appId: string, args: Record<string, unknown> = {}) {
  const app = registry.get(appId);
  if (!app) return;
  const s = wm.get();
  const existing = s.windows.filter((w) => w.app === appId);
  if (app.singleInstance && existing.length) {
    const w = existing[existing.length - 1];
    if (w.minimized) restoreWin(w.id);
    else focusWin(w.id);
    if (Object.keys(args).length) patch(w.id, { args: { ...w.args, ...args } });
    return w.id;
  }
  const area = workArea();
  const compact = area.w < 1024;
  const w = compact ? area.w : Math.min(app.window.width, area.w - 40);
  const h = compact ? area.h : Math.min(app.window.height, area.h - 20);
  const cascade = (s.windows.length % 6) * 28;
  const x = compact ? 0 : Math.max(10, Math.round((area.w - w) / 2 - 60 + cascade));
  const y = compact ? area.y : Math.max(area.y + 8, Math.round(area.y + (area.h - h) / 2 - 20 + cascade));
  const id = `w${++counter}`;
  const z = s.zTop + 1;
  wm.set({
    windows: [...s.windows, { id, app: appId, title: app.name, x, y, w, h, z, minimized: false, maximized: compact, args, menus: [], phase: "opening" }],
    focused: id,
    zTop: z,
  });
  setTimeout(() => patch(id, { phase: "open" }), 220);
  return id;
}

export function patch(id: string, p: Partial<WinState>) {
  wm.set((s) => ({ ...s, windows: s.windows.map((w) => (w.id === id ? { ...w, ...p } : w)) }));
}

export function focusWin(id: string) {
  const s = wm.get();
  const w = s.windows.find((x) => x.id === id);
  if (!w) return;
  if (s.focused === id && w.z === s.zTop) return;
  const z = s.zTop + 1;
  wm.set({ ...s, focused: id, zTop: z, windows: s.windows.map((x) => (x.id === id ? { ...x, z } : x)) });
}

function focusTopmost(except?: string) {
  const s = wm.get();
  const top = s.windows.filter((w) => !w.minimized && w.id !== except && w.phase !== "closing").sort((a, b) => b.z - a.z)[0];
  wm.set({ ...s, focused: top?.id ?? null });
}

export function closeWin(id: string) {
  patch(id, { phase: "closing" });
  focusTopmost(id);
  setTimeout(() => wm.set((s) => ({ ...s, windows: s.windows.filter((w) => w.id !== id) })), 160);
}

export function minimizeWin(id: string) {
  patch(id, { phase: "minimizing" });
  focusTopmost(id);
  setTimeout(() => patch(id, { minimized: true, phase: "open" }), 330);
}

export function restoreWin(id: string) {
  patch(id, { minimized: false, phase: "restoring" });
  focusWin(id);
  setTimeout(() => patch(id, { phase: "open" }), 330);
}

export function toggleMaximize(id: string) {
  const w = wm.get().windows.find((x) => x.id === id);
  if (!w) return;
  if (w.maximized && w.restore) patch(id, { ...w.restore, maximized: false, restore: undefined });
  else {
    const a = workArea();
    patch(id, { x: a.x, y: a.y, w: a.w, h: a.h + DOCK_RESERVE - 6, maximized: true, restore: { x: w.x, y: w.y, w: w.w, h: w.h } });
  }
}

export type SnapZone = "left" | "right" | "max" | "tl" | "tr" | "bl" | "br";

export function snapRect(zone: SnapZone): Rect {
  const a = workArea();
  const h = a.h + DOCK_RESERVE - 6;
  const hw = Math.round(a.w / 2);
  const hh = Math.round(h / 2);
  switch (zone) {
    case "left": return { x: 0, y: a.y, w: hw, h };
    case "right": return { x: hw, y: a.y, w: a.w - hw, h };
    case "tl": return { x: 0, y: a.y, w: hw, h: hh };
    case "tr": return { x: hw, y: a.y, w: a.w - hw, h: hh };
    case "bl": return { x: 0, y: a.y + hh, w: hw, h: h - hh };
    case "br": return { x: hw, y: a.y + hh, w: a.w - hw, h: h - hh };
    default: return { x: 0, y: a.y, w: a.w, h };
  }
}

export function snapWin(id: string, zone: SnapZone) {
  const w = wm.get().windows.find((x) => x.id === id);
  if (!w) return;
  if (zone === "max") return toggleMaximize(id);
  patch(id, { ...snapRect(zone), maximized: false, restore: w.restore ?? { x: w.x, y: w.y, w: w.w, h: w.h } });
}

export function cycleWindows() {
  const s = wm.get();
  const vis = s.windows.filter((w) => !w.minimized).sort((a, b) => a.z - b.z);
  if (vis.length > 1) focusWin(vis[0].id);
}

// ---- Escritorio y preferencias -----------------------------------------------------

export interface Prefs {
  mode: "auto" | "light" | "dark";
  accent: string;
  wallpaper: "atardecer" | "noche" | "amanecer" | "dinamico";
  dockSize: number;
  magnify: boolean;
  widgets: boolean;
  dnd: boolean;
}

const PREFS_KEY = "tango.prefs.v1";
function loadPrefs(): Prefs {
  const def: Prefs = { mode: "auto", accent: "#2F7CF6", wallpaper: "atardecer", dockSize: 52, magnify: true, widgets: true, dnd: false };
  try {
    return { ...def, ...JSON.parse(localStorage.getItem(PREFS_KEY) ?? "{}") };
  } catch {
    return def;
  }
}

export const prefs = createStore<Prefs>(loadPrefs());
prefs.subscribe(() => {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(prefs.get()));
  } catch {
    /* almacenamiento bloqueado: las preferencias viven solo en memoria */
  }
});

export type Overlay = null | "launcher" | "spotlight" | "control" | "notifications" | "activity" | "user" | "tango-menu" | "mission";

interface UI {
  overlay: Overlay;
  session: "boot" | "resuming" | "setup" | "login" | "desktop" | "locked";
  user: { name: string; full: string; color: string } | null;
  snapPreview: Rect | null;
  openMenu: number | null; // índice del menú abierto en la MenuBar
}

// `?skip` entra directo al escritorio (QA y desarrollo).
const skip = typeof location !== "undefined" && new URLSearchParams(location.search).has("skip");
export const ui = createStore<UI>({
  overlay: null,
  session: skip ? "desktop" : "boot",
  user: skip ? { name: "federico", full: "Federico Pereira", color: "#2F7CF6" } : null,
  snapPreview: null,
  openMenu: null,
});

export const toggleOverlay = (o: Overlay) => ui.set((s) => ({ ...s, overlay: s.overlay === o ? null : o, openMenu: null }));
export const closeOverlay = () => ui.set((s) => (s.overlay || s.openMenu !== null ? { ...s, overlay: null, openMenu: null } : s));

// ---- Comandos (Spotlight) -------------------------------------------------------------

export interface Command {
  id: string;
  title: string;
  section: "Acciones" | "Ajustes";
  keywords?: string;
  run: () => void;
}
export const commands = createStore<Command[]>([]);
export function registerCommands(cs: Command[]) {
  commands.set((prev) => [...prev.filter((p) => !cs.some((c) => c.id === p.id)), ...cs]);
}

// ---- Toasts ---------------------------------------------------------------------

export interface Toast {
  id: string;
  level: "info" | "ok" | "warn" | "crit";
  title: string;
  body?: string;
  app?: string;
  action?: { label: string; run: () => void };
}
export const toasts = createStore<Toast[]>([]);
export function toast(t: Omit<Toast, "id">) {
  const id = Math.random().toString(36).slice(2);
  toasts.set((l) => [{ ...t, id }, ...l].slice(0, 4));
  if (t.level !== "crit") setTimeout(() => dismissToast(id), 6000);
}
export const dismissToast = (id: string) => toasts.set((l) => l.filter((t) => t.id !== id));

// ---- Energía (◆ → Reiniciar/Apagar) ------------------------------------------------------

export interface PowerState {
  kind: "reboot" | "shutdown";
  /** confirm: hoja de confirmación · waiting: esperando que el NAS vuelva · off: apagado */
  phase: "confirm" | "waiting" | "off";
}
export const power = createStore<PowerState | null>(null);
