// Clúster: nodos TangoOS unidos con GlusterFS y sus volúmenes replicados
// (protocol/README.md §17, docs/17-cluster.md).
import "./cluster.css";
import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, Boxes, Camera, Database, FolderOpen, Gauge, HardDrive, Layers, Lock, LockOpen, Network, Play, Plus, RefreshCw, Server, Settings2, Share2, ShieldCheck, Square, Terminal, Trash2, UserPlus } from "lucide-react";
import { RpcError, clusterInfoText, clusterVolumeTone, fmtBytes, useIsReal, useLive, useTransport } from "@tango/client";
import type { ClusterNode, ClusterStatus, ClusterVolume } from "@tango/client";
import { WindowToolbar, toast, useWindowMenus } from "@tango/shell";
import type { AppProps } from "@tango/shell";
import { Badge, Button, DemoBadge, EmptyState, Group, IconButton, Progress, Row, Segmented, Sheet, SidebarItem, Status, TextInput, Toggle } from "@tango/ui";
import { AddNodeSheet, BrickPicker, BricksSheet, InviteSheet, NFSSheet, QuotaSheet, SecurityGroup, SnapshotsSheet, TLSSheet, choiceSpec, defaultChoice } from "./Stage2";
import type { BrickChoice } from "./Stage2";

const SIDE = 230;
const APP = "tango.cluster";

type View = { kind: "overview" } | { kind: "nodes" } | { kind: "volume"; name: string };

const errText = (e: unknown) => (e instanceof RpcError ? e.message : String(e));

function useAct() {
  const t = useTransport();
  return async (title: string, fn: () => Promise<unknown>, ok?: string) => {
    try {
      await fn();
      if (ok) toast({ level: "ok", title: ok, app: APP });
      return true;
    } catch (e) {
      toast({ level: "warn", title, body: errText(e), app: APP });
      return false;
    } finally {
      void t;
    }
  };
}

const volLabel = (v: ClusterVolume) => {
  if (v.arbiter > 0) return `${v.type} ${v.sets} × (${v.replica - v.arbiter} + ${v.arbiter})`;
  if (v.replica > 1) return `${v.type} ${v.sets} × ${v.replica}`;
  return `${v.type} · ${v.bricks.length} bricks`;
};

const volState = (v: ClusterVolume): string => {
  if (v.splitBrain > 0) return `Split-brain en ${v.splitBrain} archivos`;
  if (v.status === "Created") return "Creado, sin arrancar";
  if (v.status === "Stopped") return "Detenido";
  if (v.online < v.bricks.length) return `${v.online} de ${v.bricks.length} bricks en línea`;
  if (v.healPending > 0) return `Curando ${v.healPending} entradas`;
  return "En orden";
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
        label: "Clúster",
        items: [
          { label: "Invitar un nodo…", disabled: !st?.running, action: () => setSheet("invite") },
          { label: "Agregar un nodo…", disabled: !st?.running, action: () => setSheet("add") },
          { label: "Crear volumen…", disabled: !st?.running, action: () => setSheet("create") },
        ],
      },
    ],
    [st?.running],
  );

  const current = view.kind === "volume" ? st?.volumes.find((v) => v.name === view.name) : undefined;
  const title = view.kind === "nodes" ? "Nodos" : current ? current.name : "Clúster";

  return (
    <div className="tg-app tcl" style={{ ["--side" as string]: `${SIDE}px` }}>
      <aside className="tg-app-side tg-sidebar">
        <h4>Clúster</h4>
        <SidebarItem icon={Boxes} label="Resumen" current={view.kind === "overview"} onClick={() => setView({ kind: "overview" })} />
        <SidebarItem icon={Server} label="Nodos" current={view.kind === "nodes"} meta={st?.nodes.length || undefined} onClick={() => setView({ kind: "nodes" })} disabled={!st?.installed} />
        {!!st?.volumes.length && <h4>Volúmenes</h4>}
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
              <IconButton icon={UserPlus} label="Invitar un nodo" onClick={() => setSheet("invite")} />
              <IconButton icon={Network} label="Agregar un nodo" onClick={() => setSheet("add")} />
              <Button onClick={() => setSheet("create")}>
                <Plus size={14} /> Crear volumen
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
  const t = useTransport();
  const act = useAct();
  const [busy, setBusy] = useState(false);
  const install = () => {
    setBusy(true);
    void act("No se pudo instalar GlusterFS", () => t.call("cluster.install", undefined), "Instalando GlusterFS…").finally(() => setBusy(false));
  };
  return (
    <EmptyState
      icon={Boxes}
      title={st.installed ? "GlusterFS no está corriendo" : "Uní varios TangoOS en un clúster"}
      action={
        <Button variant="primary" onClick={install} disabled={busy}>
          {st.installed ? "Arrancar GlusterFS" : "Instalar GlusterFS"}
        </Button>
      }
    >
      {st.installed
        ? (st.error ?? "glusterd no responde.")
        : "Con GlusterFS, cada archivo se guarda a la vez en varios NAS: si uno se apaga, los demás siguen sirviendo los datos. Se instala solo en este equipo (unos 31 MB) y los puertos quedan cerrados salvo para los nodos que invites."}
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
        <Stat icon={Server} label="Nodos" value={`${connected} de ${st.nodes.length}`} tone={connected === st.nodes.length ? "ok" : "warn"} hint={connected === st.nodes.length ? "Todos conectados" : "Hay nodos sin conexión"} />
        <Stat icon={Database} label="Volúmenes" value={String(st.volumes.length)} tone={issues.length ? "warn" : "ok"} hint={issues.length ? `${issues.length} con avisos` : "Todos en orden"} />
        <Stat icon={ShieldCheck} label="Firewall" value={`${st.allowed.length} permitidos`} tone="info" hint="Solo estos equipos llegan a GlusterFS" />
        <Stat icon={st.tls.secure ? Lock : LockOpen} label="Entre nodos" value={st.tls.secure ? "Con TLS" : "Sin cifrar"} tone={st.tls.secure ? "ok" : "warn"} hint={st.tls.secure ? "Certificados de la CA del clúster" : "Prendelo desde Nodos"} />
      </div>

      {st.volumes.some((v) => v.splitBrain > 0) && (
        <div className="tcl-alert" data-tone="crit">
          <AlertTriangle size={16} />
          <span>Hay archivos en split-brain: cada copia tiene una versión distinta y GlusterFS no puede elegir sola. Abrí el volumen para ver dónde.</span>
        </div>
      )}

      {st.nodes.length === 1 ? (
        <EmptyState icon={Network} title="Este NAS todavía está solo" action={<Button onClick={onAdd}>Agregar un nodo…</Button>}>
          Para sumar otro TangoOS: en ese equipo abrí Clúster, tocá «Invitar un nodo» y escribí la IP de este NAS ({st.selfAddrs?.[0] ?? st.self}). Después agregalo desde acá.
        </EmptyState>
      ) : st.volumes.length === 0 ? (
        <EmptyState icon={Database} title="Sin volúmenes del clúster" action={<Button onClick={onCreate}>Crear volumen…</Button>}>
          Un volumen replicado guarda una copia completa en cada nodo que elijas.
        </EmptyState>
      ) : (
        <Group title="Volúmenes">
          {st.volumes.map((v) => (
            <Row key={v.name} icon={<Database size={16} />} label={<b>{v.name}</b>} hint={`${volLabel(v)} · ${v.sizeTotal ? `${fmtBytes(v.sizeTotal - v.sizeFree)} de ${fmtBytes(v.sizeTotal)}` : "sin datos de espacio"}`}>
              <Status tone={clusterVolumeTone(v)}>{volState(v)}</Status>
              <Button variant="plain" onClick={() => open(v.name)}>
                Abrir
              </Button>
            </Row>
          ))}
        </Group>
      )}

      {st.orphans.length > 0 && <Orphans st={st} />}
      <p className="tcl-foot">GlusterFS {st.version} · este nodo: {st.self}</p>
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
  const t = useTransport();
  const act = useAct();
  const [confirm, setConfirm] = useState<string | null>(null);
  return (
    <Group title="Bricks huérfanos en este nodo">
      {st.orphans.map((o) => (
        <Row key={o.path} icon={<HardDrive size={16} />} label={o.path} hint={o.kind === "lv" ? `Volumen dedicado del brick de ${o.name} que ya no usa ningún brick. Ocupa espacio en el grupo ${o.tango_volume}.` : `Quedaron del volumen ${o.name}, que ya no existe. Ocupan espacio en ${o.tango_volume}.`}>
          {confirm === o.path ? (
            <Button variant="danger" onClick={() => void act("No se pudo borrar", () => t.call("cluster.brick.cleanup", { kind: o.kind, tangoVolume: o.tango_volume, name: o.name }), `Borrado ${o.path}`).then(() => setConfirm(null))}>
              Borrar los datos
            </Button>
          ) : (
            <Button onClick={() => setConfirm(o.path)}>Borrar…</Button>
          )}
        </Row>
      ))}
    </Group>
  );
}

// ---- Nodos -----------------------------------------------------------------------------

function latencyText(n: ClusterNode) {
  if (n.self) return "—";
  if (!n.connected || n.latencyMs < 0) return "sin respuesta";
  return `${n.latencyMs} ms`;
}

function Nodes({ st, onInvite, onAdd, onTLS, onCertify }: { st: ClusterStatus; onInvite: () => void; onAdd: () => void; onTLS: () => void; onCertify: (host: string) => void }) {
  const t = useTransport();
  const act = useAct();
  const [removing, setRemoving] = useState<string | null>(null);
  const bricksOn = (host: string) => st.volumes.reduce((s, v) => s + v.bricks.filter((b) => b.host === host).length, 0);
  return (
    <div className="tcl-pad">
      <table className="tcl-table">
        <thead>
          <tr>
            <th>Nodo</th>
            <th>Estado</th>
            <th>Latencia</th>
            <th>Certificado</th>
            <th>Bricks</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {st.nodes.map((n) => (
            <tr key={n.uuid}>
              <td>
                <b>{n.host}</b>
                {n.self && <Badge>Este NAS</Badge>}
              </td>
              <td>
                <Status tone={n.connected ? "ok" : "crit"}>{n.self ? "En línea" : n.connected ? "Conectado" : "Sin conexión"}</Status>
              </td>
              <td className="tcl-num" data-slow={!n.self && n.latencyMs > 20 ? true : undefined}>
                {latencyText(n)}
              </td>
              <td>{st.nodeInfo[n.host] ? <Badge tone="ok">{st.nodeInfo[n.host].cn}</Badge> : <Badge tone="warn">Sin certificado</Badge>}</td>
              <td className="tcl-num">{bricksOn(n.host)}</td>
              <td className="tcl-actions">
                {!n.self &&
                  (removing === n.host ? (
                    <Button variant="danger" onClick={() => void act("No se pudo sacar el nodo", () => t.call("cluster.peer.remove", { host: n.host }), `${n.host} salió del clúster`).then(() => setRemoving(null))}>
                      Sacar del clúster
                    </Button>
                  ) : (
                    <IconButton icon={Trash2} label={`Sacar ${n.host}`} disabled={bricksOn(n.host) > 0} onClick={() => setRemoving(n.host)} />
                  ))}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="tcl-note">Un nodo con bricks no se puede sacar: primero reemplazá o quitá sus bricks desde cada volumen. Al sacarlo, sale del firewall de todos y de la lista TLS de cada volumen.</p>

      <SecurityGroup st={st} onTLS={onTLS} onCertify={onCertify} />

      <Group title="Invitaciones abiertas en este NAS">
        {st.invites.length === 0 ? (
          <Row label="Ninguna" hint="Invitá a un equipo para que pueda sumar a este NAS a su clúster.">
            <Button onClick={onInvite}>Invitar…</Button>
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
                  {i.joined && <Badge tone="ok">Unido</Badge>}
                </span>
              }
              hint={`${i.addrs.join(", ")} · vence ${new Date(i.expires).toLocaleTimeString("es-AR", { hour: "2-digit", minute: "2-digit", hourCycle: "h23" })}${i.fingerprint ? ` · huella ${i.fingerprint}` : ""}`}
            >
              <Button variant="plain" onClick={() => void act("No se pudo cancelar", () => t.call("cluster.invite.cancel", { host: i.host }))}>
                Cancelar
              </Button>
            </Row>
          ))
        )}
      </Group>
      <Group title="Equipos que pueden hablar con GlusterFS">
        <Row label={st.allowed.length ? st.allowed.join(" · ") : "Solo este NAS"} hint="Los nodos del clúster y las invitaciones abiertas. Para cualquier otro equipo de la red, los puertos de GlusterFS están cerrados.">
          <Button variant="plain" onClick={onAdd}>
            Agregar un nodo…
          </Button>
        </Row>
      </Group>
    </div>
  );
}

// ---- Volumen ---------------------------------------------------------------------------

function VolumeView({ st, v }: { st: ClusterStatus; v: ClusterVolume }) {
  const t = useTransport();
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
    void act(a === "start" ? "No se pudo arrancar" : "No se pudo detener", () => t.call("cluster.volume.action", { name: v.name, action: a }), a === "start" ? `${v.name} arrancado` : `${v.name} detenido`);

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
            {volLabel(v)} · {v.status === "Started" ? "Arrancado" : v.status === "Stopped" ? "Detenido" : "Creado"}
            {mount?.mounted && ` · montado en ${mount.path}`}
            {mount?.share && ` · carpeta SMB «${mount.share}»`}
            {st.nfs[v.name]?.length ? ` · NFS para ${st.nfs[v.name].map((r) => r.host).join(", ")}` : ""}
            {v.tls ? " · TLS" : ""}
          </span>
        </div>
        <div className="tcl-vbtns">
          {v.status === "Started" ? <IconButton icon={Square} label="Detener" onClick={() => action("stop")} /> : <IconButton icon={Play} label="Arrancar" onClick={() => action("start")} />}
          <IconButton icon={RefreshCw} label="Curar ahora" disabled={v.status !== "Started" || v.replica < 2} onClick={() => void act("No se pudo lanzar la curación", () => t.call("cluster.volume.heal", { name: v.name }), "Curación lanzada")} />
          <IconButton icon={Settings2} label="Opciones" onClick={() => setSheet("options")} />
          <IconButton icon={HardDrive} label="Bricks: agregar, quitar, reemplazar, rebalancear" onClick={() => setSheet("bricks")} />
          <IconButton icon={Gauge} label="Cuotas" onClick={() => setSheet("quota")} />
          <IconButton icon={Camera} label="Instantáneas" onClick={() => setSheet("snaps")} />
          <IconButton icon={Network} label="NFS" onClick={() => setSheet("nfs")} />
          <IconButton icon={Terminal} label="Ver como gluster volume info" pressed={showInfo} onClick={() => setShowInfo(!showInfo)} />
          <Button onClick={() => setSheet("mount")}>
            <FolderOpen size={13} /> {mount?.mounted ? "Montaje y SMB…" : "Montar…"}
          </Button>
          <IconButton icon={Trash2} label="Borrar el volumen" onClick={() => setSheet("delete")} />
        </div>
      </div>

      {v.sizeTotal > 0 && (
        <div className="tcl-cap">
          <Progress value={used} tone={used > 0.9 ? "crit" : used > 0.8 ? "warn" : undefined} />
          <span>
            {fmtBytes(v.sizeTotal - v.sizeFree)} usados de {fmtBytes(v.sizeTotal)} · {fmtBytes(v.sizeFree)} libres
          </span>
        </div>
      )}

      {v.splitBrain > 0 && (
        <div className="tcl-alert" data-tone="crit">
          <AlertTriangle size={16} />
          <span>
            {v.splitBrain} archivos tienen versiones distintas en cada copia. Revisalos con <code>gluster volume heal {v.name} info split-brain</code> y elegí la copia buena, o fijá la opción «cluster.favorite-child-policy».
          </span>
        </div>
      )}
      {v.healPending > 0 && v.splitBrain === 0 && (
        <div className="tcl-alert" data-tone="warn">
          <RefreshCw size={16} />
          <span>Faltan copiar {v.healPending} entradas a todas las réplicas. Pasa después de que un nodo estuvo apagado; se completa sola.</span>
        </div>
      )}

      {showInfo ? (
        <pre className="tcl-info" aria-label="Salida de gluster volume info">{clusterInfoText(v)}</pre>
      ) : (
        sets.map((set, i) => (
          <Group key={i} title={sets.length > 1 ? `Conjunto de réplica ${i + 1}` : "Bricks"}>
            {set.map((b) => (
              <Row
                key={b.host + b.path}
                icon={<HardDrive size={16} />}
                label={
                  <span>
                    <b>{b.host}</b>
                    <span className="tcl-path">{b.path}</span>
                    {b.arbiter && <Badge>Árbitro</Badge>}
                    {b.kind === "lv" && <Badge tone="info">Volumen dedicado</Badge>}
                    {b.kind === "snap" && <Badge tone="info">Restaurado</Badge>}
                  </span>
                }
                hint={
                  b.online
                    ? `${b.port ? `puerto ${b.port} · ` : ""}${b.sizeFree != null && b.sizeTotal ? `${fmtBytes(b.sizeFree)} libres de ${fmtBytes(b.sizeTotal)}` : ""}${b.fsName ? ` · ${b.fsName}` : ""}`
                    : (b.healStatus ?? "El nodo no responde")
                }
              >
                {b.splitBrain > 0 && <Badge tone="crit">{b.splitBrain} en split-brain</Badge>}
                {b.healPending > 0 && <Badge tone="warn">{b.healPending} por curar</Badge>}
                <Status tone={b.online ? "ok" : "crit"}>{b.online ? "En línea" : "Fuera de línea"}</Status>
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
  const t = useTransport();
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
    void act("No se pudieron cambiar las opciones", () => t.call("cluster.volume.options", { name: v.name, set, reset }), "Opciones guardadas").then((ok) => ok && onClose());
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
          <span className="tcl-grow">{changed.length ? `${changed.length} cambios` : "Sin cambios"}</span>
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="primary" disabled={!changed.length} onClick={save}>
            Guardar
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">Opciones de {v.name}</h2>
      <div className="tcl-presets">
        {st.presets
          .filter((p) => Object.keys(p.options).length)
          .map((p) => (
            <Button key={p.id} variant="plain" onClick={() => applyPreset(p.id)}>
              Aplicar «{p.name}»
            </Button>
          ))}
      </div>
      <div className="tcl-opts">
        {st.options.map((o) => (
          <div key={o.key} className="tcl-opt" data-changed={(vals[o.key] ?? "") !== (v.options[o.key] ?? "") || undefined}>
            <div>
              <code>{o.key}</code>
              <small>
                {o.desc}
                {o.global && " · vale para todo el clúster"}
              </small>
            </div>
            {o.type === "bool" ? (
              <Segmented
                label={o.key}
                value={(vals[o.key] ?? "") as "" | "on" | "off"}
                onChange={(x) => setVals({ ...vals, [o.key]: x })}
                options={[
                  { value: "", label: "Defecto" },
                  { value: "on", label: "on" },
                  { value: "off", label: "off" },
                ]}
              />
            ) : o.type === "enum" ? (
              <select className="tg-select" value={vals[o.key] ?? ""} onChange={(e) => setVals({ ...vals, [o.key]: e.target.value })} aria-label={o.key}>
                <option value="">Defecto</option>
                {o.enum?.map((x) => (
                  <option key={x}>{x}</option>
                ))}
              </select>
            ) : (
              <TextInput value={vals[o.key] ?? ""} placeholder="Defecto" aria-label={o.key} onChange={(e) => setVals({ ...vals, [o.key]: e.target.value })} style={{ width: 110 }} />
            )}
          </div>
        ))}
      </div>
    </Sheet>
  );
}

function MountSheet({ v, mounted, share, onClose }: { v: ClusterVolume; mounted: boolean; share?: string; onClose: () => void }) {
  const t = useTransport();
  const act = useAct();
  const users = useLive("users.list", undefined, ["users.changed"]).data ?? [];
  const [on, setOn] = useState(true);
  const [smb, setSmb] = useState(!!share);
  const [name, setName] = useState(share ?? v.name.replace(/^gv_/, "").replace(/(^|_)(\w)/g, (_, s: string, c: string) => (s ? " " : "") + c.toUpperCase()).replace(/ /g, ""));
  const [rw, setRw] = useState<string[]>([]);
  const save = () =>
    void act(
      on ? "No se pudo montar" : "No se pudo desmontar",
      () => t.call("cluster.volume.mount", on ? { name: v.name, enabled: true, share: smb ? name : "", comment: `Volumen del clúster ${v.name}`, rw: smb ? rw : [], ro: [] } : { name: v.name, enabled: false }),
      on ? `Montando ${v.name}…` : `Desmontando ${v.name}…`,
    ).then((ok) => ok && onClose());
  return (
    <Sheet
      open
      onClose={onClose}
      width={480}
      footer={
        <>
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="primary" disabled={on && smb && (!name || rw.length === 0)} onClick={save}>
            {on ? "Aplicar" : "Desmontar"}
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">Montaje de {v.name}</h2>
      <p className="tg-lead">Se monta en /cluster/{v.name} de este NAS. Si este nodo se reinicia y otro del clúster está andando, el montaje se resuelve igual.</p>
      <Row label="Montado en este NAS">
        <Toggle checked={on} onChange={setOn} label="Montado" />
      </Row>
      {on && (
        <>
          <Row label="Compartir por SMB" hint="Como una carpeta compartida más, con los usuarios de TangoOS.">
            <Toggle checked={smb} onChange={setSmb} label="Compartir por SMB" />
          </Row>
          {smb && (
            <>
              <label className="tcl-field">
                <span>Nombre de la carpeta</span>
                <TextInput value={name} onChange={(e) => setName(e.target.value)} />
              </label>
              <div className="tcl-field">
                <span>Lectura y escritura</span>
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
      {mounted && !on && <p className="tcl-note">Se va a desmontar solo en este NAS; el volumen sigue andando en el clúster.</p>}
    </Sheet>
  );
}

function DeleteSheet({ v, mounted, onClose }: { v: ClusterVolume; mounted: boolean; onClose: () => void }) {
  const t = useTransport();
  const act = useAct();
  const [typed, setTyped] = useState("");
  const remove = async () => {
    if (v.status === "Started" && !(await act("No se pudo detener", () => t.call("cluster.volume.action", { name: v.name, action: "stop" })))) return;
    if (await act("No se pudo borrar", () => t.call("cluster.volume.action", { name: v.name, action: "delete" }), `${v.name} borrado`)) onClose();
  };
  return (
    <Sheet
      open
      onClose={onClose}
      width={460}
      footer={
        <>
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="danger" disabled={typed !== v.name || mounted} onClick={() => void remove()}>
            Borrar el volumen
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">¿Borrar {v.name}?</h2>
      <p className="tg-lead">
        {mounted
          ? "Primero desmontalo en este NAS (y en los demás nodos donde esté montado)."
          : "Se detiene y se borra en todo el clúster. Los datos quedan en cada brick hasta que los borres desde «Bricks huérfanos» en cada nodo."}
      </p>
      {!mounted && (
        <label className="tcl-field">
          <span>Escribí {v.name} para confirmar</span>
          <TextInput value={typed} onChange={(e) => setTyped(e.target.value)} />
        </label>
      )}
    </Sheet>
  );
}

// ---- Crear volumen ---------------------------------------------------------------------

function CreateSheet({ st, onClose, onCreated }: { st: ClusterStatus; onClose: () => void; onCreated: (name: string) => void }) {
  const t = useTransport();
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
  const summary = replica > 1 ? `${hosts.length > replica ? "Distributed-Replicate" : "Replicate"} ${hosts.length / replica || 0} × ${replica} = ${hosts.length}` : `Distribute · ${hosts.length} bricks`;

  const create = () =>
    void act(
      "No se pudo crear el volumen",
      () =>
        t.call("cluster.volume.create", {
          name,
          replica,
          arbiter: replica === 3 && arbiter,
          preset,
          start,
          bricks: connected.filter((n) => picked[n.host]).map((n) => choiceSpec(n.host, picked[n.host])),
        }),
      `Creando ${name}…`,
    ).then((ok) => ok && onCreated(name));

  return (
    <Sheet
      open
      onClose={onClose}
      width={620}
      footer={
        <>
          <span className="tcl-grow tcl-sum">{summary}</span>
          <Button onClick={onClose}>Cancelar</Button>
          <Button variant="primary" disabled={!nameOk || !countOk} onClick={create}>
            Crear
          </Button>
        </>
      }
    >
      <h2 className="tg-h2">Crear un volumen del clúster</h2>
      <label className="tcl-field">
        <span>Nombre (minúsculas, números y _)</span>
        <TextInput autoFocus value={name} placeholder="gv_documentos" onChange={(e) => setName(e.target.value.toLowerCase())} />
      </label>

      <div className="tcl-field">
        <span>Ajustes</span>
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
              <b>{p.name}</b>
              <small>{p.desc}</small>
              {Object.keys(p.options).length > 0 && <small className="tcl-muted">{Object.keys(p.options).length} opciones</small>}
            </button>
          ))}
        </div>
      </div>

      <Row label="Copias de cada archivo" hint="Réplica 3 sigue andando con un nodo apagado y no queda en split-brain.">
        <Segmented
          label="Réplica"
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
        <Row label="Árbitro" hint="El tercer brick guarda solo metadatos: decide en caso de empate y ocupa casi nada.">
          <Toggle checked={arbiter} onChange={setArbiter} label="Árbitro" />
        </Row>
      )}

      <div className="tcl-field">
        <span>Bricks: un nodo por copia. Carpeta en un volumen de TangoOS, o un volumen dedicado (XFS) en un grupo de almacenamiento; thin permite instantáneas</span>
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
        {!countOk && <small className="tcl-warn">Con réplica {replica}, elegí {replica} nodos (o un múltiplo).</small>}
      </div>
      <Row label="Arrancar al crearlo">
        <Toggle checked={start} onChange={setStart} label="Arrancar" />
      </Row>
      <p className="tcl-note">
        <Layers size={12} /> Los volúmenes dedicados se crean en cada nodo al crear el volumen del clúster. <Share2 size={12} /> Después podés montarlo y compartirlo por SMB o NFS.
      </p>
    </Sheet>
  );
}
