// Tipos del protocolo tango/1. Escritos a mano para el prototipo.
// TODO(codegen): se generan desde /protocol/schema (docs/03 §5, docs/10).

export type Bytes = number;
export type Health = "ok" | "warn" | "crit";
/** Health de un disco: "unknown" cuando el hardware no expone SMART (p. ej. discos virtuales). */
export type DiskHealth = Health | "unknown";

export interface SystemInfo {
  hostname: string;
  model: string;
  cpu: string;
  cores: number;
  memTotal: Bytes;
  version: string;
  debian: string;
  kernel: string;
  ip: string;
  uptimeSec: number;
  serial: string;
}

export type DiskKind = "hdd" | "ssd" | "nvme";
export type DiskUse = "system" | "member" | "free" | "failed";

export interface Disk {
  id: string; // WWN o by-id: identidad estable, nunca /dev/sdX
  dev: string; // solo informativo
  bay: number | null; // null = NVMe interno / sistema
  model: string;
  serial: string;
  firmware: string;
  size: Bytes;
  kind: DiskKind;
  rpm?: number;
  tempC: number | null;
  powerOnHours: number | null;
  smart: DiskHealth;
  use: DiskUse;
  volume?: string;
  reallocated: number;
  pending: number;
  /** Disco que viene de un Synology (escenario migrate). */
  foreign?: { vendor: "synology"; label: string; profile: string };
}

export type VolumeProfile = "basic" | "raid1" | "raid5" | "raid6" | "raid10";
export type VolumeState = "healthy" | "degraded" | "building" | "scrubbing" | "readonly";

export interface Volume {
  id: string;
  name: string;
  mount: string;
  profile: VolumeProfile;
  fs: "btrfs";
  size: Bytes;
  used: Bytes;
  state: VolumeState;
  disks: string[];
  progress?: number; // 0..1 para building/scrubbing
  lastScrub: string | null; // ISO; null o "" = nunca
  growth30d: number[]; // uso relativo, 30 puntos
}

export interface Share {
  id: string;
  name: string;
  volume: string;
  path: string;
  size: Bytes;
  description: string;
  smb: boolean;
  nfs: boolean;
  snapshots: boolean;
  recycleBin: boolean;
  color: string;
  /** Destino de Time Machine por SMB (§13). */
  timeMachine?: boolean;
  /** Tope de los respaldos de Time Machine; null = sin tope. */
  timeMachineMaxBytes?: number | null;
}

export interface Snapshot {
  id: string;
  share: string;
  at: string;
  size: Bytes;
  kind: "scheduled" | "manual";
  locked: boolean;
}

export type FileKind = "dir" | "image" | "video" | "audio" | "pdf" | "doc" | "sheet" | "code" | "archive" | "text" | "other";

export interface FileEntry {
  name: string;
  path: string;
  kind: FileKind;
  size: Bytes;
  mtime: string;
  /** Semilla para generar miniaturas demo de forma determinística. */
  seed?: number;
}

/** Ítem en la papelera de una carpeta compartida (protocol §10). `path` es opaco: se usa para restaurar. */
export interface TrashEntry {
  name: string;
  path: string;
  /** Carpeta donde estaba, en rutas virtuales (`/Familia/Viajes`). */
  origin: string;
  deletedAt: string;
  size: Bytes;
  kind: FileKind;
}

/** Una versión anterior de un archivo dentro de una instantánea. */
export interface FileVersion {
  snapshot: Snapshot;
  size: Bytes;
  mtime: string;
}

/** Evento de progreso de una transferencia por `files.N`. */
export interface XferEvent {
  xfer: number;
  state: "progress" | "done" | "error";
  bytes: Bytes;
  error?: string;
}

export type JobState = "queued" | "validating" | "running" | "succeeded" | "failed" | "cancelled" | "interrupted";

export interface Job {
  id: string;
  type: string;
  title: string;
  detail: string;
  progress: number;
  state: JobState;
  startedAt: string;
  rate?: string;
}

export type NotifyLevel = "info" | "ok" | "warn" | "crit";

export interface Notice {
  id: string;
  level: NotifyLevel;
  app: string;
  title: string;
  body: string;
  at: string;
  action?: { label: string; app: string; route?: string };
}

export interface MetricsFrame {
  t: number;
  cpu: number; // 0..1
  perCore: number[];
  load: [number, number, number];
  memUsed: Bytes;
  memCache: Bytes;
  memTotal: Bytes;
  netRx: Bytes; // por segundo
  netTx: Bytes;
  diskRead: Bytes;
  diskWrite: Bytes;
  tempCpu: number;
  fanRpm: number;
}

export interface User {
  name: string;
  full: string;
  email: string;
  groups: string[];
  admin: boolean;
  twofa: boolean;
  lastLogin: string;
  quotaUsed: Bytes;
  quota: Bytes | null;
  color: string;
}

/** Lo mínimo para la pantalla de login (método público `auth.users`). */
export interface LoginUser {
  name: string;
  full: string;
  color: string;
}

export interface Group {
  name: string;
  description: string;
  members: string[];
}

export interface Session {
  id: string;
  user: string;
  client: "web" | "desk-win" | "desk-linux" | "smb" | "ssh" | "remote" | "guest";
  ip: string;
  since: string;
  current?: boolean;
}

export interface RtcChannelStats {
  label: string;
  ordered: boolean;
  buffered: Bytes;
  rate: Bytes;
  messages: number;
}

export interface RtcStats {
  ice: "new" | "checking" | "connected" | "completed" | "disconnected" | "failed";
  pair: { local: string; remote: string; type: "host" | "srflx" | "relay"; protocol: "udp" | "tcp" };
  rttMs: number;
  dtls: string;
  channels: RtcChannelStats[];
}

export type Scenario = "healthy" | "degraded" | "busy" | "empty" | "migrate" | "lab" | "cluster";

export interface VolumePlan {
  plan_id: string;
  expires: string;
  confirm_token: string;
  summary: { name: string; profile: VolumeProfile; fs: "btrfs"; usable: Bytes; disks: { id: string; model: string; serial: string; size: Bytes; content: string }[]; notes?: string[] };
}

export interface DeletePlan {
  plan_id: string;
  expires: string;
  confirm_token: string;
  summary: { name: string; shares: string[]; snapshots: number; used: Bytes; disks: { id: string; model: string; serial: string }[] };
}

export type ShareAccess = "rw" | "ro" | "none";

export interface PermissionEntry {
  kind: "user" | "group";
  name: string;
  access: ShareAccess;
}

// ---- Métodos RPC -----------------------------------------------------------

export interface Methods {
  "auth.users": { params: void; result: LoginUser[] };
  /** §13: con 2FA activa devuelve `{needs:"totp"}` (sin usuario ni token) y se sigue con `auth.totp`. */
  "auth.login": { params: { user: string; password: string }; result: { user?: User; token?: string; needs?: "totp" } };
  "auth.resume": { params: { token: string }; result: { user: User } };
  "auth.logout": { params: void; result: Record<string, never> };
  "system.info": { params: void; result: SystemInfo };
  "storage.disks.list": { params: void; result: Disk[] };
  "storage.volumes.list": { params: void; result: Volume[] };
  "storage.volumes.plan": { params: { name: string; profile: VolumeProfile; disks: string[] }; result: VolumePlan };
  "storage.volumes.create": { params: { plan_id: string; confirm_token: string; typed_name: string }; result: { job: string } };
  "storage.volumes.deletePlan": { params: { volume: string }; result: DeletePlan };
  "storage.volumes.delete": { params: { plan_id: string; confirm_token: string; typed_name: string }; result: { job: string } };
  "storage.scrub.start": { params: { volume: string }; result: { job: string } };
  "storage.foreign.mount": { params: { label: string }; result: { job: string } };
  "shares.list": { params: void; result: Share[] };
  "shares.create": { params: { name: string; volume: string; description: string; smb: boolean; timeMachine?: boolean; timeMachineMaxBytes?: number | null }; result: { job: string } };
  "shares.delete": { params: { share: string; typed_name: string }; result: { job: string } };
  "shares.permissions.get": { params: { share: string }; result: { entries: PermissionEntry[] } };
  "shares.permissions.set": { params: { share: string; entries: PermissionEntry[] }; result: Record<string, never> };
  "snapshots.list": { params: { share?: string }; result: Snapshot[] };
  "snapshots.create": { params: { share: string }; result: { job: string } };
  /** `snapshot` (Snapshot.id) lee esa instantánea, solo lectura (§13). */
  "files.list": { params: { path: string; snapshot?: string }; result: FileEntry[] };
  "files.search": { params: { q: string; path?: string }; result: FileEntry[] };
  "files.mkdir": { params: { path: string }; result: FileEntry };
  "files.rename": { params: { path: string; name: string }; result: FileEntry };
  "files.move": { params: { paths: string[]; dest: string }; result: { count: number } };
  "files.trash": { params: { paths: string[] }; result: { count: number } };
  "files.trash.list": { params: { share?: string }; result: TrashEntry[] };
  "files.restore": { params: { paths: string[] }; result: { count: number } };
  "files.trash.empty": { params: { share: string }; result: { count: number } };
  "files.versions": { params: { path: string }; result: FileVersion[] };
  "files.restoreVersion": { params: { path: string; snapshot: string }; result: FileEntry };
  "files.thumb": { params: { path: string; size: 128 | 256; snapshot?: string }; result: { mime: string; data: string } };
  "files.upload.begin": { params: { dir: string; name: string; size: Bytes; overwrite: boolean }; result: { xfer: number; channel: string; chunk: number } };
  "files.upload.end": { params: { xfer: number; sha256?: string }; result: FileEntry };
  /** `size` es el total; con rango se mandan `length` bytes desde `offset` (§13). */
  "files.download.begin": { params: { path: string; snapshot?: string; offset?: number; length?: number }; result: { xfer: number; channel: string; size: Bytes; chunk: number; offset?: number; length?: number } };
  "files.xfer.cancel": { params: { xfer: number }; result: Record<string, never> };
  "users.list": { params: void; result: User[] };
  "users.create": { params: { name: string; full: string; email: string; password: string; admin: boolean; groups: string[] }; result: User };
  "users.setPassword": { params: { name: string; password: string }; result: Record<string, never> };
  "groups.list": { params: void; result: Group[] };
  "sessions.list": { params: void; result: Session[] };
  "jobs.list": { params: void; result: Job[] };
  "notices.list": { params: void; result: Notice[] };
  "rtc.stats": { params: void; result: RtcStats };
}

export type Method = keyof Methods;
export type Params<M extends Method> = Methods[M]["params"];
export type Result<M extends Method> = Methods[M]["result"];

// ---- Eventos ---------------------------------------------------------------

export interface Topics {
  metrics: MetricsFrame;
  "jobs.changed": Job;
  "storage.changed": { what: "disks" | "volumes" };
  "shares.changed": { id?: string };
  "snapshots.changed": { share: string };
  "files.changed": { dir: string };
  "files.xfer": XferEvent;
  notice: Notice;
}

export type Topic = keyof Topics;
export interface Evt<T extends Topic = Topic> {
  topic: T;
  seq: number;
  data: Topics[T];
}

export class RpcError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

export * from "./protocol-apps";
export * from "./protocol-m22";
export * from "./protocol-m4";
export * from "./protocol-v1";
export * from "./protocol-users";
export * from "./protocol-connect";
export * from "./protocol-cluster";
