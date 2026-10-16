// Replicación de volúmenes ZFS (protocol/README.md §18.1, ADR 0016).
import type { Bytes } from "./protocol";

export interface ReplRun {
  at: string;
  ok: boolean;
  error?: string;
  bytes: Bytes;
  seconds: number;
  snap?: string;
}

export interface ReplTask {
  id: string;
  pool: string;
  vol: string;
  /** "local:<pool>" o el id de un destino vinculado. */
  target: string;
  /** Horas entre envíos; 0 = solo a mano. */
  every: number;
  enabled: boolean;
  pending?: string;
  last?: ReplRun;
  lastOk?: string;
  volumeId?: string;
  targetLabel: string;
  job?: string;
  next?: string;
}

export interface ReplTarget {
  id: string;
  name: string;
  addr: string;
  fingerprint: string;
  addedAt: string;
}

export interface ReplSource {
  id: string;
  name: string;
  fingerprint: string;
  addedAt: string;
}

export interface ReplReplica {
  pool: string;
  origin: string;
  from: string;
  vol: string;
  used: Bytes;
  encrypted: boolean;
  partial: boolean;
  lastSnap?: string;
  lastAt?: string;
  snapshots: number;
}

export interface ReplStatus {
  identity: { name: string; fingerprint: string };
  receive: { enabled: boolean; pool: string; port: number; addrs: string[]; invite?: { code: string; expires: string } };
  targets: ReplTarget[];
  sources: ReplSource[];
  tasks: ReplTask[];
  replicas: ReplReplica[];
}

type Replica = { pool: string; origin: string; vol: string };

declare module "./protocol" {
  interface Methods {
    "storage.zfs.repl.status": { params: void; result: ReplStatus };
    "storage.zfs.repl.receive": { params: { enabled: boolean; pool: string }; result: Record<string, never> };
    "storage.zfs.repl.invite": { params: void; result: { code: string; expires: string } };
    "storage.zfs.repl.targets.probe": { params: { address: string }; result: { probe: string; name: string; fingerprint: string; address: string } };
    "storage.zfs.repl.targets.add": { params: { probe: string; code: string }; result: ReplTarget };
    "storage.zfs.repl.targets.remove": { params: { id: string }; result: Record<string, never> };
    "storage.zfs.repl.sources.remove": { params: { id: string }; result: Record<string, never> };
    "storage.zfs.repl.tasks.create": { params: { volume: string; target: string; every: number; runNow?: boolean }; result: { id: string; job?: string } };
    "storage.zfs.repl.tasks.update": { params: { id: string; every: number; enabled: boolean }; result: Record<string, never> };
    "storage.zfs.repl.tasks.delete": { params: { id: string }; result: Record<string, never> };
    "storage.zfs.repl.tasks.run": { params: { id: string }; result: { job: string } };
    "storage.zfs.repl.replicas.promote": { params: Replica & { name: string; passphrase?: string }; result: { locked: boolean } };
    "storage.zfs.repl.replicas.delete": { params: Replica; result: Record<string, never> };
  }
}

export const REPL_EVERY: { hours: number; label: string }[] = [
  { hours: 1, label: "Cada hora" },
  { hours: 6, label: "Cada 6 horas" },
  { hours: 12, label: "Cada 12 horas" },
  { hours: 24, label: "Una vez por día" },
  { hours: 168, label: "Una vez por semana" },
  { hours: 0, label: "Solo a mano" },
];
