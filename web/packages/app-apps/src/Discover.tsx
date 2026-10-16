import { SearchX } from "lucide-react";
import { fmtBytes, t, tr } from "@tango/client";
import type { CatalogApp } from "@tango/client";
import { EmptyState } from "@tango/ui";
import type { AppsCtx } from "./App";
import { isActive } from "./jobs";

/** Botón de estado de una app del catálogo: Obtener / Instalando… % / Abrir / Ver. */
export function GetButton({ app, ctx, big }: { app: CatalogApp; ctx: AppsCtx; big?: boolean }) {
  const inst = ctx.installed.find((i) => i.id === app.id);
  const tj = ctx.jobs.get(app.id);
  const busy = isActive(tj) || inst?.state === "installing" || inst?.state === "updating";
  const cls = `tapp-get${big ? " tapp-get-big" : ""}`;
  if (busy) {
    const pct = Math.round((tj?.job.progress ?? 0) * 100);
    return (
      <span className={`${cls} tapp-get-busy`} role="status" aria-label={t("Instalando {pct} %", { pct })}>
        <svg viewBox="0 0 20 20" width="16" height="16" aria-hidden="true">
          <circle cx="10" cy="10" r="8" fill="none" stroke="currentColor" strokeOpacity=".25" strokeWidth="2.4" />
          <circle cx="10" cy="10" r="8" fill="none" stroke="currentColor" strokeWidth="2.4" strokeDasharray={50.3} strokeDashoffset={50.3 * (1 - (tj?.job.progress ?? 0))} transform="rotate(-90 10 10)" strokeLinecap="round" />
        </svg>
        {pct ? `${pct} %` : t("Instalando…")}
      </span>
    );
  }
  if (inst?.state === "running" && inst.url)
    return (
      <button type="button" className={cls} onClick={(e) => (e.stopPropagation(), ctx.launch(inst))}>
        {t("Abrir")}
      </button>
    );
  return (
    <button type="button" className={cls} onClick={(e) => (e.stopPropagation(), ctx.openDetail(app.id))}>
      {inst ? t("Ver") : t("Obtener")}
    </button>
  );
}

export function Featured({ app, ctx }: { app: CatalogApp; ctx: AppsCtx }) {
  return (
    <section className="tapp-featured" onClick={() => ctx.openDetail(app.id)} aria-label={t("Destacada: {name}", { name: app.name })}>
      <div className="tapp-featured-glow" style={{ backgroundImage: `url("${app.icon}")` }} aria-hidden="true" />
      <img src={app.icon} alt="" className="tapp-featured-icon" />
      <div className="tapp-featured-text">
        <span className="tapp-featured-kicker">{t("Destacada · {category}", { category: t(app.category) })}</span>
        <h2>{app.name}</h2>
        <p>{tr(app.description)}</p>
      </div>
      <GetButton app={app} ctx={ctx} big />
    </section>
  );
}

export function AppGrid({ apps, ctx, heading, query }: { apps: CatalogApp[]; ctx: AppsCtx; heading?: string; query?: string }) {
  if (!apps.length)
    return (
      <EmptyState icon={SearchX} title={query ? t("Nada coincide con «{query}»", { query }) : t("No hay apps en esta categoría")}>
        {t("Probá con otra búsqueda o mirá todas las apps en Descubrir.")}
      </EmptyState>
    );
  return (
    <section>
      {heading && <h3 className="tapp-heading">{heading}</h3>}
      <div className="tapp-grid">
        {apps.map((a) => (
          <article key={a.id} className="tapp-card" onClick={() => ctx.openDetail(a.id)} tabIndex={0} onKeyDown={(e) => e.key === "Enter" && ctx.openDetail(a.id)}>
            <img src={a.icon} alt="" className="tapp-card-icon" />
            <div className="tapp-card-text">
              <strong>{a.name}</strong>
              <span className="tapp-card-cat">{t(a.category)}</span>
              <p>{tr(a.description)}</p>
            </div>
            <div className="tapp-card-foot">
              <span className="tg-muted">{fmtBytes(a.sizeHint)}</span>
              <GetButton app={a} ctx={ctx} />
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}
