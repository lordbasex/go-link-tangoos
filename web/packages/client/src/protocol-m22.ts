// Mantenimiento de volúmenes (protocol/README.md §11). Se suma a Methods/Topics y a Volume por
// module augmentation para no chocar con otros cambios en protocol.ts.
import type { Bytes, VolumeProfile } from "./protocol";

/** Plan de mantenimiento (reemplazo o ampliación): mismo esquema plan → token → nombre escrito que §7. */
export interface MaintenancePlan {
  plan_id: string;
  expires: string;
  confirm_token: string;
  summary: {
    /** Nombre del volumen: es lo que hay que escribir para confirmar. */
    name: string;
    profile: VolumeProfile;
    fs: "btrfs";
    /** Capacidad útil actual. */
    usable: Bytes;
    /** Capacidad útil después de ampliar (solo expandPlan). */
    newUsable?: Bytes;
    /** Discos que se borran (el de reemplazo o los que se suman). */
    disks: { id: string; model: string; serial: string; size: Bytes; content: string }[];
    rebuild?: { from: string | null; to: string; estimatedSec: number };
    notes?: string[];
  };
}

export interface ScheduleLevel {
  keep: number; // 0 = nivel desactivado
}

export interface SnapshotSchedule {
  enabled: boolean;
  hourly: ScheduleLevel & { every: 1 | 2 | 4 | 6 | 12 };
  daily: ScheduleLevel & { at: string };
  weekly: ScheduleLevel & { day: number; at: string };
}

export type AlertSource = "md" | "btrfs" | "smart" | "space";

export interface StorageAlert {
  id: string;
  level: "info" | "warn" | "crit";
  title: string;
  body: string;
  at: string;
  source: AlertSource;
  resolved: boolean;
}

/** Tarea larga sobre el array (además de `state`/`progress`). */
export type VolumeTask = "rebuild" | "reshape" | "resync";

declare module "./protocol" {
  interface Disk {
    /** El array está copiando datos a este disco (reemplazo en curso). */
    rebuilding?: boolean;
  }
  interface Volume {
    /** Qué está haciendo el array mientras `progress` avanza. */
    task?: VolumeTask;
    /** Velocidad de la tarea en bytes/s. */
    taskSpeed?: Bytes;
    /** Segundos estimados para terminar. */
    taskEtaSec?: number;
  }
  interface Methods {
    "storage.disks.replacePlan": { params: { volume: string; failed: string | null; replacement: string }; result: MaintenancePlan };
    "storage.disks.replace": { params: { plan_id: string; confirm_token: string; typed_name: string }; result: { job: string } };
    "storage.volumes.expandPlan": { params: { volume: string; add: string[] }; result: MaintenancePlan };
    "storage.volumes.expand": { params: { plan_id: string; confirm_token: string; typed_name: string }; result: { job: string } };
    "storage.disks.locate": { params: { disk: string }; result: Record<string, never> };
    "snapshots.schedule.get": { params: { share: string }; result: SnapshotSchedule };
    "snapshots.schedule.set": { params: { share: string; schedule: SnapshotSchedule }; result: Record<string, never> };
    "snapshots.delete": { params: { id: string }; result: Record<string, never> };
    "alerts.list": { params: void; result: StorageAlert[] };
  }
  interface Topics {
    "alerts.changed": Record<string, never>;
  }
}

export const defaultSchedule: SnapshotSchedule = {
  enabled: false,
  hourly: { every: 4, keep: 6 },
  daily: { at: "03:00", keep: 14 },
  weekly: { day: 0, at: "03:30", keep: 8 },
};
