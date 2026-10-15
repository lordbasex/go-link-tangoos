// Clúster GlusterFS simulado (protocol/README.md §16). Datos DEMO: con ?scenario=cluster
// arranca con 3 nodos y volúmenes (uno sano, uno curándose con un nodo lento y uno con
// split-brain); en los demás escenarios GlusterFS no está instalado.
import { RpcError } from "../protocol";
import type { Job, Method, Notice, Params, Result, Scenario, Topic, Topics, Volume } from "../protocol";
import type { ClusterBrick, ClusterNode, ClusterPreset, ClusterStatus, ClusterVolume } from "../protocol-cluster";

export interface MockClusterCtx {
  scenario: () => Scenario;
  emit: <T extends Topic>(topic: T, d: Topics[T]) => void;
  startJob: (type: string, title: string, detail: string, progress: number, step: number, rate?: string, onDone?: () => void) => Job & { onTick?: (p: number) => void };
  notify: (level: Notice["level"], app: string, title: string, body: string) => void;
  volumes: () => Volume[];
}

const GB = 1024 ** 3;

const PRESETS: ClusterPreset[] = [
  { id: "default", name: "Por defecto", desc: "Los valores de GlusterFS, sin cambios.", options: {} },
  {
    id: "production-replica3",
    name: "Producción réplica 3",
    replica: 3,
    desc: "Quórum de servidores y de clientes, detección de caídas en 10 s, curación granular y sin cachés del cliente: cada nodo ve los cambios al instante.",
    options: {
      "cluster.heal-timeout": "10",
      "cluster.shd-wait-qlength": "512",
      "cluster.shd-max-threads": "2",
      "performance.open-behind": "off",
      "performance.quick-read": "off",
      "performance.read-ahead": "off",
      "performance.write-behind": "off",
      "diagnostics.client-log-level": "WARNING",
      "diagnostics.brick-log-level": "WARNING",
      "network.ping-timeout": "10",
      "cluster.server-quorum-type": "server",
      "cluster.quorum-type": "auto",
      "cluster.granular-entry-heal": "on",
      "storage.fips-mode-rchecksum": "on",
      "transport.address-family": "inet",
      "performance.client-io-threads": "on",
      "cluster.server-quorum-ratio": "51%",
    },
  },
];

// Orden en que GlusterFS lista las opciones en `volume info` (el de la captura real).
const PROD_ORDER = [
  "cluster.quorum-type", "cluster.server-quorum-type", "network.ping-timeout", "diagnostics.brick-log-level", "diagnostics.client-log-level",
  "performance.write-behind", "performance.read-ahead", "performance.quick-read", "performance.open-behind", "cluster.shd-max-threads",
  "cluster.shd-wait-qlength", "cluster.heal-timeout", "cluster.granular-entry-heal", "storage.fips-mode-rchecksum", "transport.address-family",
  "nfs.disable", "performance.client-io-threads", "cluster.server-quorum-ratio",
];

const OPTIONS: ClusterStatus["options"] = [
  { key: "cluster.quorum-type", type: "enum", enum: ["auto", "fixed", "none"], desc: "Quórum de clientes: con auto, se escribe solo si responde la mayoría de las copias" },
  { key: "cluster.server-quorum-type", type: "enum", enum: ["server", "none"], desc: "Quórum de servidores: un nodo sin mayoría apaga sus bricks" },
  { key: "cluster.server-quorum-ratio", type: "percent", min: 1, max: 100, global: true, desc: "Porcentaje de nodos para tener quórum de servidores (todo el clúster)" },
  { key: "cluster.heal-timeout", type: "int", min: 5, max: 86400, desc: "Segundos entre rondas del demonio de curación" },
  { key: "cluster.shd-max-threads", type: "int", min: 1, max: 64, desc: "Hilos de curación por brick" },
  { key: "cluster.shd-wait-qlength", type: "int", min: 1, max: 655536, desc: "Entradas en espera del demonio de curación" },
  { key: "cluster.granular-entry-heal", type: "bool", desc: "Curación por entrada (más rápida en directorios grandes)" },
  { key: "cluster.self-heal-daemon", type: "bool", desc: "Demonio de curación automática" },
  { key: "cluster.favorite-child-policy", type: "enum", enum: ["none", "size", "ctime", "mtime", "majority"], desc: "Cómo resolver un split-brain sin intervención" },
  { key: "network.ping-timeout", type: "int", min: 1, max: 1800, desc: "Segundos sin respuesta para dar un nodo por caído" },
  { key: "performance.client-io-threads", type: "bool", desc: "Hilos de E/S en el cliente" },
  { key: "performance.open-behind", type: "bool", desc: "Caché de aperturas" },
  { key: "performance.quick-read", type: "bool", desc: "Lectura rápida de archivos chicos" },
  { key: "performance.read-ahead", type: "bool", desc: "Lectura anticipada" },
  { key: "performance.write-behind", type: "bool", desc: "Escritura diferida" },
  { key: "diagnostics.brick-log-level", type: "enum", enum: ["CRITICAL", "ERROR", "WARNING", "INFO", "DEBUG", "TRACE", "NONE"], desc: "Nivel de log de los bricks" },
  { key: "diagnostics.client-log-level", type: "enum", enum: ["CRITICAL", "ERROR", "WARNING", "INFO", "DEBUG", "TRACE", "NONE"], desc: "Nivel de log de los clientes" },
  { key: "storage.fips-mode-rchecksum", type: "bool", desc: "Sumas de control compatibles con FIPS al curar" },
  { key: "transport.address-family", type: "enum", enum: ["inet", "inet6"], desc: "IPv4 o IPv6 entre nodos" },
  { key: "features.shard", type: "bool", desc: "Partir archivos grandes (imágenes de VM)" },
];

const uuid = (seed: string) => {
  let h = 0;
  for (const c of seed) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  const hex = (n: number) => (h ^ (n * 2654435761)).toString(16).padStart(8, "0").slice(0, 8);
  return `${hex(1)}-${hex(2).slice(0, 4)}-4${hex(3).slice(0, 3)}-a${hex(4).slice(0, 3)}-${hex(5)}${hex(6).slice(0, 4)}`;
};

export function clusterHandlers(ctx: MockClusterCtx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  const demo = ctx.scenario() === "cluster";
  const self = "tango";
  let installed = demo;
  const nodes: ClusterNode[] = demo
    ? [
        { uuid: uuid("tango-b"), host: "tango-b", hostnames: ["tango-b"], connected: true, state: "Peer in Cluster", self: false, latencyMs: 1 },
        { uuid: uuid("tango-c"), host: "tango-c", hostnames: ["tango-c"], connected: true, state: "Peer in Cluster", self: false, latencyMs: 38 },
        { uuid: uuid(self), host: self, connected: true, state: "Este nodo", self: true, latencyMs: 0 },
      ]
    : [];
  const brick = (host: string, vol: string, tv = "volume1", over: Partial<ClusterBrick> = {}): ClusterBrick => ({
    host, path: `/${tv}/@gluster/${vol}`, hostUuid: uuid(host), online: true, port: 49152 + Math.floor(Math.random() * 900), pid: 1000 + Math.floor(Math.random() * 9000),
    sizeTotal: 7.2 * 1024 * GB, sizeFree: 5.1 * 1024 * GB, fsName: "btrfs", healPending: 0, splitBrain: 0, ...over,
  });
  const prodOpts = { ...PRESETS[1].options, "nfs.disable": "on" };
  const mkVol = (name: string, bricks: ClusterBrick[], opts: Record<string, string>, order: string[], replica = 3): ClusterVolume => {
    const v: ClusterVolume = {
      name, id: uuid(name), status: "Started", type: bricks.length > replica ? "Distributed-Replicate" : "Replicate", replica, arbiter: 0, sets: bricks.length / replica,
      bricks, options: opts, optOrder: order, sizeTotal: 0, sizeFree: 0, healPending: 0, splitBrain: 0, online: 0,
    };
    return recompute(v);
  };
  const recompute = (v: ClusterVolume) => {
    const on = v.bricks.filter((b) => b.online);
    v.online = v.status === "Started" ? on.length : 0;
    const small = on.reduce((m, b) => Math.min(m, b.sizeTotal ?? Infinity), Infinity);
    const free = on.reduce((m, b) => Math.min(m, b.sizeFree ?? Infinity), Infinity);
    v.sizeTotal = Number.isFinite(small) ? small * v.sets : 0;
    v.sizeFree = Number.isFinite(free) ? free * v.sets : 0;
    v.healPending = v.bricks.reduce((s, b) => s + Math.max(0, b.healPending), 0);
    v.splitBrain = v.bricks.reduce((s, b) => s + b.splitBrain, 0);
    return v;
  };
  let volumes: ClusterVolume[] = demo
    ? [
        mkVol("gv_tel_config", ["tango", "tango-b", "tango-c"].map((h) => brick(h, "gv_tel_config")), { ...prodOpts }, PROD_ORDER),
        mkVol("gv_tel_locuciones", ["tango", "tango-b", "tango-c"].map((h, i) => brick(h, "gv_tel_locuciones", "volume1", i < 2 ? { healPending: 214 } : {})), { ...prodOpts }, PROD_ORDER),
        mkVol("gv_backups", ["tango", "tango-b", "tango-c", "tango", "tango-b", "tango-c"].map((h, i) => brick(h, "gv_backups", i < 3 ? "volume1" : "volume2", i === 1 ? { splitBrain: 2 } : {})), { "cluster.quorum-type": "auto", "nfs.disable": "on" }, ["cluster.quorum-type", "nfs.disable"]),
      ]
    : [];
  let mounts: ClusterStatus["mounts"] = demo ? [{ name: "gv_tel_locuciones", path: "/cluster/gv_tel_locuciones", mounted: true, share: "Locuciones" }] : [];
  let invites: ClusterStatus["invites"] = [];
  let orphans: ClusterStatus["orphans"] = demo ? [{ tango_volume: "volume2", name: "gv_viejo", path: "/volume2/@gluster/gv_viejo" }] : [];

  // La curación avanza sola en la demo.
  if (demo)
    setInterval(() => {
      const v = volumes.find((x) => x.name === "gv_tel_locuciones");
      if (!v || v.healPending === 0) return;
      for (const b of v.bricks) if (b.healPending > 0) b.healPending = Math.max(0, b.healPending - 7 - Math.floor(Math.random() * 9));
      recompute(v);
      ctx.emit("cluster.changed", status());
    }, 4000);

  const allowed = () => [...new Set([...invites.flatMap((i) => i.addrs), ...nodes.filter((n) => !n.self).map((n) => `192.168.1.${10 + nodes.indexOf(n)}`)])];
  const status = (): ClusterStatus => ({
    installed, running: installed, version: installed ? "11.1" : undefined, self, selfAddrs: ["192.168.1.10"],
    nodes: installed ? (nodes.length ? nodes : [{ uuid: uuid(self), host: self, connected: true, state: "Este nodo", self: true, latencyMs: 0 }]) : [],
    volumes: structuredClone(volumes), orphans: [...orphans], mounts: [...mounts], allowed: installed ? allowed() : [], invites: [...invites],
    tangoVolumes: ctx.volumes().filter((v) => v.mount).map((v) => ({ name: v.name, mount: v.mount ?? "", free: v.size - v.used })),
    presets: PRESETS, options: OPTIONS,
  });
  const changed = () => ctx.emit("cluster.changed", status());
  const vol = (name: string) => {
    const v = volumes.find((x) => x.name === name);
    if (!v) throw new RpcError("CLUSTER_VOLUME_NOT_FOUND", `no existe el volumen ${name}`);
    return v;
  };
  const need = () => {
    if (!installed) throw new RpcError("NOT_INSTALLED", "GlusterFS no está instalado");
  };
  const ensureSelf = () => {
    if (!nodes.length) nodes.push({ uuid: uuid(self), host: self, connected: true, state: "Este nodo", self: true, latencyMs: 0 });
  };

  return {
    "cluster.status": () => status(),
    "cluster.install": () => {
      const j = ctx.startJob("cluster.install", "Instalando GlusterFS", "DEMO", 0, 0.12, undefined, () => {
        installed = true;
        ensureSelf();
        changed();
        ctx.notify("ok", "tango.cluster", "GlusterFS listo", "Este NAS ya puede formar un clúster. Los puertos quedan cerrados salvo para los nodos que invites.");
      });
      return { job: j.id };
    },
    "cluster.invite": ({ host }) => {
      need();
      const inv = { host, addrs: [/^\d/.test(host) ? host : "192.168.1.40"], expires: new Date(Date.now() + 15 * 60_000).toISOString() };
      invites = [...invites.filter((i) => i.host !== host), inv];
      changed();
      return inv;
    },
    "cluster.invite.cancel": ({ host }) => {
      invites = invites.filter((i) => i.host !== host);
      changed();
      return {};
    },
    "cluster.peer.add": ({ host }) => {
      need();
      ensureSelf();
      if (nodes.some((n) => n.host === host)) throw new RpcError("OPERATION_FAILED", `GlusterFS: Host ${host} is already in the peer list`);
      if (/rechaz|deny/.test(host)) throw new RpcError("PEER_UNREACHABLE", `${host} no respondió: invitá a este NAS desde la app Clúster de ${host} y probá de nuevo`);
      nodes.splice(nodes.length - 1, 0, { uuid: uuid(host), host, hostnames: [host], connected: true, state: "Peer in Cluster", self: false, latencyMs: 2 });
      changed();
      return {};
    },
    "cluster.peer.remove": ({ host }) => {
      if (volumes.some((v) => v.bricks.some((b) => b.host === host)))
        throw new RpcError("OPERATION_FAILED", `GlusterFS: Peer ${host} hosts one or more bricks. If the peer is in not recoverable state then use either replace-brick or remove-brick command with force to remove all bricks from the peer and attempt the peer detach again.`);
      const i = nodes.findIndex((n) => n.host === host);
      if (i >= 0) nodes.splice(i, 1);
      changed();
      return {};
    },
    "cluster.volume.create": (p) => {
      need();
      if (volumes.some((v) => v.name === p.name)) throw new RpcError("OPERATION_FAILED", `GlusterFS: Volume ${p.name} already exists`);
      const preset = PRESETS.find((x) => x.id === p.preset);
      const opts = { ...(preset?.options ?? {}), ...(p.options ?? {}) };
      const j = ctx.startJob("cluster.volume.create", `Creando el volumen ${p.name}`, p.bricks.length > p.replica ? "Distributed-Replicate" : "Replicate", 0, 0.25, undefined, () => {
        const bricks = p.bricks.map((b, i) => brick(b.host, p.name, b.tangoVolume, { arbiter: !!p.arbiter && i % 3 === 2, sizeFree: 7.1 * 1024 * GB }));
        const v = mkVol(p.name, bricks, opts, Object.keys(opts), p.replica);
        v.arbiter = p.arbiter ? 1 : 0;
        if (!p.start) {
          v.status = "Created";
          recompute(v);
        }
        volumes = [...volumes, v];
        changed();
      });
      return { job: j.id };
    },
    "cluster.volume.action": ({ name, action }) => {
      const v = vol(name);
      if (action !== "start" && mounts.some((m) => m.name === name && m.mounted)) throw new RpcError("BUSY", "el volumen está montado en este nodo: desmontalo primero");
      if (action === "delete") {
        if (v.status === "Started") throw new RpcError("OPERATION_FAILED", "GlusterFS: Volume " + name + " has been started.Volume needs to be stopped before deletion.");
        volumes = volumes.filter((x) => x !== v);
        orphans = [...orphans, ...v.bricks.filter((b) => b.host === self).map((b) => ({ tango_volume: b.path.split("/")[1], name, path: b.path }))];
      } else {
        v.status = action === "start" ? "Started" : "Stopped";
        recompute(v);
      }
      changed();
      return {};
    },
    "cluster.volume.options": ({ name, set, reset }) => {
      const v = vol(name);
      for (const [k, val] of Object.entries(set)) {
        if (!OPTIONS.some((o) => o.key === k)) throw new RpcError("INVALID_ARGS", `opción no permitida: "${k}"`);
        v.options[k] = val;
        if (!v.optOrder?.includes(k)) v.optOrder = [...(v.optOrder ?? []), k];
      }
      for (const k of reset) {
        delete v.options[k];
        v.optOrder = (v.optOrder ?? []).filter((x) => x !== k);
      }
      changed();
      return {};
    },
    "cluster.volume.heal": ({ name }) => {
      const v = vol(name);
      for (const b of v.bricks) if (b.healPending > 0) b.healPending = Math.floor(b.healPending / 2);
      recompute(v);
      changed();
      return {};
    },
    "cluster.volume.info": ({ name }) => {
      const v = vol(name);
      return { text: "" + v.name }; // la app arma el texto con clusterInfoText
    },
    "cluster.volume.mount": ({ name, enabled, share }) => {
      vol(name);
      const j = ctx.startJob("cluster.volume.mount", enabled ? `Montando ${name}` : `Desmontando ${name}`, `/cluster/${name}`, 0, 0.4, undefined, () => {
        mounts = mounts.filter((m) => m.name !== name);
        if (enabled) mounts = [...mounts, { name, path: `/cluster/${name}`, mounted: true, share: share || undefined }];
        changed();
      });
      return { job: j.id };
    },
    "cluster.brick.cleanup": ({ tangoVolume, name }) => {
      orphans = orphans.filter((o) => !(o.name === name && o.tango_volume === tangoVolume));
      changed();
      return {};
    },
  };
}
