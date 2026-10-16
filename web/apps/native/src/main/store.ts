// NAS guardados (userData/nas.json): dirección, huella fijada y último acceso.
import fs from "node:fs";
import path from "node:path";

export interface SavedNas {
  id: string;
  name: string;
  host: string;
  port: number;
  fingerprint: string;
  addedAt: string;
  lastOpened?: string;
}

export class NasStore {
  private list: SavedNas[] = [];
  constructor(private file: string) {
    try {
      const raw = JSON.parse(fs.readFileSync(file, "utf8"));
      if (Array.isArray(raw)) this.list = raw.filter((n) => n && typeof n.host === "string" && typeof n.fingerprint === "string");
    } catch {
      this.list = [];
    }
  }
  all(): SavedNas[] {
    return [...this.list].sort((a, b) => (b.lastOpened ?? b.addedAt).localeCompare(a.lastOpened ?? a.addedAt));
  }
  get(id: string): SavedNas | undefined {
    return this.list.find((n) => n.id === id);
  }
  byHost(host: string, port: number): SavedNas | undefined {
    return this.list.find((n) => n.host === host && n.port === port);
  }
  upsert(n: SavedNas) {
    this.list = [...this.list.filter((x) => x.id !== n.id && !(x.host === n.host && x.port === n.port)), n];
    this.save();
  }
  touch(id: string) {
    const n = this.get(id);
    if (n) {
      n.lastOpened = new Date().toISOString();
      this.save();
    }
  }
  remove(id: string) {
    this.list = this.list.filter((n) => n.id !== id);
    this.save();
  }
  private save() {
    fs.mkdirSync(path.dirname(this.file), { recursive: true });
    const tmp = this.file + ".tmp";
    fs.writeFileSync(tmp, JSON.stringify(this.list, null, 2), { mode: 0o600 });
    fs.renameSync(tmp, this.file);
  }
}

export const nasId = (host: string, port: number) => `${host}:${port}`.replace(/[^a-z0-9.:-]/gi, "_");
