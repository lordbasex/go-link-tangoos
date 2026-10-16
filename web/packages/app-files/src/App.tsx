import "./files.css";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { DragEvent as RDragEvent, KeyboardEvent as RKeyboardEvent, MouseEvent as RMouseEvent, PointerEvent as RPointerEvent } from "react";
import {
  ChevronLeft, ChevronRight, Columns3, Download, File, FileArchive, FileCode, FileSpreadsheet, FileText, Film, Folder, FolderOpen, FolderPlus, History, Home, Image as ImageIcon, LayoutGrid, Link2, List, Music, SearchX, Trash2, Upload,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { fmtAgo, fmtBytes, fmtDate, locale, t, tn, useIsReal, useLive, useTransport } from "@tango/client";
import type { Evt, FileEntry, FileKind, FileVersion, TrashEntry } from "@tango/client";
import { WindowToolbar, openApp, showContextMenu, toast, useWindowMenus } from "@tango/shell";
import type { AppProps, MenuItem } from "@tango/shell";
import { Badge, Button, DemoBadge, EmptyState, IconButton, Progress, SearchField, Segmented, Sheet, SidebarItem } from "@tango/ui";
import { ShareLinkSheet, SharedLinksSheet } from "./share";
import { DocIcon, Thumb, kindLabel } from "./thumbs";
import { QuickLook, previewable } from "./QuickLook";
import { RealThumb, SnapshotCtx } from "./realthumb";
import { TransfersPanel, useTransfers } from "./xfers";
import { errText } from "./errors";

const HOME = "/homes/federico";
const DOWNLOADS = "/homes/federico/Descargas";
const TRASH = "trash";
const SIDE = 200;

type View = "icons" | "list" | "columns";
type SortKey = "name" | "mtime" | "size" | "kind";

const join = (dir: string, name: string) => (dir === "/" ? `/${name}` : `${dir}/${name}`);
const parentOf = (p: string) => p.slice(0, p.lastIndexOf("/")) || "/";
const baseName = (p: string) => (p === "/" ? t("Carpetas compartidas") : p === TRASH ? t("Papelera") : p.slice(p.lastIndexOf("/") + 1));

const kindIcon: Record<FileKind, [LucideIcon, string]> = {
  dir: [Folder, "#3b8cf5"], image: [ImageIcon, "#ec4899"], video: [Film, "#8b5cf6"], audio: [Music, "#f97316"], pdf: [FileText, "#e5484d"], doc: [FileText, "#2f7cf6"],
  sheet: [FileSpreadsheet, "#1fa463"], code: [FileCode, "#5b5f6b"], archive: [FileArchive, "#a16207"], text: [FileText, "#6b7280"], other: [File, "#8e8e96"],
};

function MiniIcon({ kind, tint }: { kind: FileKind; tint?: string }) {
  const [I, c] = kindIcon[kind];
  return <I size={16} strokeWidth={1.8} color={tint ?? c} style={{ flex: "none", fill: kind === "dir" ? `color-mix(in srgb, ${tint ?? c} 30%, transparent)` : "none" }} />;
}

const collator = new Intl.Collator(locale(), { numeric: true, sensitivity: "base" });

function sortEntries(list: FileEntry[], key: SortKey, dir: 1 | -1) {
  return [...list].sort((a, b) => {
    if ((a.kind === "dir") !== (b.kind === "dir")) return a.kind === "dir" ? -1 : 1;
    let c = 0;
    if (key === "name") c = collator.compare(a.name, b.name);
    else if (key === "mtime") c = a.mtime.localeCompare(b.mtime);
    else if (key === "size") c = a.size - b.size;
    else c = collator.compare(t(kindLabel[a.kind]), t(kindLabel[b.kind])) || collator.compare(a.name, b.name);
    return c * dir;
  });
}

function validateName(name: string, siblings: FileEntry[], self?: string): string | null {
  const n = name.trim();
  if (!n) return t("El nombre no puede quedar vacío.");
  if (n.includes("/")) return t("El nombre no puede tener «/».");
  if (n === "." || n === "..") return t("Ese nombre está reservado.");
  if (siblings.some((s) => s.path !== self && s.name.toLowerCase() === n.toLowerCase())) return t("Ya existe «{name}» en esta carpeta.", { name: n });
  return null;
}

export default function FilesApp({ win }: AppProps) {
  const tp = useTransport();
  // Datos reales (protocol §10): la raíz son las carpetas compartidas, no hay "Inicio" de usuario.
  const real = useIsReal("files.list");
  const initialPath = typeof win.args.path === "string" ? (win.args.path as string) : tp.kind === "mock" ? HOME : "/";
  const [hist, setHist] = useState({ stack: [initialPath], i: 0 });
  const path = hist.stack[hist.i];
  const [view, setView] = useState<View>("icons");
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: "name", dir: 1 });
  const [q, setQ] = useState("");
  const [results, setResults] = useState<FileEntry[] | null>(null);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [anchor, setAnchor] = useState<number | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [ql, setQl] = useState<string | null>(null);
  const [versionsOf, setVersionsOf] = useState<FileEntry | null>(null);
  const [shareOf, setShareOf] = useState<FileEntry | null>(null);
  const [showLinks, setShowLinks] = useState(false);
  const [confirmEmpty, setConfirmEmpty] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const dragDepth = useRef(0);
  const fileInput = useRef<HTMLInputElement>(null);
  const surface = useRef<HTMLDivElement>(null);
  const pendingSelect = useRef<string | null>(typeof win.args.select === "string" ? (win.args.select as string) : null);
  // Explorar una instantánea (§13): la misma vista, de solo lectura, con files.list/thumb/download.begin + snapshot.
  const snap = typeof win.args.snapshot === "string" ? (win.args.snapshot as string) : "";
  const snapAt = typeof win.args.snapshotAt === "string" ? (win.args.snapshotAt as string) : "";
  const roSnap = () => (toast({ level: "info", title: t("La instantánea es de solo lectura"), body: t("Para recuperar algo, elegilo y tocá «Restaurar una copia».") }), true);

  const isTrash = path === TRASH;
  const listing = useLive("files.list", snap ? { path, snapshot: snap } : { path: isTrash ? "/" : path }, snap ? [] : ["files.changed"], (e: Evt) => (e.data as { dir: string }).dir === path);
  const shares = useLive("shares.list", undefined, ["shares.changed"]).data ?? [];
  const vols = useLive("storage.volumes.list", undefined, ["storage.changed"]).data ?? [];
  // Papelera del lado del NAS (/<Carpeta>/.tango-trash): sobrevive a recargas y es la misma para todas las sesiones.
  const trashLive = useLive("files.trash.list", {}, ["files.changed"]);
  const trashEntries: TrashEntry[] = trashLive.data ?? [];
  const trash: FileEntry[] = useMemo(() => trashEntries.map((e) => ({ name: e.name, path: e.path, kind: e.kind, size: e.size, mtime: e.deletedAt })), [trashEntries]);
  const originOf = useMemo(() => new Map(trashEntries.map((e) => [e.path, e.origin])), [trashEntries]);
  const xf = useTransfers(tp);
  const shareColor = useMemo(() => new Map(shares.map((s) => [s.name, s.color])), [shares]);
  const free = vols.reduce((a, v) => a + (v.size - v.used), 0);

  // ---- búsqueda (debounce) -----------------------------------------------------------
  useEffect(() => {
    const term = q.trim();
    if (term.length < 2) {
      setResults(null);
      return;
    }
    const id = setTimeout(() => tp.call("files.search", { q: term }).then(setResults, () => setResults([])), 160);
    return () => clearTimeout(id);
  }, [q, tp]);

  const raw: FileEntry[] = results ?? (isTrash ? trash : (listing.data ?? []));
  const items = useMemo(() => sortEntries(raw, sort.key, sort.dir), [raw, sort]);
  const notFound = !isTrash && !results && listing.error?.message;

  // Navegación
  const navigate = useCallback((p: string) => {
    setHist((h) => (h.stack[h.i] === p ? h : { stack: [...h.stack.slice(0, h.i + 1), p], i: h.i + 1 }));
    setSel(new Set());
    setAnchor(null);
    setQ("");
    setRenaming(null);
  }, []);
  const canBack = hist.i > 0;
  const canFwd = hist.i < hist.stack.length - 1;
  const back = () => canBack && (setHist((h) => ({ ...h, i: h.i - 1 })), setSel(new Set()));
  const fwd = () => canFwd && (setHist((h) => ({ ...h, i: h.i + 1 })), setSel(new Set()));

  useEffect(() => win.setTitle(results ? t("Resultados para «{q}»", { q: q.trim() }) : baseName(path)), [path, results, q, win]);

  // Preselección pedida por Spotlight
  useEffect(() => {
    const p = pendingSelect.current;
    if (!p || !listing.data) return;
    const i = items.findIndex((e) => e.path === p);
    if (i >= 0) {
      setSel(new Set([p]));
      setAnchor(i);
      pendingSelect.current = null;
      requestAnimationFrame(() => surface.current?.querySelector(`[data-path="${CSS.escape(p)}"]`)?.scrollIntoView({ block: "nearest" }));
    }
  }, [listing.data, items]);

  // ---- acciones ---------------------------------------------------------------------
  const open = (e: FileEntry) => {
    if (e.kind === "dir") navigate(e.path);
    else if (real && !previewable(e)) download(e);
    else setQl(e.path);
  };

  const selected = items.filter((e) => sel.has(e.path));

  const newFolder = async () => {
    if (snap && roSnap()) return;
    if (isTrash || results || path === "/") return;
    const base = t("Carpeta sin título");
    let name = base;
    for (let n = 2; items.some((e) => e.name === name); n++) name = `${base} ${n}`;
    try {
      const e = await tp.call("files.mkdir", { path: join(path, name) });
      setSel(new Set([e.path]));
      setRenaming(e.path);
    } catch (x) {
      toast({ level: "crit", title: t("No se pudo crear la carpeta"), body: errText(x, { path }) });
    }
  };

  /** Restaura desde la papelera (`paths` son rutas de la papelera, opacas). */
  const restore = async (paths: string[]) => {
    if (!paths.length) return;
    const first = trashEntries.find((e) => e.path === paths[0]);
    try {
      const r = await tp.call("files.restore", { paths });
      setSel(new Set());
      toast({ level: "ok", title: r.count === 1 ? (first ? t("Se restauró «{name}»", { name: first.name }) : t("Se restauró «el ítem»")) : t("Se restauraron {n} ítems", { n: r.count }), body: first ? t("De vuelta en {place}.", { place: baseName(first.origin) }) : undefined });
    } catch (x) {
      toast({ level: "crit", title: t("No se pudo restaurar"), body: errText(x, { path: first?.origin, name: first?.name }) });
    }
  };

  const moveToTrash = async (entries: FileEntry[]) => {
    if (snap && roSnap()) return;
    if (!entries.length || isTrash) return;
    try {
      const r = await tp.call("files.trash", { paths: entries.map((e) => e.path) });
      setSel(new Set());
      // En el NAS la papelera tiene otra ruta: para deshacer se buscan los ítems recién borrados por origen y nombre.
      const undo = async () => {
        const now = await tp.call("files.trash.list", {});
        const paths = entries
          .map((e) => now.filter((x) => x.origin === parentOf(e.path) && x.name === e.name).sort((a, b) => b.deletedAt.localeCompare(a.deletedAt))[0]?.path)
          .filter((p): p is string => !!p);
        await restore(paths);
      };
      toast({
        level: "info",
        title: t("Se movió a la Papelera"),
        body: r.count === 1 ? `«${entries[0].name}»` : t("{n} ítems", { n: r.count }),
        app: "tango.files",
        action: { label: t("Deshacer"), run: () => void undo() },
      });
    } catch (x) {
      toast({ level: "crit", title: t("No se pudo mover a la Papelera"), body: errText(x, { path: entries[0].path, name: entries[0].name }) });
    }
  };

  const emptyTrash = async () => {
    setConfirmEmpty(false);
    const shares = [...new Set(trashEntries.map((e) => e.origin.split("/").filter(Boolean)[0]).filter(Boolean))];
    let n = 0;
    try {
      for (const share of shares) n += (await tp.call("files.trash.empty", { share })).count;
      setSel(new Set());
      toast({ level: "ok", title: t("Papelera vaciada"), body: tn(n, "Se borró 1 ítem para siempre.", "Se borraron {n} ítems para siempre.") });
    } catch (x) {
      toast({ level: "crit", title: t("No se pudo vaciar la papelera"), body: errText(x) });
    }
  };

  const upload = (files: FileList | File[]) => {
    if (snap && roSnap()) return;
    if (isTrash || results || path === "/") {
      toast({ level: "warn", title: t("Elegí una carpeta para subir"), body: t("Abrí una carpeta compartida y volvé a intentarlo.") });
      return;
    }
    const list = [...files];
    if (list.length) xf.upload(path, list);
  };

  const download = (e: FileEntry) => {
    if (e.kind === "dir") return;
    void xf.download(e, snap || undefined);
  };

  /** Copia un archivo de la instantánea a la carpeta en vivo (files.restoreVersion: no pisa el actual). */
  const restoreCopy = async (e: FileEntry) => {
    try {
      const r = await tp.call("files.restoreVersion", { path: e.path, snapshot: snap });
      toast({ level: "ok", title: t("Se restauró como «{name}»", { name: r.name }), body: t("Está en {place}. Lo que había quedó como estaba.", { place: parentOf(r.path) }), app: "tango.files" });
    } catch (x) {
      toast({ level: "crit", title: t("No se pudo restaurar"), body: errText(x, { path: e.path, name: e.name }) });
    }
  };

  const commitRename = async (entry: FileEntry, name: string): Promise<string | null> => {
    if (snap) return (setRenaming(null), roSnap(), null);
    const err = validateName(name, items, entry.path);
    if (err) return err;
    if (name.trim() === entry.name) {
      setRenaming(null);
      return null;
    }
    try {
      const e = await tp.call("files.rename", { path: entry.path, name: name.trim() });
      setRenaming(null);
      setSel(new Set([e.path]));
      return null;
    } catch (x) {
      return errText(x, { path: entry.path, name: name.trim() });
    }
  };

  // ---- selección -------------------------------------------------------------------
  const clickItem = (e: RMouseEvent, entry: FileEntry, i: number) => {
    surface.current?.focus({ preventScroll: true });
    if (e.shiftKey && anchor !== null) {
      const [a, b] = [Math.min(anchor, i), Math.max(anchor, i)];
      setSel(new Set(items.slice(a, b + 1).map((x) => x.path)));
    } else if (e.metaKey || e.ctrlKey) {
      setSel((s) => {
        const n = new Set(s);
        if (n.has(entry.path)) n.delete(entry.path);
        else n.add(entry.path);
        return n;
      });
      setAnchor(i);
    } else {
      if (!sel.has(entry.path) || sel.size > 1) setSel(new Set([entry.path]));
      setAnchor(i);
    }
  };

  const gridCols = () => {
    const els = surface.current?.querySelectorAll<HTMLElement>(".tf-cell");
    if (!els || !els.length) return 1;
    const top = els[0].offsetTop;
    let n = 0;
    for (const el of els) {
      if (el.offsetTop !== top) break;
      n++;
    }
    return Math.max(1, n);
  };

  const moveSel = (delta: number, extend: boolean) => {
    if (!items.length) return;
    const cur = anchor ?? (delta > 0 ? -1 : items.length);
    const next = Math.min(items.length - 1, Math.max(0, cur + delta));
    if (extend && anchor !== null) setSel((s) => new Set([...s, items[next].path]));
    else setSel(new Set([items[next].path]));
    setAnchor(next);
    requestAnimationFrame(() => surface.current?.querySelector(`[data-path="${CSS.escape(items[next].path)}"]`)?.scrollIntoView({ block: "nearest" }));
  };

  const onKey = (e: RKeyboardEvent) => {
    if (renaming || ql) return;
    const mod = e.metaKey || e.ctrlKey;
    const cols = view === "icons" ? gridCols() : 1;
    switch (e.key) {
      case "ArrowRight":
        if (view === "columns") {
          const d = selected[0];
          if (d?.kind === "dir") navigate(d.path);
        } else if (view === "icons") moveSel(1, e.shiftKey);
        else return;
        break;
      case "ArrowLeft":
        if (view === "columns") {
          if (path !== "/" && !isTrash) {
            const from = path;
            navigate(parentOf(path));
            setSel(new Set([from]));
          }
        } else if (view === "icons") moveSel(-1, e.shiftKey);
        else return;
        break;
      case "ArrowDown":
        if (mod) {
          if (selected[0]) open(selected[0]);
        } else moveSel(cols, e.shiftKey);
        break;
      case "ArrowUp":
        if (mod) {
          if (path !== "/" && !isTrash) navigate(parentOf(path));
        } else moveSel(-cols, e.shiftKey);
        break;
      case "Enter":
      case "F2":
        if (selected.length === 1 && !isTrash && path !== "/") setRenaming(selected[0].path);
        break;
      case " ":
        if (selected[0]) setQl(selected[0].path);
        break;
      case "Backspace":
      case "Delete":
        if (isTrash) void restore([...sel]);
        else void moveToTrash(selected);
        break;
      case "a":
        if (!mod) return;
        setSel(new Set(items.map((x) => x.path)));
        break;
      case "Escape":
        if (q) setQ("");
        else setSel(new Set());
        break;
      default:
        return;
    }
    e.preventDefault();
  };

  // ---- banda de selección (vista de íconos) -------------------------------------------
  const [band, setBand] = useState<{ x: number; y: number; w: number; h: number } | null>(null);
  const startBand = (e: RPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0 || view !== "icons" || (e.target as HTMLElement).closest(".tf-cell")) return;
    const host = e.currentTarget;
    const box = host.getBoundingClientRect();
    const sx = e.clientX - box.left + host.scrollLeft;
    const sy = e.clientY - box.top + host.scrollTop;
    const base = e.metaKey || e.ctrlKey || e.shiftKey ? new Set(sel) : new Set<string>();
    if (!base.size) setSel(new Set());
    host.setPointerCapture(e.pointerId);
    host.focus({ preventScroll: true });
    const cells = [...host.querySelectorAll<HTMLElement>(".tf-cell")];
    const move = (ev: PointerEvent) => {
      const cx = ev.clientX - box.left + host.scrollLeft;
      const cy = ev.clientY - box.top + host.scrollTop;
      const r = { x: Math.min(sx, cx), y: Math.min(sy, cy), w: Math.abs(cx - sx), h: Math.abs(cy - sy) };
      if (r.w + r.h < 4) return;
      setBand(r);
      const hit = new Set(base);
      for (const c of cells) {
        const cr = { x: c.offsetLeft, y: c.offsetTop, w: c.offsetWidth, h: c.offsetHeight };
        if (cr.x < r.x + r.w && cr.x + cr.w > r.x && cr.y < r.y + r.h && cr.y + cr.h > r.y) hit.add(c.dataset.path!);
      }
      setSel(hit);
    };
    const up = () => {
      host.removeEventListener("pointermove", move);
      host.removeEventListener("pointerup", up);
      setBand(null);
    };
    host.addEventListener("pointermove", move);
    host.addEventListener("pointerup", up);
  };

  // ---- drag & drop desde el sistema ---------------------------------------------------
  const isFileDrag = (e: RDragEvent) => [...e.dataTransfer.types].includes("Files");
  const dnd = {
    onDragEnter: (e: RDragEvent) => {
      if (!isFileDrag(e)) return;
      e.preventDefault();
      dragDepth.current++;
      setDragOver(true);
    },
    onDragOver: (e: RDragEvent) => {
      if (!isFileDrag(e)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "copy";
    },
    onDragLeave: () => {
      dragDepth.current = Math.max(0, dragDepth.current - 1);
      if (!dragDepth.current) setDragOver(false);
    },
    onDrop: (e: RDragEvent) => {
      if (!isFileDrag(e)) return;
      e.preventDefault();
      dragDepth.current = 0;
      setDragOver(false);
      upload(e.dataTransfer.files);
    },
  };

  // ---- menús contextuales -------------------------------------------------------------
  const sortItems = (): MenuItem[] =>
    (
      [
        ["name", "Ordenar por nombre"],
        ["mtime", "Ordenar por fecha de modificación"],
        ["size", "Ordenar por tamaño"],
        ["kind", "Ordenar por tipo"],
      ] as [SortKey, string][]
    ).map(([k, l]) => ({ label: t(l), checked: sort.key === k, action: () => setSort((s) => ({ key: k, dir: s.key === k ? (-s.dir as 1 | -1) : 1 })) }));

  const itemMenu = (e: RMouseEvent, entry: FileEntry, i: number) => {
    e.preventDefault();
    e.stopPropagation();
    let targets = selected;
    if (!sel.has(entry.path)) {
      setSel(new Set([entry.path]));
      setAnchor(i);
      targets = [entry];
    }
    const many = targets.length > 1;
    if (isTrash) {
      showContextMenu(e.clientX, e.clientY, [{ label: many ? t("Restaurar {n} ítems", { n: targets.length }) : t("Restaurar"), action: () => void restore(targets.map((x) => x.path)) }]);
      return;
    }
    showContextMenu(e.clientX, e.clientY, [
      { label: t("Abrir"), disabled: many, action: () => open(entry) },
      { label: t("Vista rápida"), shortcut: t("Espacio"), disabled: many, action: () => setQl(entry.path) },
      { separator: true },
      { label: t("Renombrar"), shortcut: "↩", disabled: many || path === "/", action: () => setRenaming(entry.path) },
      { label: t("Versiones anteriores…"), disabled: many || entry.kind === "dir", action: () => setVersionsOf(entry) },
      { label: t("Descargar"), disabled: many || entry.kind === "dir", action: () => download(entry) },
      { label: t("Compartir enlace…"), disabled: many, action: () => setShareOf(entry) },
      { separator: true },
      { label: many ? t("Mover {n} ítems a la Papelera", { n: targets.length }) : t("Mover a la Papelera"), shortcut: "⌫", disabled: path === "/", action: () => void moveToTrash(targets) },
    ]);
  };

  const backgroundMenu = (e: RMouseEvent) => {
    if ((e.target as HTMLElement).closest(".tf-cell,tr[data-path]")) return;
    e.preventDefault();
    if (isTrash) {
      showContextMenu(e.clientX, e.clientY, [
        { label: t("Restaurar todo"), disabled: !trash.length, action: () => void restore(trash.map((x) => x.path)) },
        { label: t("Vaciar la papelera…"), disabled: !trash.length, action: () => setConfirmEmpty(true) },
      ]);
      return;
    }
    const writable = path !== "/" && !results;
    showContextMenu(e.clientX, e.clientY, [
      { label: t("Nueva carpeta"), disabled: !writable, action: () => void newFolder() },
      { label: t("Subir archivos…"), disabled: !writable, action: () => fileInput.current?.click() },
      { separator: true },
      ...sortItems(),
      { separator: true },
      { label: t("Íconos"), checked: view === "icons", action: () => setView("icons") },
      { label: t("Lista"), checked: view === "list", action: () => setView("list") },
      { label: t("Columnas"), checked: view === "columns", action: () => setView("columns") },
    ]);
  };

  useWindowMenus(
    win,
    [
      {
        label: t("Archivo"),
        items: [
          { label: t("Nueva carpeta"), disabled: isTrash || path === "/", action: () => void newFolder() },
          { label: t("Subir archivos…"), disabled: isTrash || path === "/", action: () => fileInput.current?.click() },
          { separator: true },
          { label: t("Vista rápida"), shortcut: t("Espacio"), disabled: selected.length !== 1, action: () => selected[0] && setQl(selected[0].path) },
          { label: t("Versiones anteriores…"), disabled: selected.length !== 1 || selected[0]?.kind === "dir", action: () => selected[0] && setVersionsOf(selected[0]) },
          { label: t("Descargar"), disabled: selected.length !== 1 || selected[0]?.kind === "dir" || isTrash, action: () => selected[0] && download(selected[0]) },
          { separator: true },
          { label: t("Mover a la Papelera"), shortcut: "⌫", disabled: !selected.length || isTrash, action: () => void moveToTrash(selected) },
        ],
      },
      {
        label: t("Ver"),
        items: [
          { label: t("Como íconos"), checked: view === "icons", action: () => setView("icons") },
          { label: t("Como lista"), checked: view === "list", action: () => setView("list") },
          { label: t("Como columnas"), checked: view === "columns", action: () => setView("columns") },
          { separator: true },
          ...sortItems(),
        ],
      },
      {
        label: t("Ir"),
        items: [
          { label: t("Atrás"), disabled: !canBack, action: back },
          { label: t("Adelante"), disabled: !canFwd, action: fwd },
          { label: t("Carpeta superior"), disabled: path === "/" || isTrash, action: () => navigate(parentOf(path)) },
          { separator: true },
          ...(real
            ? []
            : [
                { label: t("Inicio"), action: () => navigate(HOME) },
                { label: t("Descargas"), action: () => navigate(DOWNLOADS) },
              ]),
          { label: t("Carpetas compartidas"), action: () => navigate("/") },
          { label: t("Papelera"), action: () => navigate(TRASH) },
        ],
      },
    ],
    [view, path, sel, canBack, canFwd, sort, isTrash, trash.length, real],
  );

  // ---- render -----------------------------------------------------------------------
  const tintFor = (e: FileEntry) => (e.kind === "dir" && parentOf(e.path) === "/" ? shareColor.get(e.name) : undefined);
  const qlIndex = ql ? items.findIndex((e) => e.path === ql) : -1;
  const selSize = selected.reduce((a, e) => a + e.size, 0);
  const crumbs = isTrash || results ? [] : path === "/" ? ["/"] : ["/", ...path.split("/").filter(Boolean).map((_, i, a) => "/" + a.slice(0, i + 1).join("/"))];
  const inShare = (p: string) => path === p || path.startsWith(p + "/");

  let content: React.ReactNode;
  if (notFound)
    content = (
      <EmptyState icon={SearchX} title={t("Esta carpeta ya no existe")} action={<Button onClick={() => navigate(real ? "/" : HOME)}>{real ? t("Ir a las carpetas compartidas") : t("Ir a Inicio")}</Button>}>
        {t("Puede que la hayan movido o renombrado desde otra sesión.")}
      </EmptyState>
    );
  else if (results && !items.length)
    content = (
      <EmptyState icon={SearchX} title={t("Sin resultados")}>
        {t("Nada coincide con «{q}». Probá con otra palabra o revisá la ortografía.", { q: q.trim() })}
      </EmptyState>
    );
  else if (isTrash && !items.length)
    content = (
      <EmptyState icon={Trash2} title={t("La papelera está vacía")}>
        {t("Lo que borres desde Archivos queda acá hasta que lo restaures o vacíes la papelera.")}
      </EmptyState>
    );
  else if (!listing.loading && !items.length && !results)
    content = (
      <EmptyState icon={FolderOpen} title={t("Esta carpeta está vacía")} action={path !== "/" ? <Button onClick={() => fileInput.current?.click()}>{t("Subir archivos")}</Button> : undefined}>
        {t("Arrastrá archivos acá o usá «Subir archivos».")}
      </EmptyState>
    );
  else if (view === "list")
    content = (
      <table className="tg-table tf-table">
        <thead>
          <tr>
            {(
              [
                ["name", "Nombre", ""],
                ["mtime", "Fecha de modificación", ""],
                ["size", "Tamaño", "num"],
                ["kind", "Tipo", ""],
              ] as [SortKey, string, string][]
            ).map(([k, l, cls]) => (
              <th key={k} className={cls} aria-sort={sort.key === k ? (sort.dir === 1 ? "ascending" : "descending") : undefined}>
                <button type="button" className="tf-th" onClick={() => setSort((s) => ({ key: k, dir: s.key === k ? (-s.dir as 1 | -1) : 1 }))}>
                  {t(l)}
                  {sort.key === k && <span className="tf-sort">{sort.dir === 1 ? "▲" : "▼"}</span>}
                </button>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {items.map((e, i) => (
            <tr key={e.path} data-path={e.path} aria-selected={sel.has(e.path)} onMouseDown={(ev) => ev.button === 0 && clickItem(ev, e, i)} onDoubleClick={() => open(e)} onContextMenu={(ev) => itemMenu(ev, e, i)}>
              <td>
                <span className="tf-name-cell">
                  <MiniIcon kind={e.kind} tint={tintFor(e)} />
                  {renaming === e.path ? <RenameField entry={e} onCommit={commitRename} onCancel={() => setRenaming(null)} /> : <span className="tf-ellipsis">{e.name}</span>}
                </span>
              </td>
              <td className="tf-dim">{fmtDate(e.mtime)}</td>
              <td className="num tf-dim">{e.kind === "dir" ? (e.size ? fmtBytes(e.size) : "—") : fmtBytes(e.size)}</td>
              <td className="tf-dim">{t(kindLabel[e.kind])}</td>
            </tr>
          ))}
        </tbody>
      </table>
    );
  else if (view === "columns" && !results && !isTrash)
    content = (
      <div className="tf-columns">
        {crumbs.map((dir, ci) => (
          <Column
            key={dir}
            dir={dir}
            active={ci === crumbs.length - 1}
            chosen={crumbs[ci + 1]}
            items={ci === crumbs.length - 1 ? items : undefined}
            sel={sel}
            tint={(e) => tintFor(e)}
            onPick={(ev, e, i) => {
              if (ci === crumbs.length - 1) {
                clickItem(ev, e, i);
                if (e.kind === "dir" && !ev.shiftKey && !ev.metaKey && !ev.ctrlKey) navigate(e.path);
              } else if (e.kind === "dir") navigate(e.path);
              else {
                navigate(dir);
                setSel(new Set([e.path]));
              }
            }}
            onOpen={open}
            onMenu={itemMenu}
            renaming={renaming}
            onCommit={commitRename}
            onCancel={() => setRenaming(null)}
          />
        ))}
        {selected.length === 1 && selected[0].kind !== "dir" && <Preview entry={selected[0]} real={real} onVersions={() => setVersionsOf(selected[0])} onDownload={() => download(selected[0])} />}
      </div>
    );
  else
    content = (
      <div className="tf-grid">
        {items.map((e, i) => (
          <div
            key={e.path}
            className="tf-cell"
            data-path={e.path}
            aria-selected={sel.has(e.path)}
            onMouseDown={(ev) => ev.button === 0 && clickItem(ev, e, i)}
            onDoubleClick={() => open(e)}
            onContextMenu={(ev) => itemMenu(ev, e, i)}
            title={results || isTrash ? (isTrash ? t("Estaba en {place}", { place: originOf.get(e.path) ?? "?" }) : e.path) : undefined}
          >
            <div className="tf-thumb">{real ? <RealThumb entry={e} tint={tintFor(e)} /> : <Thumb entry={e} tint={tintFor(e)} />}</div>
            {renaming === e.path ? (
              <RenameField entry={e} onCommit={commitRename} onCancel={() => setRenaming(null)} multiline />
            ) : (
              <div className="tf-label">
                <span>{e.name}</span>
              </div>
            )}
          </div>
        ))}
      </div>
    );

  return (
    <SnapshotCtx.Provider value={snap}>
    <div className="tg-app tf-app" style={{ ["--side" as string]: `${SIDE}px` }}>
      <aside className="tg-app-side tg-sidebar" aria-label={t("Lugares")}>
        <h4>{t("Favoritos")}</h4>
        {!real && <SidebarItem icon={Home} label={t("Inicio")} current={path === HOME} onClick={() => navigate(HOME)} />}
        {!real && <SidebarItem icon={Download} label={t("Descargas")} current={path === DOWNLOADS} onClick={() => navigate(DOWNLOADS)} />}
        <SidebarItem icon={FolderOpen} label={t("Todas las carpetas")} current={path === "/"} onClick={() => navigate("/")} />
        <h4>{t("Carpetas compartidas")}</h4>
        {shares.map((s) => (
          <SidebarItem key={s.id} icon={Folder} color={s.color} label={s.name} current={inShare(`/${s.name}`) && (real || !(s.name === "homes" && (path === HOME || path === DOWNLOADS)))} onClick={() => navigate(`/${s.name}`)} />
        ))}
        {!shares.length && <div className="tf-side-empty">{t("Todavía no hay carpetas compartidas. Creá un volumen en Almacenamiento.")}</div>}
        <h4>{t("Más")}</h4>
        <SidebarItem icon={Trash2} label={t("Papelera")} current={isTrash} meta={trash.length || undefined} onClick={() => navigate(TRASH)} />
        <SidebarItem icon={Link2} label={t("Enlaces compartidos")} current={false} onClick={() => setShowLinks(true)} />
      </aside>

      <WindowToolbar win={win} inset={SIDE}>
        <div className="tg-tb-group" data-no-drag>
          <IconButton icon={ChevronLeft} label={t("Atrás")} disabled={!canBack} onClick={back} />
          <IconButton icon={ChevronRight} label={t("Adelante")} disabled={!canFwd} onClick={fwd} />
        </div>
        <div className="tf-tb-title">
          <span className="tg-tb-title">{results ? t("Resultados") : baseName(path)}</span>
          {!results && !isTrash && path !== "/" && <span className="tg-tb-sub">{parentOf(path) === "/" ? t("Carpeta compartida") : parentOf(path)}</span>}
        </div>
        <div className="tg-tb-spacer" />
        <div className="tg-tb-group" data-no-drag>
          <Segmented<View>
            label={t("Vista")}
            value={view}
            onChange={setView}
            options={[
              { value: "icons", label: t("Íconos"), icon: LayoutGrid },
              { value: "list", label: t("Lista"), icon: List },
              { value: "columns", label: t("Columnas"), icon: Columns3 },
            ]}
          />
          <IconButton icon={FolderPlus} label={t("Nueva carpeta")} disabled={isTrash || !!results || path === "/"} onClick={() => void newFolder()} />
          <IconButton icon={Upload} label={t("Subir archivos")} disabled={isTrash || !!results || path === "/"} onClick={() => fileInput.current?.click()} />
        </div>
        <div className="tf-tb-search" data-no-drag>
          <SearchField value={q} onChange={setQ} placeholder={t("Buscar en el NAS")} />
        </div>
        {!real && <DemoBadge />}
      </WindowToolbar>

      <div className="tg-app-main">
        {snap && (
          <div className="tf-snapbar" role="status">
            <History size={14} />
            <span>
              {t("Instantánea del")} <b>{snapAt ? fmtDate(snapAt) : snap}</b> · {t("solo lectura")}
            </span>
            {selected.length === 1 && selected[0].kind !== "dir" && (
              <Button onClick={() => void restoreCopy(selected[0])}>{t("Restaurar una copia")}</Button>
            )}
            <Button variant="plain" onClick={() => openApp("tango.files", { path })}>
              {t("Ver la carpeta actual")}
            </Button>
          </div>
        )}
        <div
          className="tg-app-content tf-surface"
          ref={surface}
          tabIndex={0}
          role="listbox"
          aria-multiselectable="true"
          aria-label={baseName(path)}
          onKeyDown={onKey}
          onPointerDown={startBand}
          onContextMenu={backgroundMenu}
          data-view={view}
          {...dnd}
        >
          {content}
          {band && <div className="tf-band" style={{ left: band.x, top: band.y, width: band.w, height: band.h }} />}
          {dragOver && (
            <div className="tf-drop">
              <Upload size={30} />
              <strong>{t("Soltá para subir a {place}", { place: baseName(path) })}</strong>
            </div>
          )}
        </div>
        <TransfersPanel items={xf.items} onCancel={xf.cancel} onClear={xf.clearDone} />
        <footer className="tg-app-foot tf-foot">
          <nav className="tf-crumbs" aria-label={t("Ruta")}>
            {isTrash ? (
              <span>{t("Papelera")}</span>
            ) : results ? (
              <span>{t("Buscando en todas las carpetas")}</span>
            ) : (
              crumbs.map((c, i) => (
                <span key={c} className="tf-crumb">
                  {i > 0 && <ChevronRight size={11} />}
                  <button type="button" onClick={() => navigate(c)} disabled={i === crumbs.length - 1}>
                    <MiniIcon kind="dir" tint={i === 1 ? shareColor.get(baseName(c)) : undefined} />
                    {baseName(c)}
                  </button>
                </span>
              ))
            )}
          </nav>
          <div className="tg-tb-spacer" />
          <span className="tg-num">
            {tn(items.length, "1 ítem", "{n} ítems")}
            {sel.size > 0 && ` · ${tn(sel.size, "{n} seleccionado", "{n} seleccionados")}${selSize ? ` (${fmtBytes(selSize)})` : ""}`}
            {free > 0 && ` · ${t("{size} disponibles", { size: fmtBytes(free) })}`}
          </span>
        </footer>
      </div>

      <input ref={fileInput} type="file" multiple hidden onChange={(e) => (e.target.files && upload(e.target.files), (e.target.value = ""))} />

      {ql && qlIndex >= 0 && (
        <QuickLook
          entry={items[qlIndex]}
          index={qlIndex}
          total={items.length}
          onClose={() => setQl(null)}
          onPrev={() => {
            const i = (qlIndex - 1 + items.length) % items.length;
            setQl(items[i].path);
            setSel(new Set([items[i].path]));
            setAnchor(i);
          }}
          onNext={() => {
            const i = (qlIndex + 1) % items.length;
            setQl(items[i].path);
            setSel(new Set([items[i].path]));
            setAnchor(i);
          }}
          real={real}
          onDownload={() => download(items[qlIndex])}
        />
      )}

      {shareOf && <ShareLinkSheet entry={shareOf} onClose={() => setShareOf(null)} />}
      {showLinks && <SharedLinksSheet onClose={() => setShowLinks(false)} />}
      <VersionsSheet
        entry={versionsOf}
        onClose={() => setVersionsOf(null)}
        onRestored={(e) => {
          setSel(new Set([e.path]));
          pendingSelect.current = e.path;
        }}
      />
      <Sheet
        open={confirmEmpty}
        onClose={() => setConfirmEmpty(false)}
        width={420}
        footer={
          <>
            <Button onClick={() => setConfirmEmpty(false)}>{t("Cancelar")}</Button>
            <Button variant="danger" onClick={() => void emptyTrash()}>
              {t("Vaciar la papelera")}
            </Button>
          </>
        }
      >
        <div className="tf-ver-head">
          <Trash2 size={22} />
          <div>
            <strong>{t("¿Vaciar la papelera?")}</strong>
            <span>
              {tn(trash.length, "El ítem se borra para siempre del NAS. Las instantáneas que ya existían no cambian.", "Los {n} ítems se borran para siempre del NAS. Las instantáneas que ya existían no cambian.")}
            </span>
          </div>
        </div>
      </Sheet>
    </div>
    </SnapshotCtx.Provider>
  );
}

// ---- Renombrar en línea ---------------------------------------------------------------

function RenameField({ entry, onCommit, onCancel, multiline }: { entry: FileEntry; onCommit: (e: FileEntry, name: string) => Promise<string | null>; onCancel: () => void; multiline?: boolean }) {
  const [v, setV] = useState(entry.name);
  const [err, setErr] = useState<string | null>(null);
  const ref = useRef<HTMLInputElement>(null);
  const busy = useRef(false);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.focus();
    const dot = entry.kind === "dir" ? -1 : entry.name.lastIndexOf(".");
    el.setSelectionRange(0, dot > 0 ? dot : entry.name.length);
  }, [entry]);
  const commit = async () => {
    if (busy.current) return;
    busy.current = true;
    const e = await onCommit(entry, v);
    busy.current = false;
    setErr(e);
    if (e) ref.current?.focus();
  };
  return (
    <span className={`tf-rename${multiline ? " tf-rename-ml" : ""}`} onMouseDown={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
      <input
        ref={ref}
        value={v}
        spellCheck={false}
        aria-label={t("Nuevo nombre")}
        aria-invalid={!!err}
        onChange={(e) => (setV(e.target.value), setErr(null))}
        onKeyDown={(e) => {
          e.stopPropagation();
          if (e.key === "Enter") void commit();
          if (e.key === "Escape") onCancel();
        }}
        onBlur={() => {
          if (!err) void commit();
        }}
      />
      {err && <span className="tf-rename-err" role="alert">{err}</span>}
    </span>
  );
}

// ---- Vista de columnas ----------------------------------------------------------------

function Column({
  dir, active, chosen, items, sel, tint, onPick, onOpen, onMenu, renaming, onCommit, onCancel,
}: {
  dir: string;
  active: boolean;
  chosen?: string;
  items?: FileEntry[];
  sel: Set<string>;
  tint: (e: FileEntry) => string | undefined;
  onPick: (ev: RMouseEvent, e: FileEntry, i: number) => void;
  onOpen: (e: FileEntry) => void;
  onMenu: (ev: RMouseEvent, e: FileEntry, i: number) => void;
  renaming: string | null;
  onCommit: (e: FileEntry, name: string) => Promise<string | null>;
  onCancel: () => void;
}) {
  const own = useLive("files.list", { path: dir }, ["files.changed"], (e: Evt) => (e.data as { dir: string }).dir === dir);
  const list = items ?? sortEntries(own.data ?? [], "name", 1);
  return (
    <div className="tf-col" data-active={active || undefined}>
      {list.map((e, i) => {
        const on = active ? sel.has(e.path) : chosen === e.path;
        return (
          <div
            key={e.path}
            data-path={e.path}
            className="tf-col-row"
            aria-selected={on}
            data-dim={!active && on ? true : undefined}
            onMouseDown={(ev) => ev.button === 0 && onPick(ev, e, i)}
            onDoubleClick={() => onOpen(e)}
            onContextMenu={(ev) => onMenu(ev, e, i)}
          >
            <MiniIcon kind={e.kind} tint={tint(e)} />
            {renaming === e.path ? <RenameField entry={e} onCommit={onCommit} onCancel={onCancel} /> : <span className="tf-ellipsis">{e.name}</span>}
            {e.kind === "dir" && <ChevronRight size={12} className="tf-col-chev" />}
          </div>
        );
      })}
      {!list.length && !own.loading && <div className="tf-col-empty">{t("Vacía")}</div>}
    </div>
  );
}

function Preview({ entry, onVersions, onDownload, real }: { entry: FileEntry; onVersions: () => void; onDownload: () => void; real: boolean }) {
  return (
    <div className="tf-preview">
      <div className="tf-preview-art">
        {real ? <RealThumb entry={entry} size={130} /> : entry.kind === "image" || entry.kind === "video" || entry.kind === "audio" ? <Thumb entry={entry} size={150} /> : <DocIcon entry={entry} size={130} />}
      </div>
      <strong>{entry.name}</strong>
      <span className="tf-dim">
        {t(kindLabel[entry.kind])} · {fmtBytes(entry.size)}
      </span>
      <dl>
        <dt>{t("Modificado")}</dt>
        <dd>{fmtDate(entry.mtime)}</dd>
        <dt>{t("Ubicación")}</dt>
        <dd>{parentOf(entry.path)}</dd>
      </dl>
      <div className="tf-preview-actions">
        <Button onClick={onDownload}>
          <Download size={13} /> {t("Descargar")}
        </Button>
        <Button onClick={onVersions}>
          <History size={13} /> {t("Versiones")}
        </Button>
      </div>
    </div>
  );
}

// ---- Versiones anteriores (instantáneas) ---------------------------------------------

function VersionsSheet({ entry, onClose, onRestored }: { entry: FileEntry | null; onClose: () => void; onRestored: (e: FileEntry) => void }) {
  const tp = useTransport();
  const [list, setList] = useState<FileVersion[] | null>(null);
  const [pick, setPick] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!entry) return;
    setList(null);
    setPick(null);
    tp.call("files.versions", { path: entry.path }).then(setList, () => setList([]));
  }, [entry, tp]);
  if (!entry) return null;
  const chosen = list?.find((v) => v.snapshot.id === pick);
  const restore = async () => {
    if (!chosen) return;
    setBusy(true);
    try {
      const e = await tp.call("files.restoreVersion", { path: entry.path, snapshot: chosen.snapshot.id });
      onClose();
      onRestored(e);
      toast({ level: "ok", title: t("Se restauró como «{name}»", { name: e.name }), body: t("Es la versión de {date}. El archivo actual quedó como estaba.", { date: fmtDate(chosen.snapshot.at) }) });
    } catch (x) {
      toast({ level: "crit", title: t("No se pudo restaurar la versión"), body: errText(x, { path: entry.path, name: entry.name }) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      open
      onClose={onClose}
      width={520}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={!chosen || busy} onClick={() => void restore()}>
            {busy ? t("Restaurando…") : t("Restaurar esta versión")}
          </Button>
        </>
      }
    >
      <div className="tf-ver-head">
        <History size={22} />
        <div>
          <strong>{t("Versiones anteriores de «{name}»", { name: entry.name })}</strong>
          <span>{t("Cada instantánea guarda cómo estaba la carpeta en ese momento. Restaurar crea una copia al lado del archivo actual, sin pisarlo.")}</span>
        </div>
      </div>
      {!list && <Progress indeterminate />}
      {list && !list.length && <p className="tf-dim">{t("No hay versiones anteriores de este archivo: la carpeta todavía no tiene instantáneas, o el archivo es nuevo.")}</p>}
      {list && list.length > 0 && (
        <ol className="tf-timeline">
          {list.map((v, i) => (
            <li key={v.snapshot.id}>
              <button type="button" aria-pressed={pick === v.snapshot.id} onClick={() => setPick(v.snapshot.id)}>
                <span className="tf-dot" data-first={i === 0 || undefined} />
                <span className="tf-ver-when">
                  <strong>{fmtDate(v.snapshot.at)}</strong>
                  <small>
                    {fmtAgo(v.snapshot.at)} · {t("modificado {date}", { date: fmtDate(v.mtime) })}
                  </small>
                </span>
                {v.snapshot.kind === "manual" ? <Badge tone="info">{t("Manual")}</Badge> : <Badge>{t("Programada")}</Badge>}
                {v.snapshot.locked && <Badge tone="warn">{t("Protegida")}</Badge>}
                <span className="tf-ver-size tg-num">{fmtBytes(v.size)}</span>
              </button>
            </li>
          ))}
        </ol>
      )}
    </Sheet>
  );
}
