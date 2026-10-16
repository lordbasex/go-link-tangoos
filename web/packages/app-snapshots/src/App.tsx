// Instantáneas: línea de tiempo de todas las carpetas compartidas (protocol §7 y §11).
import "@tango/app-storage/storage.css";
import "./snapshots.css";
import { useMemo, useState } from "react";
import { CalendarClock, Camera, FolderOpen, History, Lock, Plus, Trash2 } from "lucide-react";
import { RpcError, fmtBytes, locale, t, tr, useIsReal, useLive, useTransport } from "@tango/client";
import type { Share, Snapshot } from "@tango/client";
import { ScheduleSheet, scheduleSummary } from "@tango/app-storage/Schedule";
import { WindowToolbar, openApp, toast, useWindowMenus } from "@tango/shell";
import type { AppProps } from "@tango/shell";
import { Badge, Button, DemoBadge, EmptyState, IconButton, Sheet, SidebarItem } from "@tango/ui";

const SIDE = 220;

function dayKey(iso: string) {
  const d = new Date(iso);
  const today = new Date();
  const yest = new Date(Date.now() - 86400_000);
  if (d.toDateString() === today.toDateString()) return t("Hoy");
  if (d.toDateString() === yest.toDateString()) return t("Ayer");
  const s = d.toLocaleDateString(locale(), { weekday: "long", day: "numeric", month: "long" });
  return s.charAt(0).toUpperCase() + s.slice(1);
}

export default function SnapshotsApp({ win }: AppProps) {
  const tp = useTransport();
  const real = useIsReal("snapshots.list");
  const shares = useLive("shares.list", undefined, ["shares.changed"]).data ?? [];
  const snaps = useLive("snapshots.list", {}, ["snapshots.changed", "shares.changed"]).data ?? [];
  const [share, setShare] = useState<string | "all">(typeof win.args.share === "string" ? (win.args.share as string) : "all");
  const [scheduling, setScheduling] = useState<Share | null>(null);
  const [deleting, setDeleting] = useState<Snapshot | null>(null);

  const visible = useMemo(() => [...snaps].filter((s) => share === "all" || s.share === share).sort((a, b) => b.at.localeCompare(a.at)), [snaps, share]);
  const groups = useMemo(() => {
    const m = new Map<string, Snapshot[]>();
    for (const s of visible) {
      const k = dayKey(s.at);
      m.set(k, [...(m.get(k) ?? []), s]);
    }
    return [...m.entries()];
  }, [visible]);

  const byId = (id: string) => shares.find((s) => s.id === id);
  const current = share === "all" ? null : (byId(share) ?? null);

  const create = async (sh: Share) => {
    try {
      await tp.call("snapshots.create", { share: sh.id });
      toast({ level: "info", title: t("Creando instantánea de {name}…", { name: sh.name }), app: "tango.snapshots" });
    } catch (e) {
      toast({ level: "warn", title: t("No se pudo crear la instantánea"), body: e instanceof RpcError ? e.message : tr(String(e)), app: "tango.snapshots" });
    }
  };

  const explore = (s: Snapshot) => {
    const sh = byId(s.share);
    openApp("tango.files", { path: `/${sh?.name ?? s.share}`, snapshot: s.id, snapshotAt: s.at });
  };

  useWindowMenus(
    win,
    [
      {
        label: t("Instantánea"),
        items: [
          { label: current ? t("Crear instantánea de {name}", { name: current.name }) : t("Crear instantánea"), disabled: !current, action: () => current && void create(current) },
          { label: t("Programar…"), disabled: !current, action: () => current && setScheduling(current) },
        ],
      },
    ],
    [current?.id],
  );

  return (
    <div className="tg-app tsnap" style={{ ["--side" as string]: `${SIDE}px` }}>
      <aside className="tg-app-side tg-sidebar">
        <h4>{t("Carpetas")}</h4>
        <SidebarItem icon={History} label={t("Todas")} current={share === "all"} meta={snaps.length || undefined} onClick={() => setShare("all")} />
        {shares
          .filter((s) => s.snapshots !== false)
          .map((s) => (
            <SidebarItem key={s.id} icon={Camera} color={s.color} label={s.name} current={share === s.id} meta={snaps.filter((x) => x.share === s.id).length || undefined} onClick={() => setShare(s.id)} />
          ))}
      </aside>
      <div className="tg-app-main">
        <WindowToolbar win={win} inset={SIDE}>
          <span className="tg-tb-title">{current ? current.name : t("Todas las instantáneas")}</span>
          <span className="tg-tb-spacer" />
          {!real && <DemoBadge />}
          {current && <IconButton icon={CalendarClock} label={t("Programar")} onClick={() => setScheduling(current)} />}
          {current && (
            <Button onClick={() => void create(current)}>
              <Plus size={14} /> {t("Crear instantánea")}
            </Button>
          )}
        </WindowToolbar>
        <div className="tg-app-content">
          {current && <ScheduleSummary share={current} onEdit={() => setScheduling(current)} />}
          {shares.length === 0 ? (
            <EmptyState icon={Camera} title={t("Todavía no hay carpetas compartidas")} action={<Button onClick={() => openApp("tango.settings", { section: "shares" })}>{t("Crear una carpeta…")}</Button>}>
              {t("Las instantáneas guardan el estado de cada carpeta compartida para volver atrás.")}
            </EmptyState>
          ) : visible.length === 0 ? (
            <EmptyState icon={History} title={t("Sin instantáneas todavía")} action={current ? <Button onClick={() => void create(current)}>{t("Crear la primera")}</Button> : undefined}>
              {current ? t("Creá una ahora o programalas para que se hagan solas.") : t("Elegí una carpeta para crear o programar instantáneas.")}
            </EmptyState>
          ) : (
            <div className="tsnap-timeline">
              {groups.map(([day, list]) => (
                <section key={day}>
                  <h3 className="tsnap-day">{day}</h3>
                  {list.map((s) => {
                    const sh = byId(s.share);
                    return (
                      <div key={s.id} className="tsnap-item">
                        <span className="tsnap-dot" style={{ background: sh?.color ?? "var(--tg-accent)" }} />
                        <div className="tsnap-info">
                          <b>{new Date(s.at).toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit", hourCycle: "h23" })}</b>
                          <span>
                            {sh?.name ?? s.share} · {s.kind === "scheduled" ? t("Programada") : t("Manual")}
                            {s.size > 0 && ` · ${t("{size} nuevos", { size: fmtBytes(s.size) })}`}
                          </span>
                        </div>
                        {s.locked && (
                          <Badge>
                            <Lock size={10} /> {t("Protegida")}
                          </Badge>
                        )}
                        <Button onClick={() => explore(s)}>
                          <FolderOpen size={13} /> {t("Explorar")}
                        </Button>
                        <IconButton icon={Trash2} label={t("Eliminar instantánea")} disabled={s.locked} onClick={() => setDeleting(s)} />
                      </div>
                    );
                  })}
                </section>
              ))}
            </div>
          )}
        </div>
      </div>
      <ScheduleSheet share={scheduling} onClose={() => setScheduling(null)} />
      {deleting && <DeleteSnapshot snap={deleting} shareName={byId(deleting.share)?.name ?? deleting.share} onClose={() => setDeleting(null)} />}
    </div>
  );
}

function ScheduleSummary({ share, onEdit }: { share: Share; onEdit: () => void }) {
  const { data } = useLive("snapshots.schedule.get", { share: share.id }, ["snapshots.changed"]);
  return (
    <div className="tsnap-sched">
      <CalendarClock size={16} />
      <span>{data ? scheduleSummary(data) : "…"}</span>
      <Button variant="plain" onClick={onEdit}>
        {t("Cambiar…")}
      </Button>
    </div>
  );
}

function DeleteSnapshot({ snap, shareName, onClose }: { snap: Snapshot; shareName: string; onClose: () => void }) {
  const tp = useTransport();
  const [busy, setBusy] = useState(false);
  const remove = async () => {
    setBusy(true);
    try {
      await tp.call("snapshots.delete", { id: snap.id });
      toast({ level: "ok", title: t("Instantánea eliminada"), app: "tango.snapshots" });
      onClose();
    } catch (e) {
      setBusy(false);
      toast({ level: "warn", title: t("No se pudo eliminar"), body: e instanceof RpcError ? e.message : tr(String(e)), app: "tango.snapshots" });
    }
  };
  return (
    <Sheet
      open
      onClose={onClose}
      width={420}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="danger" onClick={() => void remove()} disabled={busy}>
            {t("Eliminar")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("¿Eliminar esta instantánea?")}</h2>
      <p className="tg-lead" style={{ marginTop: 6 }}>
        {t("La de {share} del {date}. Los archivos actuales no se tocan; solo se pierde ese punto para volver atrás.", {
          share: shareName,
          date: new Date(snap.at).toLocaleString(locale(), { day: "numeric", month: "long", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }),
        })}
      </p>
    </Sheet>
  );
}
