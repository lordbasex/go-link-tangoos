// Clúster GlusterFS (protocol/README.md §17, ADR 0012): tipos de cluster.*.

export interface ClusterBrick {
  host: string;
  path: string;
  hostUuid: string;
  arbiter?: boolean;
  online: boolean;
  port?: number;
  pid?: number;
  sizeTotal?: number;
  sizeFree?: number;
  fsName?: string;
  /** -1 si el brick no respondió a la consulta de curación. */
  healPending: number;
  splitBrain: number;
  healStatus?: string;
  /** dir: carpeta en un volumen de TangoOS · lv: volumen dedicado con XFS · snap: restaurado de una instantánea. */
  kind?: "dir" | "lv" | "snap";
}

export interface ClusterVolume {
  name: string;
  id: string;
  /** Created, Started, Stopped */
  status: string;
  /** Replicate, Distributed-Replicate, Distribute */
  type: string;
  replica: number;
  arbiter: number;
  sets: number;
  bricks: ClusterBrick[];
  options: Record<string, string>;
  optOrder: string[] | null;
  sizeTotal: number;
  sizeFree: number;
  healPending: number;
  splitBrain: number;
  online: number;
  /** TLS de E/S (client.ssl y server.ssl on). */
  tls: boolean;
  /** Cuotas por directorio activadas. */
  quota: boolean;
  /** Solo la demo: instantáneas (el real lo trae cluster.volume.snapshot). */
  snapshotCount?: number;
}

export interface ClusterNode {
  uuid: string;
  host: string;
  hostnames?: string[];
  connected: boolean;
  state: string;
  self: boolean;
  /** -1 sin respuesta. */
  latencyMs: number;
}

export interface ClusterInvite {
  host: string;
  addrs: string[];
  expires: string;
  /** Código de unión que se escribe en el otro NAS (ABCDE-FGHJK). */
  code?: string;
  /** Huella de la clave de este NAS: el otro la muestra para comparar. */
  fingerprint?: string;
  /** Huella de la CA del clúster al que se unió por esta invitación. */
  joined?: string;
}

export interface ClusterOrphan {
  /** lv: volumen dedicado (tango_volume es el grupo). */
  kind?: "lv";
  tango_volume: string;
  name: string;
  path: string;
}

export interface ClusterMount {
  name: string;
  path: string;
  mounted: boolean;
  share?: string;
}

export interface ClusterPreset {
  id: string;
  name: string;
  desc: string;
  replica?: number;
  options: Record<string, string>;
}

export interface ClusterOption {
  key: string;
  type: "bool" | "int" | "enum" | "percent";
  min?: number;
  max?: number;
  enum?: string[];
  global?: boolean;
  desc: string;
}

export interface ClusterTLS {
  ca: boolean;
  caFingerprint: string;
  signed: boolean;
  fingerprint: string;
  files: boolean;
  /** TLS de management prendido en este nodo. */
  secure: boolean;
}

export interface ClusterNodeInfo {
  cn: string;
  fingerprint: string;
  names: string[];
  secure: boolean;
  tangoVolumes: { name: string; mount: string; free: number }[];
  pools: { name: string; free: number }[];
}

export interface ClusterLVBrick {
  vol: string;
  pool: string;
  size: number;
  thin: boolean;
  mounted: boolean;
  snaps: number;
}

export interface NFSClientRule {
  host: string;
  access: "rw" | "ro";
}

export interface ClusterStatus {
  installed: boolean;
  running: boolean;
  version?: string;
  error?: string;
  self: string;
  selfAddrs: string[] | null;
  nodes: ClusterNode[];
  volumes: ClusterVolume[];
  orphans: ClusterOrphan[];
  mounts: ClusterMount[];
  allowed: string[];
  invites: ClusterInvite[];
  tangoVolumes: { name: string; mount: string; free: number }[];
  presets: ClusterPreset[];
  options: ClusterOption[];
  tls: ClusterTLS;
  /** Lo que cuenta cada nodo por el canal de control (vacío sin certificados). */
  nodeInfo: Record<string, ClusterNodeInfo>;
  /** Volumen → equipos con acceso NFS. */
  nfs: Record<string, NFSClientRule[]>;
  lvBricks: ClusterLVBrick[];
  pools: { name: string; free: number }[];
}

/** Un brick pedido desde la UI: carpeta en un volumen o volumen dedicado (XFS) en un grupo. */
export interface ClusterBrickSpec {
  host: string;
  kind: "dir" | "lv";
  tangoVolume?: string;
  pool?: string;
  size?: number;
  thin?: boolean;
}

export interface ClusterTaskNode {
  node: string;
  files: number;
  size: number;
  lookups: number;
  failures: number;
  skipped: number;
  status: string;
  runtime: number;
}

export interface ClusterTask {
  id: string;
  status: string;
  done: boolean;
  failed: boolean;
  total: ClusterTaskNode;
  nodes: ClusterTaskNode[];
}

export interface ClusterQuota {
  path: string;
  limit: number;
  softPercent: number;
  used: number;
  avail: number;
  softOver: boolean;
  hardOver: boolean;
}

export interface ClusterSnapshot {
  name: string;
  uuid: string;
  desc?: string;
  created: string;
  status: string;
}

export interface ClusterCreate {
  name: string;
  replica: number;
  arbiter?: boolean;
  preset?: string;
  options?: Record<string, string>;
  bricks: ClusterBrickSpec[];
  start: boolean;
}

type Empty = Record<string, never>;

declare module "./protocol" {
  interface Methods {
    "cluster.status": { params: void; result: ClusterStatus };
    "cluster.install": { params: void; result: { job: string } };
    "cluster.invite": { params: { host: string }; result: ClusterInvite };
    "cluster.invite.cancel": { params: { host: string }; result: Empty };
    "cluster.peer.prepare": { params: { host: string; code: string }; result: { id: string; host: string; cn: string; fingerprint: string } };
    "cluster.peer.confirm": { params: { id: string; certify?: boolean }; result: Empty };
    "cluster.tls.set": { params: { enabled: boolean }; result: { job: string } };
    "cluster.volume.bricks.add": { params: { name: string; replica?: number; bricks: ClusterBrickSpec[] }; result: { job: string } };
    "cluster.volume.bricks.replace": { params: { name: string; from: { host: string; path: string }; to: ClusterBrickSpec }; result: { job: string } };
    "cluster.volume.bricks.remove": { params: { name: string; replica?: number; bricks: { host: string; path: string }[]; phase: "start" | "status" | "commit" }; result: { task: ClusterTask | null } };
    "cluster.volume.rebalance": { params: { name: string; action: "start" | "status" | "stop" }; result: { task: ClusterTask | null } };
    "cluster.volume.quota": { params: { name: string; op: "enable" | "disable" | "limit" | "remove" | "list"; path?: string; limit?: number }; result: { quotas: ClusterQuota[] } };
    "cluster.volume.snapshot": { params: { name: string; op: "create" | "delete" | "restore" | "info"; snap?: string; desc?: string }; result: { snapshots: ClusterSnapshot[] } };
    "cluster.volume.nfs": { params: { name: string; clients: NFSClientRule[] }; result: { job: string } };
    "cluster.peer.remove": { params: { host: string }; result: Empty };
    "cluster.volume.create": { params: ClusterCreate; result: { job: string } };
    "cluster.volume.action": { params: { name: string; action: "start" | "stop" | "delete" }; result: Empty };
    "cluster.volume.options": { params: { name: string; set: Record<string, string>; reset: string[] }; result: Empty };
    "cluster.volume.heal": { params: { name: string; full?: boolean }; result: Empty };
    "cluster.volume.info": { params: { name: string }; result: { text: string } };
    "cluster.volume.mount": { params: { name: string; enabled: boolean; share?: string; comment?: string; rw?: string[]; ro?: string[] }; result: { job: string } };
    "cluster.brick.cleanup": { params: { kind?: "lv"; tangoVolume: string; name: string }; result: Empty };
  }
  interface Topics {
    "cluster.changed": ClusterStatus;
  }
}

/** Texto de `gluster volume info` (mismo formato que el CLI; ver internal/gluster.InfoText). */
export function clusterInfoText(v: ClusterVolume): string {
  const lines = [`Volume Name: ${v.name}`, `Type: ${v.type}`, `Volume ID: ${v.id}`, `Status: ${v.status}`, `Snapshot Count: ${v.snapshotCount ?? 0}`];
  const n = v.bricks.length;
  if (v.arbiter > 0) lines.push(`Number of Bricks: ${v.sets} x (${v.replica - v.arbiter} + ${v.arbiter}) = ${n}`);
  else if (v.replica > 1) lines.push(`Number of Bricks: ${v.sets} x ${v.replica} = ${n}`);
  else lines.push(`Number of Bricks: ${n}`);
  lines.push("Transport-type: tcp", "Bricks:");
  v.bricks.forEach((b, i) => lines.push(`Brick${i + 1}: ${b.host}:${b.path}${b.arbiter ? " (arbiter)" : ""}`));
  const order = v.optOrder ?? [];
  if (order.length) {
    lines.push("Options Reconfigured:");
    for (const k of order) lines.push(`${k}: ${v.options[k]}`);
  }
  return lines.join("\n");
}

/** Estado de un volumen para la UI. */
export function clusterVolumeTone(v: ClusterVolume): "ok" | "warn" | "crit" | "info" {
  if (v.splitBrain > 0) return "crit";
  if (v.status !== "Started") return "info";
  if (v.online < v.bricks.length || v.healPending > 0) return "warn";
  return "ok";
}
