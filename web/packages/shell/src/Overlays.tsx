import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { AlertTriangle, AppWindow, CheckCircle2, Info, MoonStar, Network, Search, Settings2, Sun, SunMoon, Thermometer, Users, X, XOctagon, Zap } from "lucide-react";
import { fmtAgo, fmtBytes, fmtRate, t, tn, tr, useJobs, useLive, useMetrics, useNotices, useTopic, maxTemp, fmtCpuTemp, fmtRpm } from "@tango/client";
import type { FileEntry, Notice } from "@tango/client";
import { Progress, Ring } from "@tango/ui";
import { closeOverlay, commands, dismissToast, openApp, prefs, registry, toast, toasts, ui } from "./store";

// ---- Launcher -----------------------------------------------------------------------

export function Launcher() {
  const [q, setQ] = useState("");
  const apps = [...registry.values()].filter((a) => !a.hidden && a.name.toLowerCase().includes(q.toLowerCase()));
  const launch = (id: string) => {
    closeOverlay();
    openApp(id);
  };
  return (
    <div className="tg-launcher" onMouseDown={(e) => e.target === e.currentTarget && closeOverlay()}>
      <label className="tg-launcher-search">
        <Search size={14} />
        <input autoFocus placeholder={t("Buscar")} value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.key === "Enter" && apps[0] && launch(apps[0].id)} aria-label={t("Buscar apps")} />
      </label>
      <div className="tg-launcher-grid" onMouseDown={(e) => e.target === e.currentTarget && closeOverlay()}>
        {apps.map((a) => (
          <button key={a.id} type="button" className="tg-launcher-app" onClick={() => launch(a.id)}>
            <a.icon size={84} />
            <span>{a.name}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

// ---- Spotlight ----------------------------------------------------------------------

interface Hit {
  key: string;
  section: string;
  title: string;
  sub?: string;
  icon: ReactNode;
  run: () => void;
}

const norm = (s: string) => s.normalize("NFD").replace(/\p{Diacritic}/gu, "").toLowerCase();

export function Spotlight({ search }: { search: (q: string) => Promise<FileEntry[]> }) {
  const [q, setQ] = useState("");
  const [files, setFiles] = useState<FileEntry[]>([]);
  const [sel, setSel] = useState(0);
  const cmds = commands.use();

  useEffect(() => {
    if (q.trim().length < 2) return setFiles([]);
    const tm = setTimeout(() => search(q.trim()).then(setFiles, () => setFiles([])), 150);
    return () => clearTimeout(tm);
  }, [q, search]);

  const hits = useMemo<Hit[]>(() => {
    const n = norm(q.trim());
    if (!n) return [];
    const words = n.split(/\s+/);
    const match = (s: string) => words.every((w) => norm(s).includes(w));
    const out: Hit[] = [];
    for (const c of cmds) if (match(`${c.title} ${c.keywords ?? ""}`)) out.push({ key: c.id, section: c.section, title: c.title, icon: <Zap size={16} />, run: c.run });
    for (const a of registry.values()) if (!a.hidden && match(`${a.name} ${(a.keywords ?? []).join(" ")}`)) out.push({ key: a.id, section: "Apps", title: a.name, icon: <a.icon size={20} />, run: () => openApp(a.id) });
    for (const f of files.slice(0, 8)) out.push({ key: f.path, section: "Archivos", title: f.name, sub: f.path, icon: <AppWindow size={16} />, run: () => openApp("tango.files", { path: f.path.slice(0, f.path.lastIndexOf("/")) || "/", select: f.path }) });
    const order = ["Acciones", "Apps", "Ajustes", "Archivos"];
    return out.sort((a, b) => order.indexOf(a.section) - order.indexOf(b.section));
  }, [q, cmds, files]);

  useEffect(() => setSel(0), [q]);

  const run = (h?: Hit) => {
    if (!h) return;
    closeOverlay();
    h.run();
  };

  let lastSection = "";
  return (
    <div className="tg-spot-wrap" onMouseDown={(e) => e.target === e.currentTarget && closeOverlay()}>
      <div className="tg-spot" role="dialog" aria-label={t("Buscar")}>
        <div className="tg-spot-input">
          <Search size={20} />
          <input
            autoFocus
            value={q}
            placeholder={t("Buscar apps, ajustes, archivos y acciones")}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") (e.preventDefault(), setSel((s) => Math.min(hits.length - 1, s + 1)));
              if (e.key === "ArrowUp") (e.preventDefault(), setSel((s) => Math.max(0, s - 1)));
              if (e.key === "Enter") run(hits[sel]);
            }}
            aria-label={t("Buscar")}
          />
        </div>
        {hits.length > 0 && (
          <div className="tg-spot-results" role="listbox">
            {hits.map((h, i) => {
              const head = h.section !== lastSection ? (lastSection = h.section) : null;
              return (
                <div key={h.key}>
                  {head && <div className="tg-spot-section">{t(head)}</div>}
                  <div role="option" aria-selected={i === sel} className="tg-spot-hit" onMouseEnter={() => setSel(i)} onClick={() => run(h)}>
                    <span className="tg-spot-icon">{h.icon}</span>
                    <span className="tg-spot-title">{h.title}</span>
                    {h.sub && <span className="tg-spot-sub">{h.sub}</span>}
                  </div>
                </div>
              );
            })}
          </div>
        )}
        {q.trim().length > 1 && hits.length === 0 && <div className="tg-spot-none">{t("Nada coincide con «{q}».", { q })}</div>}
      </div>
    </div>
  );
}

// ---- Centro de control -------------------------------------------------------------

export function ControlCenter() {
  const p = prefs.use();
  const m = useMetrics();
  const last = m[m.length - 1];
  const vols = useLive("storage.volumes.list", undefined, ["storage.changed"]).data ?? [];
  const sessions = useLive("sessions.list", undefined).data ?? [];
  const disks = useLive("storage.disks.list", undefined, ["storage.changed"]).data ?? [];
  const hottest = maxTemp(disks.map((d) => d.tempC));
  const modes = [
    { id: "light", label: t("Claro"), icon: Sun },
    { id: "dark", label: t("Oscuro"), icon: MoonStar },
    { id: "auto", label: t("Auto"), icon: SunMoon },
  ] as const;
  return (
    <div className="tg-popover tg-cc" role="dialog" aria-label={t("Centro de control")}>
      <div className="tg-cc-tile tg-cc-wide">
        <div className="tg-cc-modes">
          {modes.map((md) => (
            <button key={md.id} type="button" aria-pressed={p.mode === md.id} onClick={() => prefs.set({ ...p, mode: md.id })}>
              <md.icon size={16} />
              {md.label}
            </button>
          ))}
        </div>
      </div>
      {vols.map((v) => (
        <div key={v.id} className="tg-cc-tile tg-cc-wide tg-cc-vol" onClick={() => (closeOverlay(), openApp("tango.storage"))}>
          <Ring value={v.used / v.size} size={38} stroke={5} color={v.state === "degraded" ? "var(--tg-crit)" : "var(--tg-accent)"} />
          <div>
            <strong>{v.name}</strong>
            <small>
              {t("{size} libres", { size: fmtBytes(v.size - v.used) })} · {v.state === "healthy" ? t("En orden") : v.state === "degraded" ? t("Sin redundancia") : v.state === "scrubbing" ? t("Verificando") : t("Construyendo")}
            </small>
          </div>
        </div>
      ))}
      <div className="tg-cc-tile">
        <Network size={16} />
        <strong>192.168.1.50</strong>
        <small>
          ↓ {fmtRate(last?.netRx ?? 0)}
          <br />↑ {fmtRate(last?.netTx ?? 0)}
        </small>
      </div>
      <div className="tg-cc-tile">
        <Thermometer size={16} />
        <strong>{fmtCpuTemp(last?.tempCpu)}</strong>
        <small>
          {hottest == null ? "CPU" : t("CPU · discos hasta {temp} °C", { temp: hottest })}
          <br />
          {t("ventilador {rpm}", { rpm: fmtRpm(last?.fanRpm) })}
        </small>
      </div>
      <div className="tg-cc-tile">
        <Users size={16} />
        <strong>{tn(sessions.length, "{n} sesión", "{n} sesiones")}</strong>
        <small>web, app, SMB</small>
      </div>
      <button type="button" className="tg-cc-tile tg-cc-toggle" aria-pressed={p.dnd} onClick={() => prefs.set({ ...p, dnd: !p.dnd })}>
        <MoonStar size={16} />
        <strong>{t("No molestar")}</strong>
        <small>{p.dnd ? t("Activado") : t("Desactivado")}</small>
      </button>
      <button type="button" className="tg-cc-tile tg-cc-wide tg-cc-link" onClick={() => (closeOverlay(), openApp("tango.settings", { section: "appearance" }))}>
        <Settings2 size={15} /> {t("Apariencia y escritorio…")}
      </button>
    </div>
  );
}

// ---- Actividad (jobs) ---------------------------------------------------------------

export function Activity() {
  const jobs = useJobs();
  return (
    <div className="tg-popover tg-activity" role="dialog" aria-label={t("Actividad")}>
      <div className="tg-pop-title">{t("Actividad")}</div>
      {jobs.length === 0 && <div className="tg-pop-empty">{t("No hay tareas en curso.")}</div>}
      {jobs.map((j) => (
        <div key={j.id} className="tg-job">
          <div className="tg-job-head">
            <strong>{tr(j.title)}</strong>
            <span className="tg-num">{j.state === "succeeded" ? <CheckCircle2 size={14} color="var(--tg-ok)" /> : `${Math.round(j.progress * 100)} %`}</span>
          </div>
          <Progress value={j.progress} tone={j.state === "succeeded" ? "ok" : undefined} />
          <small>
            {tr(j.detail)}
            {j.rate && j.state === "running" ? ` · ${j.rate}` : ""}
          </small>
        </div>
      ))}
    </div>
  );
}

// ---- Notificaciones -----------------------------------------------------------------

const levelIcon = { info: Info, ok: CheckCircle2, warn: AlertTriangle, crit: XOctagon };

function NoticeCard({ n, onAction }: { n: Pick<Notice, "level" | "title" | "body"> & { app?: string; at?: string; action?: { label: string } }; onAction?: () => void }) {
  const I = levelIcon[n.level];
  const app = n.app ? registry.get(n.app) : undefined;
  return (
    <div className="tg-notice" data-level={n.level}>
      <div className="tg-notice-icon">{app ? <app.icon size={30} /> : <I size={22} />}</div>
      <div className="tg-notice-text">
        <div className="tg-notice-head">
          <I size={13} className="tg-notice-level" />
          <strong>{tr(n.title)}</strong>
          {n.at && <time>{fmtAgo(n.at)}</time>}
        </div>
        {n.body && <p>{tr(n.body)}</p>}
        {n.action && onAction && (
          <button type="button" className="tg-notice-action" onClick={onAction}>
            {tr(n.action.label)}
          </button>
        )}
      </div>
    </div>
  );
}

export function NotificationCenter() {
  const notices = useNotices();
  const [cleared, setCleared] = useState<Set<string>>(new Set());
  const visible = notices.filter((n) => !cleared.has(n.id));
  return (
    <aside className="tg-nc" aria-label={t("Notificaciones")}>
      <div className="tg-nc-head">
        <strong>{t("Notificaciones")}</strong>
        {visible.length > 0 && (
          <button type="button" onClick={() => setCleared(new Set(notices.map((n) => n.id)))}>
            {t("Borrar todo")}
          </button>
        )}
      </div>
      {visible.length === 0 && <div className="tg-pop-empty">{t("Sin notificaciones nuevas.")}</div>}
      {visible.map((n) => (
        <NoticeCard key={n.id} n={n} onAction={n.action ? () => (closeOverlay(), openApp(n.action!.app, n.action!.route ? { section: n.action!.route } : {})) : undefined} />
      ))}
    </aside>
  );
}

/** Puente: cada evento `notice` del servidor aparece como toast (salvo "No molestar"). */
export function NoticeToaster() {
  useTopic("notice", (e) => {
    if (prefs.get().dnd && e.data.level !== "crit") return;
    const n = e.data;
    toast({ level: n.level, title: n.title, body: n.body, app: n.app, action: n.action ? { label: n.action.label, run: () => openApp(n.action!.app) } : undefined });
  });
  return null;
}

export function Toasts() {
  const list = toasts.use();
  return (
    <div className="tg-toasts" aria-live="polite">
      {list.map((x) => (
        <div key={x.id} className="tg-toast">
          <NoticeCard n={{ ...x, body: x.body ?? "", action: x.action }} onAction={x.action ? () => (dismissToast(x.id), x.action!.run()) : undefined} />
          <button type="button" className="tg-toast-x" aria-label={t("Cerrar")} onClick={() => dismissToast(x.id)}>
            <X size={11} strokeWidth={3} />
          </button>
        </div>
      ))}
    </div>
  );
}

// ---- Cierre por clic afuera / Esc -----------------------------------------------------

export function useOverlayDismiss() {
  const overlay = ui.use((s) => s.overlay);
  const menu = ui.use((s) => s.openMenu);
  const ref = useRef(false);
  ref.current = !!overlay || menu !== null;
  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      if (!ref.current) return;
      const el = e.target as HTMLElement;
      if (el.closest(".tg-popover,.tg-nc,.tg-spot,.tg-launcher,.tg-mission,.tg-mb-item,.tg-mb-icon,.tg-mb-clock")) return;
      closeOverlay();
    };
    addEventListener("pointerdown", onDown, true);
    return () => removeEventListener("pointerdown", onDown, true);
  }, []);
}
