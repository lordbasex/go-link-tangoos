// Centro de apps (protocol/README.md §9, ADR 0010). Se suma a Methods/Topics por module augmentation
// para no chocar con otros cambios en protocol.ts.
import type { Bytes } from "./protocol";

export type AppCategory = "Multimedia" | "Fotos" | "Hogar" | "Productividad" | "Red" | "Seguridad" | "Utilidades";

export const appCategories: AppCategory[] = ["Multimedia", "Fotos", "Hogar", "Productividad", "Red", "Seguridad", "Utilidades"];

export interface CatalogOption {
  key: string;
  label: string;
  type: "string" | "password" | "port" | "bool";
  default?: string | number | boolean;
  required?: boolean;
}

export interface CatalogApp {
  id: string;
  name: string;
  description: string;
  category: AppCategory;
  /** data-URL (SVG embebido): sirve igual en la LAN y en el portal TangoConnect. */
  icon: string;
  version: string;
  webPort: number | null;
  options: CatalogOption[];
  notes?: string;
  requiresHostNetwork?: boolean;
  /** Tamaño aproximado de las imágenes a descargar. */
  sizeHint: Bytes;
}

export type AppState = "installing" | "running" | "stopped" | "updating" | "error";

export interface InstalledApp {
  id: string;
  name: string;
  icon: string;
  version: string;
  volume: string;
  dataPath: string;
  state: AppState;
  /** https://<host>:<proxyPort>/ */
  url: string | null;
  cpu: number | null;
  mem: Bytes | null;
  error?: string;
}

export interface DockerStatus {
  installed: boolean;
  running: boolean;
  version: string | null;
}

type AppId = { id: string };

declare module "./protocol" {
  interface Methods {
    "apps.docker.status": { params: void; result: DockerStatus };
    "apps.docker.install": { params: void; result: { job: string } };
    "apps.catalog": { params: void; result: CatalogApp[] };
    "apps.list": { params: void; result: InstalledApp[] };
    "apps.install": { params: { id: string; volume: string; options: Record<string, string | number | boolean> }; result: { job: string } };
    "apps.start": { params: AppId; result: { job: string } };
    "apps.stop": { params: AppId; result: { job: string } };
    "apps.restart": { params: AppId; result: { job: string } };
    "apps.update": { params: AppId; result: { job: string } };
    "apps.uninstall": { params: { id: string; keepData: boolean }; result: { job: string } };
    "apps.logs": { params: { id: string; tail: number }; result: { lines: string[] } };
  }
  interface Topics {
    "apps.changed": { id?: string };
  }
}
