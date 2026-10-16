import "./apps.css";
import { useEffect, useMemo, useState } from "react";
import { Briefcase, ChevronLeft, Container, Film, House, Image, Network, Package, Shield, Sparkles, Wrench } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { appCategories, t, tr, useIsReal, useLive, useTransport } from "@tango/client";
import type { AppCategory, CatalogApp, InstalledApp } from "@tango/client";
import { DemoBadge, IconButton, SearchField, SidebarItem } from "@tango/ui";
import { WindowToolbar, openApp, toast, useWindowMenus } from "@tango/shell";
import type { AppProps } from "@tango/shell";
import { useTrackedJobs } from "./jobs";
import { appUrl } from "./ticket";
import { AppGrid, Featured } from "./Discover";
import { Detail } from "./Detail";
import { Installed } from "./Installed";
import { DockerHero, DockerSettings } from "./Docker";

const SIDE = 210;

export const categoryIcon: Record<AppCategory, LucideIcon> = {
  Multimedia: Film,
  Fotos: Image,
  Hogar: House,
  Productividad: Briefcase,
  Red: Network,
  Seguridad: Shield,
  Utilidades: Wrench,
};

type View = { kind: "discover" } | { kind: "installed" } | { kind: "category"; cat: AppCategory } | { kind: "docker" };

/** Todo lo que las vistas necesitan del Centro de apps. */
export interface AppsCtx {
  catalog: CatalogApp[];
  installed: InstalledApp[];
  dockerReady: boolean;
  jobs: ReturnType<typeof useTrackedJobs>;
  openDetail: (id: string) => void;
  launch: (a: InstalledApp) => void;
  run: (key: string, p: Promise<{ job: string }>, failTitle: string) => void;
}

export default function AppsApp({ win }: AppProps) {
  const tp = useTransport();
  const real = useIsReal("apps.catalog");
  const docker = useLive("apps.docker.status", undefined, ["apps.changed"]);
  const catalog = useLive("apps.catalog", undefined, ["apps.changed"]).data ?? [];
  const installed = useLive("apps.list", undefined, ["apps.changed"]).data ?? [];
  const jobs = useTrackedJobs();
  const [view, setView] = useState<View>({ kind: "discover" });
  const [detail, setDetail] = useState<string | null>(null);
  const [q, setQ] = useState("");

  const dockerReady = !!docker.data?.installed;

  useEffect(() => {
    if (typeof win.args.app === "string") setDetail(win.args.app);
  }, [win.args]);

  const ctx: AppsCtx = {
    catalog,
    installed,
    dockerReady,
    jobs,
    openDetail: (id) => setDetail(id),
    launch: (a) => {
      if (!a.url) return toast({ level: "info", title: t("{name} no tiene interfaz web", { name: a.name }) });
      void appUrl(tp, a).then((url) => url && openApp("tango.appview", { url, title: a.name, icon: a.icon }));
    },
    run: (key, p, failTitle) => {
      p.then(
        (r) => jobs.track(key, r.job),
        (e: Error) => toast({ level: "warn", title: failTitle, body: e.message }),
      );
    },
  };

  useWindowMenus(
    win,
    [
      {
        label: "Apps",
        items: [
          { label: t("Descubrir"), checked: view.kind === "discover", action: () => (setDetail(null), setView({ kind: "discover" })) },
          { label: t("Instaladas"), checked: view.kind === "installed", action: () => (setDetail(null), setView({ kind: "installed" })) },
          { separator: true },
          { label: t("Ajustes de Docker"), action: () => (setDetail(null), setView({ kind: "docker" })) },
        ],
      },
    ],
    [view.kind],
  );

  const go = (v: View) => {
    setDetail(null);
    setView(v);
  };

  const filtered = useMemo(() => {
    const n = q.trim().toLowerCase();
    let list = catalog;
    if (view.kind === "category") list = list.filter((a) => a.category === view.cat);
    if (n) list = list.filter((a) => `${a.name} ${a.description} ${a.category} ${tr(a.description)} ${t(a.category)}`.toLowerCase().includes(n));
    return list;
  }, [catalog, view, q]);

  const detailApp = detail ? catalog.find((a) => a.id === detail) : undefined;
  const title = detailApp ? detailApp.name : view.kind === "discover" ? t("Descubrir") : view.kind === "installed" ? t("Instaladas") : view.kind === "docker" ? t("Ajustes de Docker") : t(view.cat);
  const showHero = !docker.loading && !dockerReady && view.kind !== "docker";

  return (
    <div className="tg-app tapp" style={{ ["--side" as string]: `${SIDE}px` }}>
      <nav className="tg-app-side tg-sidebar" aria-label={t("Secciones")}>
        <h4>{t("Centro de apps")}</h4>
        <SidebarItem icon={Sparkles} label={t("Descubrir")} current={!detail && view.kind === "discover"} onClick={() => go({ kind: "discover" })} />
        <SidebarItem icon={Package} label={t("Instaladas")} current={!detail && view.kind === "installed"} meta={installed.length || undefined} onClick={() => go({ kind: "installed" })} />
        <h4>{t("Categorías")}</h4>
        {appCategories.map((c) => (
          <SidebarItem key={c} icon={categoryIcon[c]} label={t(c)} current={!detail && view.kind === "category" && view.cat === c} meta={catalog.filter((a) => a.category === c).length || undefined} onClick={() => go({ kind: "category", cat: c })} />
        ))}
        <h4>{t("Sistema")}</h4>
        <SidebarItem icon={Container} label={t("Ajustes de Docker")} current={!detail && view.kind === "docker"} onClick={() => go({ kind: "docker" })} />
      </nav>
      <div className="tg-app-main">
        <WindowToolbar win={win} inset={SIDE}>
          {detail && <IconButton icon={ChevronLeft} label={t("Atrás")} onClick={() => setDetail(null)} />}
          <span className="tg-tb-title">{title}</span>
          <span className="tg-tb-spacer" />
          {!real && <DemoBadge />}
          {!detail && (view.kind === "discover" || view.kind === "category") && (
            <div className="tapp-search">
              <SearchField value={q} onChange={setQ} placeholder={t("Buscar apps")} />
            </div>
          )}
        </WindowToolbar>
        <div className="tg-app-content">
          {detailApp ? (
            <Detail app={detailApp} ctx={ctx} onUninstalled={() => setDetail(null)} />
          ) : showHero ? (
            <DockerHero jobs={jobs} run={ctx.run} install={() => tp.call("apps.docker.install", undefined)} />
          ) : view.kind === "docker" ? (
            <DockerSettings status={docker.data} jobs={jobs} run={ctx.run} install={() => tp.call("apps.docker.install", undefined)} />
          ) : view.kind === "installed" ? (
            <Installed ctx={ctx} onBrowse={() => go({ kind: "discover" })} />
          ) : (
            <div className="tg-app-pad tapp-stack">
              {view.kind === "discover" && !q && catalog.length > 0 && <Featured app={catalog.find((a) => !installed.some((i) => i.id === a.id)) ?? catalog[0]} ctx={ctx} />}
              <AppGrid apps={filtered} ctx={ctx} heading={view.kind === "discover" && !q ? t("Todas las apps") : undefined} query={q} />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
