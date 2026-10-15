// Pools ZFS (protocol/README.md §18, ADR 0013): alternativa a md + LVM + Btrfs. OpenZFS se
// instala a pedido (paquetes de contrib, módulo compilado con DKMS). Un volumen ZFS es el
// dataset <pool>/<volumen> montado en /<volumen>; sus carpetas son datasets hijos.
import type { Bytes } from "./protocol";


export type ZfsLayout = "stripe" | "mirror" | "raidz1" | "raidz2" | "raidz3";

export interface ZfsLeaf {
  /** Disk.id; "" si el disco ya no está (se nombra por GUID). */
  id: string;
  name: string;
  guid: string;
  state: string;
  read: number;
  write: number;
  cksum: number;
  resilvering?: boolean;
}

export interface ZfsVdev {
  name: string;
  type: "disk" | ZfsLayout;
  state: string;
  read: number;
  write: number;
  cksum: number;
  size: Bytes;
  disks: ZfsLeaf[];
}

export interface ZfsScan {
  function: "scrub" | "resilver";
  state: "scanning" | "finished" | "canceled";
  progress: number;
  start?: string;
  end?: string;
  errors: number;
}

export interface ZfsZvol {
  name: string;
  size: Bytes;
  used: Bytes;
  device: string;
}

export interface ZfsPool {
  id: string; // "zfs:<nombre>"
  name: string;
  guid: string;
  health: "ONLINE" | "DEGRADED" | "FAULTED" | "OFFLINE" | "UNAVAIL" | "SUSPENDED" | string;
  state: "healthy" | "degraded" | "faulted" | "readonly";
  readonly: boolean;
  layout: ZfsLayout | "mixed";
  size: Bytes;
  allocated: Bytes;
  free: Bytes;
  raw: Bytes;
  frag: number | null;
  vdevs: ZfsVdev[];
  logs: ZfsVdev[];
  cache: ZfsLeaf[];
  scan: ZfsScan | null;
  errors?: string;
  volumes: string[];
  zvols: ZfsZvol[];
  encrypted: string[];
  disks: string[];
  /** Datasets fuera del layout de TangoOS (pools importados de otro equipo). */
  other: { name: string; mountpoint: string; mounted: boolean; used: Bytes }[];
}

export interface ZfsStatus {
  enabled: boolean;
  installed: boolean;
  version?: string;
  /** Job de "Habilitar ZFS" en curso. */
  enabling?: string;
  pools: ZfsPool[];
}

export interface ZfsImportable {
  name: string;
  guid: string;
  state: string;
  status?: string;
  disks: string[];
}

export interface ZfsPlan {
  plan_id: string;
  expires: string;
  confirm_token: string;
  summary: {
    name: string;
    kind: "create" | "data" | "cache" | "log" | "replace";
    layout: string;
    usable: Bytes;
    raw: Bytes;
    wasted: Bytes;
    tolerance: number;
    disks: { id: string; model: string; serial: string; size: Bytes; content: string }[];
    notes: string[];
  };
}

export interface ZfsScrubSchedule {
  enabled: boolean;
  days: number;
  last?: string;
}

type Confirm = { plan_id: string; confirm_token: string; typed_name: string };

declare module "./protocol" {
  interface Methods {
    "storage.zfs.status": { params: void; result: ZfsStatus };
    "storage.zfs.importable": { params: void; result: ZfsImportable[] };
    "storage.zfs.enable": { params: void; result: { job: string } };
    "storage.zfs.plan": { params: { name: string; layout: ZfsLayout; disks: string[] }; result: ZfsPlan };
    "storage.zfs.create": { params: Confirm; result: { job: string } };
    "storage.zfs.addPlan": { params: { pool: string; kind: "data" | "cache" | "log"; disks: string[] }; result: ZfsPlan };
    "storage.zfs.add": { params: Confirm; result: { job: string } };
    "storage.zfs.auxRemove": { params: { pool: string; kind: "cache" | "log"; vdev: string }; result: { job: string } };
    "storage.zfs.replacePlan": { params: { pool: string; old: string; replacement: string }; result: ZfsPlan };
    "storage.zfs.replace": { params: Confirm; result: { job: string } };
    "storage.zfs.scrub": { params: { pool: string; stop?: boolean }; result: { job: string } };
    "storage.zfs.scrubSchedule.get": { params: { pool: string }; result: ZfsScrubSchedule };
    "storage.zfs.scrubSchedule.set": { params: { pool: string; enabled: boolean; days: number }; result: ZfsScrubSchedule };
    "storage.zfs.import": { params: { pool: string; readonly: boolean }; result: { job: string } };
    "storage.zfs.export": { params: { pool: string }; result: { job: string } };
    "storage.zfs.destroyPlan": { params: { pool: string }; result: import("./protocol").DeletePlan };
    "storage.zfs.destroy": { params: Confirm; result: { job: string } };
    "storage.zfs.volumes.create": {
      params: { pool: string; name: string; quota?: Bytes; reservation?: Bytes; encryption: { passphrase: string } | null };
      result: { job: string };
    };
    "storage.zfs.volumes.set": { params: { volume: string; quota: Bytes; reservation: Bytes }; result: Record<string, never> };
    "storage.zfs.volumes.deletePlan": { params: { volume: string }; result: import("./protocol").DeletePlan };
    "storage.zfs.volumes.delete": { params: Confirm; result: { job: string } };
    "storage.zfs.zvols.create": { params: { pool: string; name: string; size: Bytes; sparse: boolean }; result: Record<string, never> };
    "storage.zfs.zvols.delete": { params: { pool: string; name: string }; result: Record<string, never> };
  }
}

// ---- Reglas (las mismas que internal/zfs/zfs.go) ---------------------------------------

export const ZFS_LAYOUTS: { id: ZfsLayout; label: string; description: string; min: number; parity: number }[] = [
  { id: "mirror", label: "Espejo", description: "Cada dato en todos los discos. Lecturas rápidas.", min: 2, parity: 0 },
  { id: "raidz1", label: "RAIDZ1", description: "Paridad simple, como RAID 5 pero sin agujero de escritura.", min: 3, parity: 1 },
  { id: "raidz2", label: "RAIDZ2", description: "Doble paridad. Recomendado con discos grandes.", min: 4, parity: 2 },
  { id: "raidz3", label: "RAIDZ3", description: "Triple paridad, para muchos discos.", min: 5, parity: 3 },
  { id: "stripe", label: "Sin redundancia", description: "Suma la capacidad de los discos (stripe).", min: 1, parity: 0 },
];

export const zfsLayoutLabel = (l: string) => ZFS_LAYOUTS.find((x) => x.id === l)?.label ?? (l === "mixed" ? "Mixto" : l === "disk" ? "Disco" : l);

export function zfsTolerance(layout: ZfsLayout, n: number): number {
  if (layout === "mirror") return Math.max(0, n - 1);
  if (layout === "stripe") return 0;
  return ZFS_LAYOUTS.find((x) => x.id === layout)?.parity ?? 0;
}

export function zfsCheck(layout: ZfsLayout, n: number): string | null {
  const m = ZFS_LAYOUTS.find((x) => x.id === layout)?.min ?? 1;
  return n >= m ? null : `Elegí al menos ${m} discos`;
}

/** Capacidad útil aproximada de un vdev (sin la reserva de ~3 % de ZFS). */
export function zfsUsable(layout: ZfsLayout, sizes: Bytes[]): Bytes {
  if (!sizes.length) return 0;
  const m = Math.min(...sizes);
  if (layout === "stripe") return sizes.reduce((a, b) => a + b, 0);
  if (layout === "mirror") return m;
  const p = ZFS_LAYOUTS.find((x) => x.id === layout)?.parity ?? 0;
  return sizes.length > p ? m * (sizes.length - p) : 0;
}

/** Reparto de cada disco para el diagrama: datos, paridad/copia y sin usar. */
export function zfsSplit(layout: ZfsLayout, sizes: Bytes[]): { data: Bytes; redundancy: Bytes; unused: Bytes }[] {
  if (!sizes.length) return [];
  const m = Math.min(...sizes);
  const n = sizes.length;
  return sizes.map((s) => {
    if (layout === "stripe") return { data: s, redundancy: 0, unused: 0 };
    if (layout === "mirror") return { data: m / n, redundancy: m - m / n, unused: s - m };
    const p = ZFS_LAYOUTS.find((x) => x.id === layout)?.parity ?? 0;
    return { data: (m * (n - p)) / n, redundancy: (m * p) / n, unused: s - m };
  });
}
