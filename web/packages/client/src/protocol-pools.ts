// Grupos de almacenamiento (protocol/README.md §16, ADR 0011): LVM sobre md, RAID híbrido,
// LUKS2 por volumen y caché SSD. Se suma a Methods/Topics, Volume y Disk por module
// augmentation para no chocar con otros cambios en protocol.ts.
import type { Bytes } from "./protocol";

export type PoolProfile = "basic" | "raid1" | "raid5" | "raid6" | "raid10" | "hybrid1" | "hybrid2";
export type TierLevel = "basic" | "raid1" | "raid5" | "raid6" | "raid10" | "unused";
export type PoolFS = "btrfs" | "xfs" | "ext4";
export type CacheMode = "read" | "readwrite";

export interface PoolTier {
  index: number;
  /** Tamaño de la partición (o del disco) de cada miembro. */
  size: Bytes;
  members: string[];
  level: TierLevel;
  usable: Bytes;
  state?: string;
  progress?: number;
}

export interface Pool {
  id: string;
  name: string;
  profile: PoolProfile;
  state: "healthy" | "degraded" | "building" | "readonly" | "scrubbing";
  size: Bytes;
  allocated: Bytes;
  free: Bytes;
  disks: string[];
  cacheDisks: string[];
  tiers: PoolTier[];
  volumes: string[];
  /** LUN de iSCSI en volúmenes lógicos del grupo. */
  luns?: { name: string; dev: string; size: Bytes; thin: boolean }[];
  task?: "rebuild" | "reshape" | "resync";
  progress?: number;
}

export interface PoolPlan {
  plan_id: string;
  expires: string;
  confirm_token: string;
  summary: {
    name: string;
    profile: PoolProfile;
    fs: string;
    usable: Bytes;
    unused: Bytes;
    tiers: PoolTier[];
    disks: { id: string; model: string; serial: string; size: Bytes; content: string }[];
    notes?: string[];
    rebuild?: { from: string | null; to: string; estimatedSec: number };
  };
}

export interface KeyDevice {
  uuid: string;
  fs: string;
  label: string;
  size: Bytes;
  disk: string;
  usb: boolean;
}

export interface VolumeCache {
  mode: CacheMode;
  disks: string[];
  size: Bytes;
  used: Bytes;
  dirty: Bytes;
  readHits: number;
  readMisses: number;
  writeHits: number;
  writeMisses: number;
  /** Aciertos de lectura sobre el total (null = todavía sin lecturas). */
  hitRatio: number | null;
}

declare module "./protocol" {
  interface Disk {
    /** Grupo de almacenamiento al que pertenece (Pool.id). */
    pool?: string;
    role?: "data" | "cache" | "log";
  }
  interface Volume {
    /** "pool" = volumen dentro de un grupo; sin valor = volumen clásico (md + Btrfs). */
    kind?: "classic" | "pool" | "zfs";
    pool?: string;
    encrypted?: boolean;
    locked?: boolean;
    keyDevice?: string;
    cache?: VolumeCache;
  }
  interface Methods {
    "storage.pools.list": { params: void; result: Pool[] };
    "storage.pools.plan": { params: { name: string; profile: PoolProfile; disks: string[] }; result: PoolPlan };
    "storage.pools.create": { params: { plan_id: string; confirm_token: string; typed_name: string }; result: { job: string } };
    "storage.pools.expandPlan": { params: { pool: string; add: string[] }; result: PoolPlan };
    "storage.pools.expand": { params: { plan_id: string; confirm_token: string; typed_name: string }; result: { job: string } };
    "storage.pools.replacePlan": { params: { pool: string; failed: string | null; replacement: string }; result: PoolPlan };
    "storage.pools.replace": { params: { plan_id: string; confirm_token: string; typed_name: string }; result: { job: string } };
    "storage.pools.deletePlan": { params: { pool: string }; result: DeletePlan };
    "storage.pools.delete": { params: { plan_id: string; confirm_token: string; typed_name: string }; result: { job: string } };
    "storage.poolVolumes.create": {
      params: { pool: string; name: string; size: Bytes; fs: PoolFS; encryption: { passphrase: string; keyDevice: string | null } | null };
      result: { job: string };
    };
    "storage.poolVolumes.resize": { params: { volume: string; size: Bytes }; result: { job: string } };
    "storage.volumes.unlock": { params: { volume: string; passphrase?: string; useKeyDevice?: boolean }; result: Record<string, never> };
    "storage.volumes.lock": { params: { volume: string }; result: Record<string, never> };
    "storage.volumes.passphrase": { params: { volume: string; old: string; new: string }; result: Record<string, never> };
    "storage.volumes.keyDevice": { params: { volume: string; passphrase: string; device: string | null }; result: Record<string, never> };
    "storage.keyDevices.list": { params: void; result: KeyDevice[] };
    "storage.cache.plan": { params: { volume: string; disks: string[]; mode: CacheMode }; result: PoolPlan };
    "storage.cache.create": { params: { plan_id: string; confirm_token: string; typed_name: string }; result: { job: string } };
    "storage.cache.remove": { params: { volume: string }; result: { job: string } };
  }
}

// ---- Reglas (las mismas que internal/diskops/hybrid.go) -------------------------------

export const POOL_PROFILES: { id: PoolProfile; label: string; description: string; tolerance: string; check: (n: number) => string | null }[] = [
  { id: "hybrid1", label: "Híbrido-1", description: "Aprovecha discos de distinto tamaño. Se puede ampliar de a un disco.", tolerance: "Soporta la falla de 1 disco.", check: (n) => (n >= 2 ? null : "Elegí al menos 2 discos") },
  { id: "hybrid2", label: "Híbrido-2", description: "Como Híbrido-1, con doble protección.", tolerance: "Soporta la falla de 2 discos a la vez.", check: (n) => (n >= 4 ? null : "Elegí al menos 4 discos") },
  { id: "basic", label: "Básico", description: "Un solo disco, sin redundancia.", tolerance: "No soporta la falla del disco.", check: (n) => (n === 1 ? null : "Elegí exactamente 1 disco") },
  { id: "raid1", label: "RAID 1", description: "Dos discos espejados.", tolerance: "Soporta la falla de 1 disco.", check: (n) => (n === 2 ? null : "Elegí exactamente 2 discos") },
  { id: "raid5", label: "RAID 5", description: "Tres discos o más, con paridad.", tolerance: "Soporta la falla de 1 disco.", check: (n) => (n >= 3 ? null : "Elegí al menos 3 discos") },
  { id: "raid6", label: "RAID 6", description: "Cuatro discos o más, con doble paridad.", tolerance: "Soporta la falla de 2 discos.", check: (n) => (n >= 4 ? null : "Elegí al menos 4 discos") },
  { id: "raid10", label: "RAID 10", description: "Espejos de a pares, unidos en franjas.", tolerance: "Soporta la falla de 1 disco por par.", check: (n) => (n < 4 ? "Elegí al menos 4 discos" : n % 2 ? "La cantidad de discos tiene que ser par" : null) },
];

export const poolProfileLabel = (p: PoolProfile) => POOL_PROFILES.find((x) => x.id === p)?.label ?? p;
export const isHybrid = (p: PoolProfile) => p === "hybrid1" || p === "hybrid2";

const MiB = 1024 * 1024;

/** Lo que cada disco aporta a los tramos: desde 1 MiB hasta 1 MiB antes del final. */
export const hybridUsable = (size: Bytes) => (size <= 3 * MiB ? 0 : Math.floor((size - 2 * MiB) / MiB) * MiB);

export function hybridLevel(profile: PoolProfile, members: number): TierLevel {
  if (profile === "hybrid1") return members >= 3 ? "raid5" : members === 2 ? "raid1" : "unused";
  if (profile === "hybrid2") return members >= 4 ? "raid6" : "unused";
  return "unused";
}

function levelUsable(level: TierLevel, n: number, size: Bytes): Bytes {
  switch (level) {
    case "basic":
    case "raid1":
      return size;
    case "raid5":
      return (n - 1) * size;
    case "raid6":
      return (n - 2) * size;
    case "raid10":
      return Math.floor(n / 2) * size;
    default:
      return 0;
  }
}

/**
 * Tramos de un grupo con estos discos (ids y tamaños). En RAID hay un solo tramo con los discos
 * enteros (md usa el más chico); en híbrido, un tramo por cada tamaño distinto (±1 %).
 */
export function poolLayout(profile: PoolProfile, disks: { id: string; size: Bytes }[]): PoolTier[] {
  if (!disks.length) return [];
  if (!isHybrid(profile)) {
    const m = Math.min(...disks.map((d) => d.size));
    const level = profile as TierLevel;
    return [{ index: 1, size: m, members: disks.map((d) => d.id), level, usable: levelUsable(level, disks.length, m) }];
  }
  const usable = disks.map((d) => hybridUsable(d.size));
  const bounds: number[] = [];
  for (const u of [...usable].sort((a, b) => a - b)) {
    if (!u) continue;
    const last = bounds[bounds.length - 1];
    if (last !== undefined && u - last <= last / 100) continue;
    bounds.push(u);
  }
  let prev = 0;
  return bounds.map((b, k) => {
    const members = disks.filter((_, i) => usable[i] >= b).map((d) => d.id);
    const level = hybridLevel(profile, members.length);
    const t: PoolTier = { index: k + 1, size: b - prev, members, level, usable: levelUsable(level, members.length, b - prev) };
    prev = b;
    return t;
  });
}

export const tiersUsable = (t: PoolTier[]) => t.reduce((a, x) => a + x.usable, 0);
export const tiersUnused = (t: PoolTier[]) => t.reduce((a, x) => a + (x.level === "unused" ? x.size * x.members.length : 0), 0);
