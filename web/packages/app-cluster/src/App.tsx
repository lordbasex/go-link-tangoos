// Clúster: nodos TangoOS unidos con GlusterFS y sus volúmenes replicados
// (protocol/README.md §17, docs/17-cluster.md).
import "./cluster.css";
import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, Boxes, Camera, Database, FolderOpen, Gauge, HardDrive, Layers, Lock, LockOpen, Network, Play, Plus, RefreshCw, Server, Settings2, Share2, ShieldCheck, Square, Terminal, Trash2, UserPlus } from "lucide-react";
import { RpcError, clusterInfoText, clusterVolumeTone, fmtBytes, locale, t, tn, tr, useIsReal, useLive, useTransport } from "@tango/client";
import type { ClusterNode, ClusterStatus, ClusterVolume } from "@tango/client";
import { WindowToolbar, toast, useWindowMenus } from "@tango/shell";
import type { AppProps } from "@tango/shell";
import { Badge, Button, DemoBadge, EmptyState, Group, IconButton, Progress, Row, Segmented, Sheet, SidebarItem, Status, TextInput, Toggle } from "@tango/ui";
import { AddNodeSheet, BrickPicker, BricksSheet, InviteSheet, NFSSheet, QuotaSheet, SecurityGroup, SnapshotsSheet, TLSSheet, choiceSpec, defaultChoice, rich } from "./Stage2";
import type { BrickChoice } from "./Stage2";

const SIDE = 230;
const APP = "tango.cluster";

type View = { kind: "overview" } | { kind: "nodes" } | { kind: "volume"; name: string };

const errText = (e: unknown) => (e instanceof RpcError ? e.message : String(e));

function useAct() {
  const tp = useTransport();
  return async (title: string, fn: () => Promise<unknown>, ok?: string) => {
    try {
      await fn();
      if (ok) toast({ level: "ok", title: ok, app: APP });
      return true;
    } catch (e) {
      toast({ level: "warn", title, body: errText(e), app: APP });
      return false;
    } finally {
      void tp;
    }
  };
}

const volLabel = (v: ClusterVolume) => {
  if (v.arbiter > 0) return `${v.type} ${v.sets} × (${v.replica - v.arbiter} + ${v.arbiter})`;
  if (v.replica > 1) return `${v.type} ${v.sets} × ${v.replica}`;
  return t("{type} · {n} bricks", { type: v.type, n: v.bricks.length });
};

const volState = (v: ClusterVolume): string => {
  if (v.splitBrain > 0) return tn(v.splitBrain, "Split-brain en un archivo", "Split-brain en {n} archivos");
  if (v.status === "Created") return t("Creado, sin arrancar");
  if (v.status === "Stopped") return t("Detenido");
  if (v.online < v.bricks.length) return t("{online} de {total} bricks en línea", { online: v.online, total: v.bricks.length });
  if (v.healPending > 0) return tn(v.healPending, "Curando una entrada", "Curando {n} entradas");
  return t("En orden");
};

export default function ClusterApp({ win }: AppProps) {
  const real = useIsReal("cluster.status");
  const { data: st, reload } = useLive("cluster.status", undefined, ["cluster.changed", "jobs.changed"]);
  const [view, setView] = useState<View>({ kind: "overview" });
  const [sheet, setSheet] = useState<null | "invite" | "add" | "create" | "tls">(null);
  const [certify, setCertify] = useState<string | null>(null);

  useEffect(() => {
    const id = setInterval(reload, 10_000);
    return () => clearInterval(id);
  }, [reload]);

  useWindowMenus(
    win,
    [
      {
        label: t("Clúster"),
        items: [
          { label: t("Invitar un nodo…"), disabled: !st?.running, action: () => setSheet("invite") },
          { label: t("Agregar un nodo…"), disabled: !st?.running, action: () => setSheet("add") },
          { label: t("Crear volumen…"), disabled: !st?.running, action: () => setSheet("create") },
        ],
      },
    ],
    [st?.running],
  );

  const current = view.kind === "volume" ? st?.volumes.find((v) => v.name === view.name) : undefined;
  const title = view.kind === "nodes" ? t("Nodos") : current ? current.name : t("Clúster");

  return (
    <div className="tg-app tcl" style={{ ["--side" as string]: `${SIDE}px` }}>
      <aside className="tg-app-side tg-sidebar">
        <h4>{t("Clúster")}</h4>
        <SidebarItem icon={Boxes} label={t("Resumen")} current={view.kind === "overview"} onClick={() => setView({ kind: "overview" })} />
        <SidebarItem icon={Server} label={t("Nodos")} current={view.kind === "nodes"} meta={st?.nodes.length || undefined} onClick={() => setView({ kind: "nodes" })} disabled={!st?.installed} />
        {!!st?.volumes.length && <h4>{t("Volúmenes")}</h4>}
        {st?.volumes.map((v) => (
          <SidebarItem
            key={v.name}
            icon={Database}
            label={v.name}
            current={view.kind === "volume" && view.name === v.name}
            meta={clusterVolumeTone(v) !== "ok" ? <span className="tcl-dot" data-tone={clusterVolumeTone(v)} /> : undefined}
            onClick={() => setView({ kind: "volume", name: v.name })}
          />
        ))}
      </aside>
      <div className="tg-app-main">
        <WindowToolbar win={win} inset={SIDE}>
          <span className="tg-tb-title">{title}</span>
          <span className="tg-tb-spacer" />
          {!real && <DemoBadge />}
          {st?.running && (
            <>
              <IconButton icon={UserPlus} label={t("Invitar un nodo")} onClick={() => setSheet("invite")} />
              <IconButton icon={Network} label={t("Agregar un nodo")} onClick={() => setSheet("add")} />
              <Button onClick={() => setSheet("create")}>
                <Plus size={14} /> {t("Crear volumen")}
              </Button>
            </>
          )}
        </WindowToolbar>
        <div className="tg-app-content">
          {!st ? null : !st.installed || !st.running ? (
            <Install st={st} />
          ) : view.kind === "nodes" ? (
            <Nodes st={st} onInvite={() => setSheet("invite")} onAdd={() => setSheet("add")} onTLS={() => setSheet("tls")} onCertify={setCertify} />
          ) : current ? (
            <VolumeView st={st} v={current} />
          ) : (
            <Overview st={st} open={(name) => setView({ kind: "volume", name })} onCreate={() => setSheet("create")} onAdd={() => setSheet("add")} />
          )}
        </div>
      </div>
      {st && sheet === "invite" && <InviteSheet st={st} onClose={() => setSheet(null)} />}
      {st && sheet === "add" && <AddNodeSheet st={st} onClose={() => setSheet(null)} />}
      {st && certify && <AddNodeSheet st={st} certifyHost={certify} onClose={() => setCertify(null)} />}
      {st && sheet === "tls" && <TLSSheet st={st} onClose={() => setSheet(null)} />}
      {st && sheet === "create" && (
        <CreateSheet
          st={st}
          onClose={() => setSheet(null)}
          onCreated={(name) => {
            setSheet(null);
            setView({ kind: "volume", name });
          }}
        />
      )}
    </div>
  );
}

// ---- Instalación ----------------------------------------------------------------------

function Install({ st }: { st: ClusterStatus }) {
  const tp = useTransport();
  const act = useAct();
  const [busy, setBusy] = useState(false);
  const install = () => {
    setBusy(true);
    void act(t("No se pudo instalar GlusterFS"), () => tp.call("cluster.install", undefined), t("Instalando GlusterFS…")).finally(() => setBusy(false));
  };
  return (
    <EmptyState
      icon={Boxes}
      title={st.installed ? t("GlusterFS no está corriendo") : t("Uní varios TangoOS en un clúster")}
      action={
        <Button variant="primary" onClick={install} disabled={busy}>
          {st.installed ? t("Arrancar GlusterFS") : t("Instalar GlusterFS")}
        </Button>
      }
    >
      {st.installed
        ? (st.error ? tr(st.error) : t("glusterd no responde."))
        : t("Con GlusterFS, cada archivo se guarda a la vez en varios NAS: si uno se apaga, los demás siguen sirviendo los datos. Se instala solo en este equipo (unos 31 MB) y los puertos quedan cerrados salvo para los nodos que invites.")}
    </EmptyState>
  );
}

// ---- Resumen --------------------------------------------------------------------------

function Overview({ st, open, onCreate, onAdd }: { st: ClusterStatus; open: (name: string) => void; onCreate: () => void; onAdd: () => void }) {
  const connected = st.nodes.filter((n) => n.connected).length;
  const issues = st.volumes.filter((v) => clusterVolumeTone(v) === "crit" || clusterVolumeTone(v) === "warn");
  return (
    <div className="tcl-pad">
      <div className="tcl-stats">
        <Stat icon={Server} label={t("Nodos")} value={t("{n} de {total}", { n: connected, total: st.nodes.length })} tone={connected === st.nodes.length ? "ok" : "warn"} hint={connected === st.nodes.length ? t("Todos conectados") : t("Hay nodos sin conexión")} />
        <Stat icon={Database} label={t("Volúmenes")} value={String(st.volumes.length)} tone={issues.length ? "warn" : "ok"} hint={issues.length ? tn(issues.length, "Uno con avisos", "{n} con avisos") : t("Todos en orden")} />
        <Stat icon={ShieldCheck} label={t("Firewall")} value={tn(st.allowed.length, "{n} permitido", "{n} permitidos")} tone="info" hint={t("Solo estos equipos llegan a GlusterFS")} />
        <Stat icon={st.tls.secure ? Lock : LockOpen} label={t("Entre nodos")} value={st.tls.secure ? t("Con TLS") : t("Sin cifrar")} tone={st.tls.secure ? "ok" : "warn"} hint={st.tls.secure ? t("Certificados de la CA del clúster") : t("Prendelo desde Nodos")} />
      </div>

      {st.volumes.some((v) => v.splitBrain > 0) && (
        <div className="tcl-alert" data-tone="crit">
          <AlertTriangle size={16} />
          <span>{t("Hay archivos en split-brain: cada copia tiene una versión distinta y GlusterFS no puede elegir sola. Abrí el volumen para ver dónde.")}</span>
        </div>
      )}

      {st.nodes.length === 1 ? (
        <EmptyState icon={Network} title={t("Este NAS todavía está solo")} action={<Button onClick={onAdd}>{t("Agregar un nodo…")}</Button>}>
          {t("Para sumar otro TangoOS: en ese equipo abrí Clúster, tocá «Invitar un nodo» y escribí la IP de este NAS ({ip}). Después agregalo desde acá.", { ip: st.selfAddrs?.[0] ?? st.self })}
        </EmptyState>
      ) : st.volumes.length === 0 ? (
        <EmptyState icon={Database} title={t("Sin volúmenes del clúster")} action={<Button onClick={onCreate}>{t("Crear volumen…")}</Button>}>
          {t("Un volumen replicado guarda una copia completa en cada nodo que elijas.")}
        </EmptyState>
      ) : (
        <Group title={t("Volúmenes")}>
          {st.volumes.map((v) => (
            <Row key={v.name} icon={<Database size={16} />} label={<b>{v.name}</b>} hint={`${volLabel(v)} · ${v.sizeTotal ? t("{used} de {total}", { used: fmtBytes(v.sizeTotal - v.sizeFree), total: fmtBytes(v.sizeTotal) }) : t("sin datos de espacio")}`}>
              <Status tone={clusterVolumeTone(v)}>{volState(v)}</Status>
              <Button variant="plain" onClick={() => open(v.name)}>
                {t("Abrir")}
              </Button>
            </Row>
          ))}
        </Group>
      )}

      {st.orphans.length > 0 && <Orphans st={st} />}
      <p className="tcl-foot">{t("GlusterFS {version} · este nodo: {self}", { version: st.version ?? "", self: st.self })}</p>
    </div>
  );
}

function Stat({ icon: Icon, label, value, hint, tone }: { icon: typeof Server; label: string; value: string; hint: string; tone: "ok" | "warn" | "crit" | "info" }) {
  return (
    <div className="tcl-stat" data-tone={tone}>
      <Icon size={18} />
      <div>
        <span>{label}</span>
        <b>{value}</b>
        <small>{hint}</small>
      </div>
    </div>
  );
}

function Orphans({ st }: { st: ClusterStatus }) {
  const tp = useTransport();
  const act = useAct();
  const [confirm, setConfirm] = useState<string | null>(null);
  return (
    <Group title={t("Bricks huérfanos en este nodo")}>
      {st.orphans.map((o) => (
        <Row key={o.path} icon={<HardDrive size={16} />} label={o.path} hint={o.kind === "lv" ? t("Volumen dedicado del brick de {name} que ya no usa ningún brick. Ocupa espacio en el grupo {group}.", { name: o.name, group: o.tango_volume }) : t("Quedaron del volumen {name}, que ya no existe. Ocupan espacio en {volume}.", { name: o.name, volume: o.tango_volume })}>
          {confirm === o.path ? (
            <Button variant="danger" onClick={() => void act(t("No se pudo borrar"), () => tp.call("cluster.brick.cleanup", { kind: o.kind, tangoVolume: o.tango_volume, name: o.name }), t("Borrado {path}", { path: o.path })).then(() => setConfirm(null))}>
              {t("Borrar los datos")}
            </Button>
          ) : (
            <Button onClick={() => setConfirm(o.path)}>{t("Borrar…")}</Button>
          )}
        </Row>
      ))}
    </Group>
  );
}

// ---- Nodos -----------------------------------------------------------------------------

function latencyText(n: ClusterNode) {
  if (n.self) return "—";
  if (!n.connected || n.latencyMs < 0) return t("sin respuesta");
  return `${n.latencyMs} ms`;
}

function Nodes({ st, onInvite, onAdd, onTLS, onCertify }: { st: ClusterStatus; onInvite: () => void; onAdd: () => void; onTLS: () => void; onCertify: (host: string) => void }) {
  const tp = useTransport();
  const act = useAct();
  const [removing, setRemoving] = useState<string | null>(null);
  const bricksOn = (host: string) => st.volumes.reduce((s, v) => s + v.bricks.filter((b) => b.host === host).length, 0);
  return (
    <div className="tcl-pad">
      <table className="tcl-table">
        <thead>
          <tr>
            <th>{t("Nodo")}</th>
            <th>{t("Estado")}</th>
            <th>{t("Latencia")}</th>
            <th>{t("Certificado")}</th>
            <th>{t("Bricks")}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {st.nodes.map((n) => (
            <tr key={n.uuid}>
              <td>
                <b>{n.host}</b>
                {n.self && <Badge>{t("Este NAS")}</Badge>}
              </td>
              <td>
                <Status tone={n.connected ? "ok" : "crit"}>{n.self ? t("En línea") : n.connected ? t("Conectado") : t("Sin conexión")}</Status>
              </td>
              <td className="tcl-num" data-slow={!n.self && n.latencyMs > 20 ? true : undefined}>
                {latencyText(n)}
              </td>
              <td>{st.nodeInfo[n.host] ? <Badge tone="ok">{st.nodeInfo[n.host].cn}</Badge> : <Badge tone="warn">{t("Sin certificado")}</Badge>}</td>
              <td className="tcl-num">{bricksOn(n.host)}</td>
              <td className="tcl-actions">
                {!n.self &&
                  (removing === n.host ? (
                    <Button variant="danger" onClick={() => void act(t("No se pudo sacar el nodo"), () => tp.call("cluster.peer.remove", { host: n.host }), t("{host} salió del clúster", { host: n.host })).then(() => setRemoving(null))}>
                      {t("Sacar del clúster")}
                    </Button>
                  ) : (
                    <IconButton icon={Trash2} label={t("Sacar {host}", { host: n.host })} disabled={bricksOn(n.host) > 0} onClick={() => setRemoving(n.host)} />
                  ))}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="tcl-note">{t("Un nodo con bricks no se puede sacar: primero reemplazá o quitá sus bricks desde cada volumen. Al sacarlo, sale del firewall de todos y de la lista TLS de cada volumen.")}</p>

      <SecurityGroup st={st} onTLS={onTLS} onCertify={onCertify} />

      <Group title={t("Invitaciones abiertas en este NAS")}>
        {st.invites.length === 0 ? (
          <Row label={t("Ninguna")} hint={t("Invitá a un equipo para que pueda sumar a este NAS a su clúster.")}>
            <Button onClick={onInvite}>{t("Invitar…")}</Button>
          </Row>
        ) : (
          st.invites.map((i) => (
            <Row
              key={i.host}
              icon={<UserPlus size={16} />}
              label={
                <span>
                  {i.host}
                  {i.code && !i.joined && <code className="tcl-path">{i.code}</code>}
                  {i.joined && <Badge tone="ok">{t("Unido")}</Badge>}
                </span>
              }
              hint={`${i.addrs.join(", ")} · ${t("vence {time}", { time: new Date(i.expires).toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }) })}${i.fingerprint ? ` · ${t("huella {fp}", { fp: i.fingerprint })}` : ""}`}
            >
              <Button variant="plain" onClick={() => void act(t("No se pudo cancelar"), () => tp.call("cluster.invite.cancel", { host: i.host }))}>
                {t("Cancelar")}
              </Button>
            </Row>
          ))
        )}
      </Group>
      <Group title={t("Equipos que pueden hablar con GlusterFS")}>
        <Row label={st.allowed.length ? st.allowed.join(" · ") : t("Solo este NAS")} hint={t("Los nodos del clúster y las invitaciones abiertas. Para cualquier otro equipo de la red, los puertos de GlusterFS están cerrados.")}>
          <Button variant="plain" onClick={onAdd}>
            {t("Agregar un nodo…")}
          </Button>
        </Row>
      </Group>
    </div>
  );
}

// ---- Volumen ---------------------------------------------------------------------------

function VolumeView({ st, v }: { st: ClusterStatus; v: ClusterVolume }) {
  const tp = useTransport();
  const act = useAct();
  const [showInfo, setShowInfo] = useState(false);
  const [sheet, setSheet] = useState<null | "options" | "mount" | "delete" | "bricks" | "quota" | "snaps" | "nfs">(null);
  const mount = st.mounts.find((m) => m.name === v.name);
  const used = v.sizeTotal ? (v.sizeTotal - v.sizeFree) / v.sizeTotal : 0;
  const sets = useMemo(() => {
    const out: (typeof v.bricks)[] = [];
    for (let i = 0; i < v.bricks.length; i += Math.max(1, v.replica)) out.push(v.bricks.slice(i, i + Math.max(1, v.replica)));
    return out;
  }, [v]);
  const action = (a: "start" | "stop") =>
    void act(a === "start" ? t("No se pudo arrancar") : t("No se pudo detener"), () => tp.call("cluster.volume.action", { name: v.name, action: a }), a === "start" ? t("{name} arrancado", { name: v.name }) : t("{name} detenido", { name: v.name }));

  return (
    <div className="tcl-pad">
      <div className="tcl-vhead">
        <div>
          <div className="tcl-vtitle">
            <Status tone={clusterVolumeTone(v)} size={16}>
              {volState(v)}
            </Status>
          </div>
          <span className="tcl-sub">
            {volLabel(v)} · {v.status === "Started" ? t("Arrancado") : v.status === "Stopped" ? t("Detenido") : t("Creado")}
            {mount?.mounted && ` · ${t("montado en {path}", { path: mount.path })}`}
            {mount?.share && ` · ${t("carpeta SMB «{share}»", { share: mount.share })}`}
            {st.nfs[v.name]?.length ? ` · ${t("NFS para {hosts}", { hosts: st.nfs[v.name].map((r) => r.host).join(", ") })}` : ""}
            {v.tls ? " · TLS" : ""}
          </span>
        </div>
        <div className="tcl-vbtns">
          {v.status === "Started" ? <IconButton icon={Square} label={t("Detener")} onClick={() => action("stop")} /> : <IconButton icon={Play} label={t("Arrancar")} onClick={() => action("start")} />}
          <IconButton icon={RefreshCw} label={t("Curar ahora")} disabled={v.status !== "Started" || v.replica < 2} onClick={() => void act(t("No se pudo lanzar la curación"), () => tp.call("cluster.volume.heal", { name: v.name }), t("Curación lanzada"))} />
          <IconButton icon={Settings2} label={t("Opciones")} onClick={() => setSheet("options")} />
          <IconButton icon={HardDrive} label={t("Bricks: agregar, quitar, reemplazar, rebalancear")} onClick={() => setSheet("bricks")} />
          <IconButton icon={Gauge} label={t("Cuotas")} onClick={() => setSheet("quota")} />
          <IconButton icon={Camera} label={t("Instantáneas")} onClick={() => setSheet("snaps")} />
          <IconButton icon={Network} label="NFS" onClick={() => setSheet("nfs")} />
          <IconButton icon={Terminal} label={t("Ver como gluster volume info")} pressed={showInfo} onClick={() => setShowInfo(!showInfo)} />
          <Button onClick={() => setSheet("mount")}>
            <FolderOpen size={13} /> {mount?.mounted ? t("Montaje y SMB…") : t("Montar…")}
          </Button>
          <IconButton icon={Trash2} label={t("Borrar el volumen")} onClick={() => setSheet("delete")} />
        </div>
      </div>

      {v.sizeTotal > 0 && (
        <div className="tcl-cap">
          <Progress value={used} tone={used > 0.9 ? "crit" : used > 0.8 ? "warn" : undefined} />
          <span>
            {t("{used} usados de {total} · {free} libres", { used: fmtBytes(v.sizeTotal - v.sizeFree), total: fmtBytes(v.sizeTotal), free: fmtBytes(v.sizeFree) })}
          </span>
        </div>
      )}

      {v.splitBrain > 0 && (
        <div className="tcl-alert" data-tone="crit">
          <AlertTriangle size={16} />
          <span>
            {rich(
              tn(
                v.splitBrain,
                "Un archivo tiene versiones distintas en cada copia. Revisalo con {cmd} y elegí la copia buena, o fijá la opción «cluster.favorite-child-policy».",
                "{n} archivos tienen versiones distintas en cada copia. Revisalos con {cmd} y elegí la copia buena, o fijá la opción «cluster.favorite-child-policy».",
                { cmd: "{cmd}" },
              ),
              { cmd: <code>gluster volume heal {v.name} info split-brain</code> },
            )}
          </span>
        </div>
      )}
      {v.healPending > 0 && v.splitBrain === 0 && (
        <div className="tcl-alert" data-tone="warn">
          <RefreshCw size={16} />
          <span>{tn(v.healPending, "Falta copiar una entrada a todas las réplicas. Pasa después de que un nodo estuvo apagado; se completa sola.", "Faltan copiar {n} entradas a todas las réplicas. Pasa después de que un nodo estuvo apagado; se completa sola.")}</span>
        </div>
      )}

      {showInfo ? (
        <pre className="tcl-info" aria-label={t("Salida de gluster volume info")}>{clusterInfoText(v)}</pre>
      ) : (
        sets.map((set, i) => (
          <Group key={i} title={sets.length > 1 ? t("Conjunto de réplica {n}", { n: i + 1 }) : t("Bricks")}>
            {set.map((b) => (
              <Row
                key={b.host + b.path}
                icon={<HardDrive size={16} />}
                label={
                  <span>
                    <b>{b.host}</b>
                    <span className="tcl-path">{b.path}</span>
                    {b.arbiter && <Badge>{t("Árbitro")}</Badge>}
                    {b.kind === "lv" && <Badge tone="info">{t("Volumen dedicado")}</Badge>}
                    {b.kind === "snap" && <Badge tone="info">{t("Restaurado")}</Badge>}
                  </span>
                }
                hint={
                  b.online
                    ? `${b.port ? `${t("puerto {port}", { port: b.port })} · ` : ""}${b.sizeFree != null && b.sizeTotal ? t("{free} libres de {total}", { free: fmtBytes(b.sizeFree), total: fmtBytes(b.sizeTotal) }) : ""}${b.fsName ? ` · ${b.fsName}` : ""}`
                    : (b.healStatus ? tr(b.healStatus) : t("El nodo no responde"))
                }
              >
                {b.splitBrain > 0 && <Badge tone="crit">{t("{n} en split-brain", { n: b.splitBrain })}</Badge>}
                {b.healPending > 0 && <Badge tone="warn">{t("{n} por curar", { n: b.healPending })}</Badge>}
                <Status tone={b.online ? "ok" : "crit"}>{b.online ? t("En línea") : t("Fuera de línea")}</Status>
              </Row>
            ))}
          </Group>
        ))
      )}

      {sheet === "options" && <OptionsSheet st={st} v={v} onClose={() => setSheet(null)} />}
      {sheet === "mount" && <MountSheet v={v} mounted={!!mount?.mounted} share={mount?.share} onClose={() => setSheet(null)} />}
      {sheet === "delete" && <DeleteSheet v={v} mounted={!!mount?.mounted} onClose={() => setSheet(null)} />}
      {sheet === "bricks" && <BricksSheet st={st} v={v} onClose={() => setSheet(null)} />}
      {sheet === "quota" && <QuotaSheet v={v} onClose={() => setSheet(null)} />}
      {sheet === "snaps" && <SnapshotsSheet v={v} onClose={() => setSheet(null)} />}
      {sheet === "nfs" && <NFSSheet st={st} v={v} onClose={() => setSheet(null)} />}
    </div>
  );
}

function OptionsSheet({ st, v, onClose }: { st: ClusterStatus; v: ClusterVolume; onClose: () => void }) {
  const tp = useTransport();
  const act = useAct();
  const [vals, setVals] = useState<Record<string, string>>(() => ({ ...v.options }));
  const changed = st.options.filter((o) => (vals[o.key] ?? "") !== (v.options[o.key] ?? ""));
  const save = () => {
    const set: Record<string, string> = {};
    const reset: string[] = [];
    for (const o of changed) {
      if (vals[o.key]) set[o.key] = vals[o.key];
      else if (!o.global) reset.push(o.key);
    }
    void act(t("No se pudieron cambiar las opciones"), () => tp.call("cluster.volume.options", { name: v.name, set, reset }), t("Opciones guardadas")).then((ok) => ok && onClose());
  };
  const applyPreset = (id: string) => {
    const p = st.presets.find((x) => x.id === id);
    if (p) setVals((cur) => ({ ...cur, ...p.options }));
  };
  return (
    <Sheet
      open
      onClose={onClose}
      width={620}
      footer={
        <>
          <span className="tcl-grow">{changed.length ? tn(changed.length, "Un cambio", "{n} cambios") : t("Sin cambios")}</span>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={!changed.length} onClick={save}>
            {t("Guardar")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Opciones de {name}", { name: v.name })}</h2>
      <div className="tcl-presets">
        {st.presets
          .filter((p) => Object.keys(p.options).length)
          .map((p) => (
            <Button key={p.id} variant="plain" onClick={() => applyPreset(p.id)}>
              {t("Aplicar «{name}»", { name: tr(p.name) })}
            </Button>
          ))}
      </div>
      <div className="tcl-opts">
        {st.options.map((o) => (
          <div key={o.key} className="tcl-opt" data-changed={(vals[o.key] ?? "") !== (v.options[o.key] ?? "") || undefined}>
            <div>
              <code>{o.key}</code>
              <small>
                {tr(o.desc)}
                {o.global && ` · ${t("vale para todo el clúster")}`}
              </small>
            </div>
            {o.type === "bool" ? (
              <Segmented
                label={o.key}
                value={(vals[o.key] ?? "") as "" | "on" | "off"}
                onChange={(x) => setVals({ ...vals, [o.key]: x })}
                options={[
                  { value: "", label: t("Defecto") },
                  { value: "on", label: "on" },
                  { value: "off", label: "off" },
                ]}
              />
            ) : o.type === "enum" ? (
              <select className="tg-select" value={vals[o.key] ?? ""} onChange={(e) => setVals({ ...vals, [o.key]: e.target.value })} aria-label={o.key}>
                <option value="">{t("Defecto")}</option>
                {o.enum?.map((x) => (
                  <option key={x}>{x}</option>
                ))}
              </select>
            ) : (
              <TextInput value={vals[o.key] ?? ""} placeholder={t("Defecto")} aria-label={o.key} onChange={(e) => setVals({ ...vals, [o.key]: e.target.value })} style={{ width: 110 }} />
            )}
          </div>
        ))}
      </div>
    </Sheet>
  );
}

function MountSheet({ v, mounted, share, onClose }: { v: ClusterVolume; mounted: boolean; share?: string; onClose: () => void }) {
  const tp = useTransport();
  const act = useAct();
  const users = useLive("users.list", undefined, ["users.changed"]).data ?? [];
  const [on, setOn] = useState(true);
  const [smb, setSmb] = useState(!!share);
  const [name, setName] = useState(share ?? v.name.replace(/^gv_/, "").replace(/(^|_)(\w)/g, (_, s: string, c: string) => (s ? " " : "") + c.toUpperCase()).replace(/ /g, ""));
  const [rw, setRw] = useState<string[]>([]);
  const save = () =>
    void act(
      on ? t("No se pudo montar") : t("No se pudo desmontar"),
      () => tp.call("cluster.volume.mount", on ? { name: v.name, enabled: true, share: smb ? name : "", comment: `Volumen del clúster ${v.name}`, rw: smb ? rw : [], ro: [] } : { name: v.name, enabled: false }),
      on ? t("Montando {name}…", { name: v.name }) : t("Desmontando {name}…", { name: v.name }),
    ).then((ok) => ok && onClose());
  return (
    <Sheet
      open
      onClose={onClose}
      width={480}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={on && smb && (!name || rw.length === 0)} onClick={save}>
            {on ? t("Aplicar") : t("Desmontar")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Montaje de {name}", { name: v.name })}</h2>
      <p className="tg-lead">{t("Se monta en /cluster/{name} de este NAS. Si este nodo se reinicia y otro del clúster está andando, el montaje se resuelve igual.", { name: v.name })}</p>
      <Row label={t("Montado en este NAS")}>
        <Toggle checked={on} onChange={setOn} label={t("Montado")} />
      </Row>
      {on && (
        <>
          <Row label={t("Compartir por SMB")} hint={t("Como una carpeta compartida más, con los usuarios de TangoOS.")}>
            <Toggle checked={smb} onChange={setSmb} label={t("Compartir por SMB")} />
          </Row>
          {smb && (
            <>
              <label className="tcl-field">
                <span>{t("Nombre de la carpeta")}</span>
                <TextInput value={name} onChange={(e) => setName(e.target.value)} />
              </label>
              <div className="tcl-field">
                <span>{t("Lectura y escritura")}</span>
                <div className="tcl-chips">
                  {users.map((u) => (
                    <button key={u.name} type="button" className="tcl-chip" aria-pressed={rw.includes(u.name)} onClick={() => setRw(rw.includes(u.name) ? rw.filter((x) => x !== u.name) : [...rw, u.name])}>
                      {u.name}
                    </button>
                  ))}
                </div>
              </div>
            </>
          )}
        </>
      )}
      {mounted && !on && <p className="tcl-note">{t("Se va a desmontar solo en este NAS; el volumen sigue andando en el clúster.")}</p>}
    </Sheet>
  );
}

function DeleteSheet({ v, mounted, onClose }: { v: ClusterVolume; mounted: boolean; onClose: () => void }) {
  const tp = useTransport();
  const act = useAct();
  const [typed, setTyped] = useState("");
  const remove = async () => {
    if (v.status === "Started" && !(await act(t("No se pudo detener"), () => tp.call("cluster.volume.action", { name: v.name, action: "stop" })))) return;
    if (await act(t("No se pudo borrar"), () => tp.call("cluster.volume.action", { name: v.name, action: "delete" }), t("{name} borrado", { name: v.name }))) onClose();
  };
  return (
    <Sheet
      open
      onClose={onClose}
      width={460}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="danger" disabled={typed !== v.name || mounted} onClick={() => void remove()}>
            {t("Borrar el volumen")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("¿Borrar {name}?", { name: v.name })}</h2>
      <p className="tg-lead">
        {mounted
          ? t("Primero desmontalo en este NAS (y en los demás nodos donde esté montado).")
          : t("Se detiene y se borra en todo el clúster. Los datos quedan en cada brick hasta que los borres desde «Bricks huérfanos» en cada nodo.")}
      </p>
      {!mounted && (
        <label className="tcl-field">
          <span>{t("Escribí {name} para confirmar", { name: v.name })}</span>
          <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} />
        </label>
      )}
    </Sheet>
  );
}

// ---- Crear volumen ---------------------------------------------------------------------

function CreateSheet({ st, onClose, onCreated }: { st: ClusterStatus; onClose: () => void; onCreated: (name: string) => void }) {
  const tp = useTransport();
  const act = useAct();
  const connected = st.nodes.filter((n) => n.connected);
  const [name, setName] = useState("");
  const [preset, setPreset] = useState(connected.length >= 3 ? "production-replica3" : "default");
  const [replica, setReplica] = useState(Math.min(3, Math.max(2, connected.length)));
  const [arbiter, setArbiter] = useState(false);
  const [start, setStart] = useState(true);
  const [picked, setPicked] = useState<Record<string, BrickChoice>>(() => Object.fromEntries(connected.slice(0, 3).map((n) => [n.host, defaultChoice(st, n)])));
  const hosts = Object.keys(picked);
  const nameOk = /^[a-z][a-z0-9_]{0,31}$/.test(name) && !st.volumes.some((v) => v.name === name);
  const countOk = hosts.length > 0 && hosts.length % replica === 0;
  const summary = replica > 1 ? `${hosts.length > replica ? "Distributed-Replicate" : "Replicate"} ${hosts.length / replica || 0} × ${replica} = ${hosts.length}` : t("Distribute · {n} bricks", { n: hosts.length });

  const create = () =>
    void act(
      t("No se pudo crear el volumen"),
      () =>
        tp.call("cluster.volume.create", {
          name,
          replica,
          arbiter: replica === 3 && arbiter,
          preset,
          start,
          bricks: connected.filter((n) => picked[n.host]).map((n) => choiceSpec(n.host, picked[n.host])),
        }),
      t("Creando {name}…", { name }),
    ).then((ok) => ok && onCreated(name));

  return (
    <Sheet
      open
      onClose={onClose}
      width={620}
      footer={
        <>
          <span className="tcl-grow tcl-sum">{summary}</span>
          <Button onClick={onClose}>{t("Cancelar")}</Button>
          <Button variant="primary" disabled={!nameOk || !countOk} onClick={create}>
            {t("Crear")}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">{t("Crear un volumen del clúster")}</h2>
      <label className="tcl-field">
        <span>{t("Nombre (minúsculas, números y _)")}</span>
        <TextInput autoFocus value={name} placeholder="gv_documentos" onChange={(e) => setName(e.target.value.toLowerCase())} />
      </label>

      <div className="tcl-field">
        <span>{t("Ajustes")}</span>
        <div className="tcl-cards">
          {st.presets.map((p) => (
            <button
              key={p.id}
              type="button"
              className="tcl-card"
              aria-pressed={preset === p.id}
              onClick={() => {
                setPreset(p.id);
                if (p.replica) setReplica(p.replica);
              }}
            >
              <b>{tr(p.name)}</b>
              <small>{tr(p.desc)}</small>
              {Object.keys(p.options).length > 0 && <small className="tcl-muted">{tn(Object.keys(p.options).length, "Una opción", "{n} opciones")}</small>}
            </button>
          ))}
        </div>
      </div>

      <Row label={t("Copias de cada archivo")} hint={t("Réplica 3 sigue andando con un nodo apagado y no queda en split-brain.")}>
        <Segmented
          label={t("Réplica")}
          value={String(replica) as "2" | "3" | "4"}
          onChange={(x) => setReplica(Number(x))}
          options={[
            { value: "2", label: "2" },
            { value: "3", label: "3" },
            { value: "4", label: "4" },
          ]}
        />
      </Row>
      {replica === 3 && (
        <Row label={t("Árbitro")} hint={t("El tercer brick guarda solo metadatos: decide en caso de empate y ocupa casi nada.")}>
          <Toggle checked={arbiter} onChange={setArbiter} label={t("Árbitro")} />
        </Row>
      )}

      <div className="tcl-field">
        <span>{t("Bricks: un nodo por copia. Carpeta en un volumen de TangoOS, o un volumen dedicado (XFS) en un grupo de almacenamiento; thin permite instantáneas")}</span>
        <div className="tcl-bricks">
          {connected.map((n) => (
            <BrickPicker
              key={n.host}
              st={st}
              node={n}
              vol={name}
              value={picked[n.host]}
              onChange={(c) => {
                const next = { ...picked };
                if (c) next[n.host] = c;
                else delete next[n.host];
                setPicked(next);
              }}
            />
          ))}
        </div>
        {!countOk && <small className="tcl-warn">{t("Con réplica {n}, elegí {n} nodos (o un múltiplo).", { n: replica })}</small>}
      </div>
      <Row label={t("Arrancar al crearlo")}>
        <Toggle checked={start} onChange={setStart} label={t("Arrancar")} />
      </Row>
      <p className="tcl-note">
        <Layers size={12} /> {t("Los volúmenes dedicados se crean en cada nodo al crear el volumen del clúster.")} <Share2 size={12} /> {t("Después podés montarlo y compartirlo por SMB o NFS.")}
      </p>
    </Sheet>
  );
}
