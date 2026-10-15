// Clúster GlusterFS simulado (protocol/README.md §17). Datos DEMO: con ?scenario=cluster
// arranca con 3 nodos y volúmenes (uno sano, uno curándose con un nodo lento y uno con
// split-brain); en los demás escenarios GlusterFS no está instalado.
import { RpcError } from "../protocol";
import type { Job, Method, Notice, Params, Result, Scenario, Topic, Topics, Volume } from "../protocol";
import type { ClusterBrick, ClusterNode, ClusterPreset, ClusterQuota, ClusterSnapshot, ClusterStatus, ClusterTask, ClusterVolume } from "../protocol-cluster";

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
  const hex = (n: number) => ((h ^ Math.imul(n, 2654435761)) >>> 0).toString(16).padStart(8, "0");
  return `${hex(1)}-${hex(2).slice(0, 4)}-4${hex(3).slice(0, 3)}-a${hex(4).slice(0, 3)}-${hex(5)}${hex(6).slice(0, 4)}`;
};

export function clusterHandlers(ctx: MockClusterCtx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  const self = "tango";
  let installed = false;
  const nodes: ClusterNode[] = [];
  let volumes: ClusterVolume[] = [];
  let mounts: ClusterStatus["mounts"] = [];
  let invites: ClusterStatus["invites"] = [];
  let orphans: ClusterStatus["orphans"] = [];
  let secure = false;
  let nfs: ClusterStatus["nfs"] = {};
  const quotas: Record<string, ClusterQuota[]> = {};
  const snaps: Record<string, ClusterSnapshot[]> = {};
  const joins: Record<string, { host: string }> = {};
  const tasks: Record<string, { progress: number; files: number }> = {};
  const fp = (seed: string) => (uuid(seed).replace(/-/g, "").toUpperCase().slice(0, 32).match(/.{4}/g) ?? []).join(" ");
  // El escenario se lee en la primera llamada: MockTransport arma los handlers antes
  // de asignar el suyo.
  let seeded = false;
  const seed = () => {
    if (seeded) return;
    seeded = true;
    if (ctx.scenario() !== "cluster") return;
    installed = true;
    secure = true;
    nodes.push(...demoNodes());
    snaps.gv_tel_config = [
      { name: "antes-de-migrar", uuid: uuid("s1"), desc: "Antes de migrar la central", created: "2026-10-12 22:10:04 +0000", status: "Stopped" },
      { name: "diaria-1015", uuid: uuid("s2"), created: "2026-10-15 03:00:01 +0000", status: "Stopped" },
    ];
    volumes = demoVolumes();
    quotas.gv_tel_locuciones = [
      { path: "/Grabaciones", limit: 2 * 1024 * GB, softPercent: 80, used: 1.74 * 1024 * GB, avail: 0.26 * 1024 * GB, softOver: true, hardOver: false },
      { path: "/Mensajes", limit: 200 * GB, softPercent: 80, used: 31 * GB, avail: 169 * GB, softOver: false, hardOver: false },
    ];
    nfs = { gv_tel_locuciones: [{ host: "192.168.1.0/24", access: "ro" }] };
    mounts = [{ name: "gv_tel_locuciones", path: "/cluster/gv_tel_locuciones", mounted: true, share: "Locuciones" }];
    orphans = [{ tango_volume: "volume2", name: "gv_viejo", path: "/volume2/@gluster/gv_viejo" }];
    // La curación avanza sola en la demo.
    setInterval(() => {
      const v = volumes.find((x) => x.name === "gv_tel_locuciones");
      if (!v || v.healPending === 0) return;
      for (const b of v.bricks) if (b.healPending > 0) b.healPending = Math.max(0, b.healPending - 7 - Math.floor(Math.random() * 9));
      recompute(v);
      ctx.emit("cluster.changed", status());
    }, 4000);
  };
  const demoNodes = (): ClusterNode[] => [
        { uuid: uuid("tango-b"), host: "tango-b", hostnames: ["tango-b"], connected: true, state: "Peer in Cluster", self: false, latencyMs: 1 },
        { uuid: uuid("tango-c"), host: "tango-c", hostnames: ["tango-c"], connected: true, state: "Peer in Cluster", self: false, latencyMs: 38 },
        { uuid: uuid(self), host: self, connected: true, state: "Este nodo", self: true, latencyMs: 0 },
      ];
  const brick = (host: string, vol: string, tv = "volume1", over: Partial<ClusterBrick> = {}): ClusterBrick => ({
    kind: "dir", host, path: `/${tv}/@gluster/${vol}`, hostUuid: uuid(host), online: true, port: 49152 + Math.floor(Math.random() * 900), pid: 1000 + Math.floor(Math.random() * 9000),
    sizeTotal: 7.2 * 1024 * GB, sizeFree: 5.1 * 1024 * GB, fsName: "btrfs", healPending: 0, splitBrain: 0, ...over,
  });
  const prodOpts = { ...PRESETS[1].options, "nfs.disable": "on", "client.ssl": "on", "server.ssl": "on", "auth.ssl-allow": "tango,tango-b,tango-c" };
  const mkVol = (name: string, bricks: ClusterBrick[], opts: Record<string, string>, order: string[], replica = 3): ClusterVolume => {
    const v: ClusterVolume = {
      name, id: uuid(name), status: "Started", type: bricks.length > replica ? "Distributed-Replicate" : "Replicate", replica, arbiter: 0, sets: bricks.length / replica,
      bricks, options: opts, optOrder: order, sizeTotal: 0, sizeFree: 0, healPending: 0, splitBrain: 0, online: 0, tls: false, quota: false,
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
    v.tls = v.options["client.ssl"] === "on" && v.options["server.ssl"] === "on";
    v.quota = v.options["features.quota"] === "on";
    v.snapshotCount = snaps[v.name]?.length ?? 0;
    return v;
  };
  const demoVolumes = (): ClusterVolume[] => [
        mkVol("gv_tel_config", ["tango", "tango-b", "tango-c"].map((h) => brick(h, "gv_tel_config", "", { kind: "lv", path: "/srv/gluster/gv_tel_config/brick", fsName: "xfs", sizeTotal: 100 * GB, sizeFree: 92.5 * GB })), { ...prodOpts }, PROD_ORDER),
        mkVol("gv_tel_locuciones", ["tango", "tango-b", "tango-c"].map((h, i) => brick(h, "gv_tel_locuciones", "volume1", i < 2 ? { healPending: 214 } : {})), { ...prodOpts, "features.quota": "on" }, PROD_ORDER),
        mkVol("gv_backups", ["tango", "tango-b", "tango-c", "tango", "tango-b", "tango-c"].map((h, i) => brick(h, "gv_backups", i < 3 ? "volume1" : "volume2", i === 1 ? { splitBrain: 2 } : {})), { "cluster.quorum-type": "auto", "nfs.disable": "on" }, ["cluster.quorum-type", "nfs.disable"]),
      ];

  const allowed = () => [...new Set([...invites.flatMap((i) => i.addrs), ...nodes.filter((n) => !n.self).map((n) => `192.168.1.${10 + nodes.indexOf(n)}`)])];
  const status = (): ClusterStatus => ({
    installed, running: installed, version: installed ? "11.1" : undefined, self, selfAddrs: ["192.168.1.10"],
    nodes: installed ? (nodes.length ? nodes : [{ uuid: uuid(self), host: self, connected: true, state: "Este nodo", self: true, latencyMs: 0 }]) : [],
    volumes: structuredClone(volumes), orphans: [...orphans], mounts: [...mounts], allowed: installed ? allowed() : [], invites: [...invites],
    tangoVolumes: ctx.volumes().filter((v) => v.mount).map((v) => ({ name: v.name, mount: v.mount ?? "", free: v.size - v.used })),
    presets: PRESETS, options: OPTIONS,
    tls: { ca: secure, caFingerprint: secure ? fp("ca") : "", signed: secure, fingerprint: fp(self), files: secure, secure },
    nodeInfo: Object.fromEntries(
      (installed ? nodes : []).map((n) => [
        n.host,
        { cn: n.host, fingerprint: fp(n.host), names: [n.host], secure, tangoVolumes: [{ name: "volume1", mount: "/volume1", free: 5.1 * 1024 * GB }], pools: [{ name: "data", free: 1.2 * 1024 * GB }] },
      ]),
    ),
    nfs: { ...nfs },
    lvBricks: volumes.filter((v) => v.bricks.some((b) => b.kind === "lv" && b.host === self)).map((v) => ({ vol: v.name, pool: "data", size: 100 * GB, thin: true, mounted: true, snaps: 0 })),
    pools: [{ name: "data", free: 1.2 * 1024 * GB }],
  });
  const changed = () => ctx.emit("cluster.changed", status());
  const demoTask = (t: { progress: number; files: number }): ClusterTask => {
    const done = t.progress >= 1;
    const st = done ? "completed" : "in progress";
    const n = (node: string, k: number) => ({ node, files: Math.round(t.files * k), size: Math.round(t.files * k * 2.1e6), lookups: Math.round(t.files * k * 1.4), failures: 0, skipped: 0, status: st, runtime: Math.round(t.progress * 180) });
    return { id: uuid("task"), status: st, done, failed: false, total: n("total", 1), nodes: [n(self, 0.4), n("tango-b", 0.35), n("tango-c", 0.25)] };
  };
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

  const handlers: { [M in Method]?: (p: Params<M>) => Result<M> } = {
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
      const inv = { host, addrs: [/^\d/.test(host) ? host : "192.168.1.40"], expires: new Date(Date.now() + 15 * 60_000).toISOString(), code: "K7QWM-4RZTP", fingerprint: fp(self) };
      invites = [...invites.filter((i) => i.host !== host), inv];
      changed();
      return inv;
    },
    "cluster.invite.cancel": ({ host }) => {
      invites = invites.filter((i) => i.host !== host);
      changed();
      return {};
    },
    "cluster.peer.prepare": ({ host, code }) => {
      need();
      ensureSelf();
      if (/rechaz|deny/.test(host)) throw new RpcError("PEER_UNREACHABLE", `${host} no responde en el puerto 24010: invitalo primero desde la app Clúster de ${host}`);
      if (code.replace(/[-\s]/g, "").length !== 10) throw new RpcError("JOIN_CODE", `el código no coincide con el que muestra ${host}`);
      const id = `j${Object.keys(joins).length + 1}`;
      joins[id] = { host };
      return { id, host, cn: host, fingerprint: fp(host) };
    },
    "cluster.peer.confirm": ({ id, certify }) => {
      const j = joins[id];
      if (!j) throw new RpcError("JOIN_EXPIRED", "la unión venció: empezá de nuevo");
      delete joins[id];
      if (!certify && !nodes.some((n) => n.host === j.host)) nodes.splice(nodes.length - 1, 0, { uuid: uuid(j.host), host: j.host, hostnames: [j.host], connected: true, state: "Peer in Cluster", self: false, latencyMs: 2 });
      changed();
      return {};
    },
    "cluster.tls.set": ({ enabled }) => {
      need();
      const j = ctx.startJob("cluster.tls", enabled ? "Prendiendo TLS en el clúster" : "Apagando TLS en el clúster", "Los volúmenes se detienen un momento", 0, 0.2, undefined, () => {
        secure = enabled;
        for (const v of volumes) {
          v.options["client.ssl"] = enabled ? "on" : "off";
          v.options["server.ssl"] = enabled ? "on" : "off";
          if (enabled) v.options["auth.ssl-allow"] = nodes.map((n) => n.host).sort().join(",");
          else delete v.options["auth.ssl-allow"];
          recompute(v);
        }
        changed();
      });
      return { job: j.id };
    },
    "cluster.volume.bricks.add": ({ name, bricks, replica }) => {
      const v = vol(name);
      const j = ctx.startJob("cluster.bricks.add", `Agregando bricks a ${name}`, "DEMO", 0, 0.3, undefined, () => {
        v.bricks.push(...bricks.map((b) => (b.kind === "lv" ? brick(b.host, name, "", { kind: "lv", path: `/srv/gluster/${name}/brick`, fsName: "xfs" }) : brick(b.host, name, b.tangoVolume || "volume1"))));
        if (replica) v.replica = replica;
        v.sets = v.bricks.length / v.replica;
        v.type = v.sets > 1 ? "Distributed-Replicate" : "Replicate";
        recompute(v);
        changed();
      });
      return { job: j.id };
    },
    "cluster.volume.bricks.replace": ({ name, from, to }) => {
      const v = vol(name);
      const j = ctx.startJob("cluster.bricks.replace", `Reemplazando un brick de ${name}`, `${from.host}:${from.path}`, 0, 0.35, undefined, () => {
        const i = v.bricks.findIndex((b) => b.host === from.host && b.path === from.path);
        if (i >= 0)
          v.bricks[i] =
            to.kind === "lv"
              ? brick(to.host, name, "", { kind: "lv", path: `/srv/gluster/${name}/brick`, fsName: "xfs", healPending: 120 })
              : brick(to.host, name, to.tangoVolume || "volume1", { healPending: 120 });
        recompute(v);
        changed();
      });
      return { job: j.id };
    },
    "cluster.volume.bricks.remove": ({ name, bricks, phase, replica }) => {
      const v = vol(name);
      const key = `rm:${name}`;
      if (phase === "start") tasks[key] = { progress: 0, files: 0 };
      if (phase === "commit" || replica) {
        v.bricks = v.bricks.filter((b) => !bricks.some((x) => x.host === b.host && x.path === b.path));
        if (replica) v.replica = replica;
        v.sets = Math.max(1, v.bricks.length / v.replica);
        v.type = v.sets > 1 ? "Distributed-Replicate" : "Replicate";
        recompute(v);
        delete tasks[key];
        changed();
        return { task: null };
      }
      const t = tasks[key];
      if (!t) return { task: null };
      t.progress = Math.min(1, t.progress + 0.34);
      t.files += 412;
      return { task: demoTask(t) };
    },
    "cluster.volume.rebalance": ({ name, action }) => {
      vol(name);
      const key = `rb:${name}`;
      if (action === "start") tasks[key] = { progress: 0, files: 0 };
      if (action === "stop") delete tasks[key];
      const t = tasks[key];
      if (!t) return { task: null };
      if (action === "status") {
        t.progress = Math.min(1, t.progress + 0.25);
        t.files += 1380;
      }
      return { task: demoTask(t) };
    },
    "cluster.volume.quota": ({ name, op, path, limit }) => {
      const v = vol(name);
      quotas[name] ??= [];
      if (op === "enable") v.options["features.quota"] = "on";
      if (op === "disable") {
        delete v.options["features.quota"];
        quotas[name] = [];
      }
      if (op === "limit" && path && limit) quotas[name] = [...quotas[name].filter((q) => q.path !== path), { path, limit, softPercent: 80, used: 0, avail: limit, softOver: false, hardOver: false }];
      if (op === "remove") quotas[name] = quotas[name].filter((q) => q.path !== path);
      recompute(v);
      if (op !== "list") changed();
      return { quotas: v.quota ? [...quotas[name]] : [] };
    },
    "cluster.volume.snapshot": ({ name, op, snap, desc }) => {
      const v = vol(name);
      snaps[name] ??= [];
      if (op !== "info" && !v.bricks.every((b) => b.kind === "lv")) throw new RpcError("SNAPSHOT_UNSUPPORTED", "las instantáneas necesitan que todos los bricks sean volúmenes thin dedicados");
      if (op === "create" && snap) snaps[name] = [...snaps[name], { name: snap, uuid: uuid(snap), desc, created: new Date().toISOString().replace("T", " ").slice(0, 19) + " +0000", status: "Stopped" }];
      if (op === "delete") snaps[name] = snaps[name].filter((x) => x.name !== snap);
      if (op === "restore") {
        if (v.status === "Started") throw new RpcError("VOLUME_STARTED", `para restaurar, primero detené el volumen ${name}`);
        snaps[name] = snaps[name].filter((x) => x.name !== snap);
      }
      recompute(v);
      if (op !== "info") changed();
      return { snapshots: [...snaps[name]] };
    },
    "cluster.volume.nfs": ({ name, clients }) => {
      vol(name);
      const j = ctx.startJob("cluster.volume.nfs", `Exportando ${name} por NFS`, `/cluster/${name}`, 0, 0.5, undefined, () => {
        const next = { ...nfs };
        if (clients.length) next[name] = clients;
        else delete next[name];
        nfs = next;
        changed();
      });
      return { job: j.id };
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
        orphans = [
          ...orphans,
          ...v.bricks
            .filter((b) => b.host === self)
            .map((b) => (b.kind === "lv" ? { kind: "lv" as const, tango_volume: "data", name, path: `/srv/gluster/${name}` } : { tango_volume: b.path.split("/")[1], name, path: b.path })),
        ];
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
  // Cada handler siembra la demo antes de responder.
  for (const k of Object.keys(handlers) as Method[]) {
    const h = handlers[k] as (p: unknown) => unknown;
    (handlers as Record<string, (p: unknown) => unknown>)[k] = (p) => {
      seed();
      return h(p);
    };
  }
  return handlers;
}
