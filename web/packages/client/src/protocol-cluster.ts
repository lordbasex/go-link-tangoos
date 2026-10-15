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
}

export interface ClusterOrphan {
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
}

export interface ClusterCreate {
  name: string;
  replica: number;
  arbiter?: boolean;
  preset?: string;
  options?: Record<string, string>;
  bricks: { host: string; tangoVolume: string }[];
  start: boolean;
}

type Empty = Record<string, never>;

declare module "./protocol" {
  interface Methods {
    "cluster.status": { params: void; result: ClusterStatus };
    "cluster.install": { params: void; result: { job: string } };
    "cluster.invite": { params: { host: string }; result: ClusterInvite };
    "cluster.invite.cancel": { params: { host: string }; result: Empty };
    "cluster.peer.add": { params: { host: string }; result: Empty };
    "cluster.peer.remove": { params: { host: string }; result: Empty };
    "cluster.volume.create": { params: ClusterCreate; result: { job: string } };
    "cluster.volume.action": { params: { name: string; action: "start" | "stop" | "delete" }; result: Empty };
    "cluster.volume.options": { params: { name: string; set: Record<string, string>; reset: string[] }; result: Empty };
    "cluster.volume.heal": { params: { name: string; full?: boolean }; result: Empty };
    "cluster.volume.info": { params: { name: string }; result: { text: string } };
    "cluster.volume.mount": { params: { name: string; enabled: boolean; share?: string; comment?: string; rw?: string[]; ro?: string[] }; result: { job: string } };
    "cluster.brick.cleanup": { params: { tangoVolume: string; name: string }; result: Empty };
  }
  interface Topics {
    "cluster.changed": ClusterStatus;
  }
}

/** Texto de `gluster volume info` (mismo formato que el CLI; ver internal/gluster.InfoText). */
export function clusterInfoText(v: ClusterVolume): string {
  const lines = [`Volume Name: ${v.name}`, `Type: ${v.type}`, `Volume ID: ${v.id}`, `Status: ${v.status}`, "Snapshot Count: 0"];
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
