// Datos demo. Todo lo que sale de acá se muestra con la insignia DEMO.
import { t } from "../i18n";
import { fmtBytes } from "../format";
import type { Disk, FileEntry, FileKind, Group, Notice, Scenario, Session, Share, Snapshot, SystemInfo, User, Volume } from "../protocol";

export const KiB = 1024;
export const MiB = KiB * 1024;
export const GiB = MiB * 1024;
export const TiB = GiB * 1024;
const TB = 1e12;

const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString();
const H = 3600_000;
const D = 24 * H;

export function systemInfo(): SystemInfo {
  return {
    hostname: "tango",
    model: t("Mini-PC N150 · {n} bahías", { n: 4 }),
    cpu: "Intel N150",
    cores: 4,
    memTotal: 16 * GiB,
    version: "0.1.0-dev",
    debian: "Debian 13.7 (trixie)",
    kernel: "6.12.111-amd64",
    ip: "192.168.1.50",
    uptimeSec: 12 * 86400 + 4 * 3600 + 17 * 60,
    serial: "TG-N150-0001",
  };
}

// ---- Discos -----------------------------------------------------------------

const systemDisk: Disk = {
  id: "nvme-eui.0025385b21404f2a", dev: "nvme0n1", bay: null, model: "Samsung 980 500GB", serial: "S64DNX0T412345",
  firmware: "3B4QFXO7", size: 500e9, kind: "nvme", tempC: 41, powerOnHours: 3120, smart: "ok", use: "system", reallocated: 0, pending: 0,
};

function bayDisk(bay: number, model: string, serial: string, size: number, rpm: number, extra: Partial<Disk> = {}): Disk {
  return {
    id: `wwn-0x5000c500${(0xa1b2c3 + bay * 7919).toString(16)}`, dev: `sd${String.fromCharCode(97 + bay)}`, bay, model, serial,
    firmware: "SC60", size, kind: "hdd", rpm, tempC: 34 + bay, powerOnHours: 8000 + bay * 913, smart: "ok", use: "free",
    reallocated: 0, pending: 0, ...extra,
  };
}

export function disks(s: Scenario): Disk[] {
  const d1 = bayDisk(1, "WD Red Plus 8TB", "WD-CA1K9L2M", 8 * TB, 5640);
  const d2 = bayDisk(2, "Seagate IronWolf 8TB", "ZA1F3K7Q", 8 * TB, 7200);
  const d3 = bayDisk(3, "Seagate IronWolf 4TB", "ZDH8P2L1", 4 * TB, 5900);
  switch (s) {
    case "lab": {
      // Igual que la VM de desarrollo: 4×16 GB + 6×2 GB + 2×4 GB; volume1 (RAID 1) en D1+D2, volume2 (RAID 5) en S1–S3.
      const G = 1024 ** 3;
      const lab = (bay: number, serial: string, size: number, extra: Partial<Disk> = {}) =>
        bayDisk(bay, "QEMU HARDDISK", serial, size, 0, { rpm: undefined, kind: "ssd", tempC: null, powerOnHours: null, smart: "unknown", ...extra });
      return [
        { ...systemDisk, model: "QEMU HARDDISK", serial: "TANGO-SYS", size: 20 * G, kind: "ssd", tempC: null, powerOnHours: null, smart: "unknown" },
        lab(1, "TANGO-D1", 16 * G, { use: "member", volume: "vol1" }),
        lab(2, "TANGO-D2", 16 * G, { use: "member", volume: "vol1" }),
        lab(3, "TANGO-D3", 16 * G),
        lab(4, "TANGO-D4", 16 * G),
        ...[1, 2, 3, 4, 5, 6].map((i) => lab(4 + i, `TANGO-S${i}`, 2 * G, i <= 3 ? { use: "member", volume: "vol2" } : {})),
        lab(11, "TANGO-M1", 4 * G),
        lab(12, "TANGO-M2", 4 * G),
      ];
    }
    case "pools":
    case "locked": {
      // Un grupo Híbrido-1 con discos de 8, 8, 4 y 12 TB y una NVMe de caché (§16).
      const pool = { use: "member" as const, pool: "p1", role: "data" as const };
      return [
        systemDisk,
        { ...d1, ...pool },
        { ...d2, ...pool },
        { ...d3, ...pool },
        bayDisk(4, "WD Red Pro 12TB", "WD-8KJ2P0QN", 12 * TB, 7200, pool),
        { ...systemDisk, id: "nvme-eui.0025385c31507a11", dev: "nvme1n1", model: "Samsung 990 EVO 1TB", serial: "S7GCNX0X118822", size: 1e12, use: "member", pool: "p1", role: "cache", tempC: 44 },
      ];
    }
    case "zfs": {
      // Un pool ZFS RAIDZ1 de 4×8 TB con una NVMe de caché L2ARC y dos discos libres de 12 TB (§18).
      const z = { use: "member" as const, pool: "zfs:tanque", role: "data" as const };
      return [
        systemDisk,
        { ...d1, ...z },
        { ...d2, ...z },
        bayDisk(3, "WD Red Plus 8TB", "WD-CA4M7T1P", 8 * TB, 5640, z),
        bayDisk(4, "Seagate IronWolf 8TB", "ZA1G8R2C", 8 * TB, 7200, z),
        bayDisk(5, "WD Red Pro 12TB", "WD-8KJ2P0QN", 12 * TB, 7200),
        bayDisk(6, "WD Red Pro 12TB", "WD-8KJ7X3LM", 12 * TB, 7200),
        { ...systemDisk, id: "nvme-eui.0025385c31507a11", dev: "nvme1n1", model: "Samsung 990 EVO 1TB", serial: "S7GCNX0X118822", size: 1e12, use: "member", pool: "zfs:tanque", role: "cache", tempC: 44 },
      ];
    }
    case "hybrid":
      // Discos de distinto tamaño, sin grupos: el asistente muestra el RAID híbrido.
      return [
        systemDisk,
        bayDisk(1, "Seagate IronWolf 4TB", "ZDH8P2L1", 4 * TB, 5900),
        bayDisk(2, "WD Red Plus 8TB", "WD-CA1K9L2M", 8 * TB, 5640),
        bayDisk(3, "Seagate IronWolf 8TB", "ZA1F3K7Q", 8 * TB, 7200),
        bayDisk(4, "WD Red Pro 12TB", "WD-8KJ2P0QN", 12 * TB, 7200),
      ];
    case "empty":
      return [systemDisk, d1, d2, d3];
    case "migrate": {
      const foreign = { label: t("NAS de {n} bahías · {volume}", { n: 4, volume: "volume_1" }), profile: t("RAID híbrido ({n} discos)", { n: 2 }) };
      return [systemDisk, { ...d1, foreign }, { ...d2, foreign }, d3];
    }
    case "degraded":
      return [
        systemDisk,
        { ...d1, use: "member", volume: "vol1" },
        { ...d2, use: "failed", volume: "vol1", smart: "crit", tempC: 0, reallocated: 1872, pending: 64 },
        d3,
        // Disco nuevo ya colocado en la bahía 4: listo para reemplazar al de la bahía 2.
        bayDisk(4, "WD Red Plus 8TB", "WD-CA7N2R5X", 8 * TB, 5640, { powerOnHours: 3 }),
      ];
    default:
      return [systemDisk, { ...d1, use: "member", volume: "vol1" }, { ...d2, use: "member", volume: "vol1" }, { ...d3, smart: s === "busy" ? "warn" : "ok", reallocated: s === "busy" ? 8 : 0 }];
  }
}

export function volumes(s: Scenario, ds: Disk[]): Volume[] {
  if (s === "empty" || s === "migrate" || s === "hybrid") return [];
  if (s === "pools" || s === "locked") {
    const disks = ds.filter((d) => d.pool === "p1" && d.role === "data").map((d) => d.id);
    const nvme = ds.filter((d) => d.role === "cache").map((d) => d.id);
    const growth = Array.from({ length: 30 }, (_, i) => 0.5 + i * 0.003 + Math.sin(i / 3) * 0.004);
    const locked = s === "locked";
    return [
      {
        id: "vol1", name: "volume1", mount: "/volume1", kind: "pool", pool: "p1", profile: "hybrid1", fs: "btrfs", size: 13 * TiB, used: 7.2 * TiB,
        state: "healthy", disks, lastScrub: iso(9 * D), growth30d: growth,
        cache: { mode: "read", disks: nvme, size: 0.93 * TiB, used: 0.71 * TiB, dirty: 0, readHits: 4_812_330, readMisses: 985_114, writeHits: 0, writeMisses: 1_204_778, hitRatio: 0.83 },
      },
      {
        id: "vol2", name: "privado", mount: locked ? "" : "/privado", kind: "pool", pool: "p1", profile: "hybrid1", fs: "btrfs", size: 2.7 * TiB, used: locked ? 0 : 0.42 * TiB,
        state: locked ? "locked" : "healthy", disks, lastScrub: locked ? null : iso(9 * D), growth30d: [], encrypted: true, locked, keyDevice: "5E2A-91C4",
      },
    ];
  }
  if (s === "zfs") {
    const disks = ds.filter((d) => d.pool === "zfs:tanque" && d.role === "data").map((d) => d.id);
    const growth = Array.from({ length: 30 }, (_, i) => 0.41 + i * 0.0021 + Math.sin(i / 4) * 0.003);
    return [
      { id: "vol1", name: "volume1", mount: "/volume1", kind: "zfs", pool: "zfs:tanque", profile: "raidz1", fs: "zfs", size: 19.6 * TiB, used: 8.1 * TiB,
        state: "healthy", disks, lastScrub: iso(6 * D), growth30d: growth },
      { id: "vol2", name: "privado", mount: "/privado", kind: "zfs", pool: "zfs:tanque", profile: "raidz1", fs: "zfs", size: 2 * TiB, used: 0.31 * TiB,
        state: "healthy", disks, lastScrub: iso(6 * D), growth30d: [], encrypted: true },
    ];
  }
  if (s === "lab")
    return [
      { id: "vol1", name: "volume1", mount: "/volume1", profile: "raid1", fs: "btrfs", size: 15.6 * 1024 ** 3, used: 0.4 * 1024 ** 3, state: "healthy", disks: ds.filter((d) => d.volume === "vol1").map((d) => d.id), lastScrub: iso(2 * H), growth30d: [] },
      { id: "vol2", name: "volume2", mount: "/volume2", profile: "raid5", fs: "btrfs", size: 3.9 * 1024 ** 3, used: 0.1 * 1024 ** 3, state: "healthy", disks: ds.filter((d) => d.volume === "vol2").map((d) => d.id), lastScrub: null, growth30d: [] },
    ];
  const growth = Array.from({ length: 30 }, (_, i) => 0.55 + i * 0.0024 + Math.sin(i / 3) * 0.004);
  return [
    {
      id: "vol1", name: "volume1", mount: "/volume1", profile: "raid1", fs: "btrfs",
      size: 7.27 * TiB, used: 4.51 * TiB,
      state: s === "degraded" ? "degraded" : s === "busy" ? "scrubbing" : "healthy",
      progress: s === "busy" ? 0.37 : undefined,
      disks: ds.filter((d) => d.volume === "vol1").map((d) => d.id),
      lastScrub: iso(12 * D), growth30d: growth,
    },
  ];
}

// ---- Carpetas compartidas -------------------------------------------------------

export function shares(s: Scenario): Share[] {
  if (s === "lab")
    return [
      { id: "familia", name: "Familia", volume: "vol1", path: "/volume1/@shares/Familia", size: 0, description: "Lo compartido de la casa", smb: true, nfs: false, snapshots: true, recycleBin: false, color: "#F97316" },
      { id: "fotos", name: "Fotos", volume: "vol2", path: "/volume2/@shares/Fotos", size: 0, description: "Biblioteca de fotos", smb: true, nfs: false, snapshots: true, recycleBin: false, color: "#EC4899" },
    ];
  if (s === "empty" || s === "migrate" || s === "hybrid") return [];
  const sh = (id: string, name: string, size: number, description: string, color: string, extra: Partial<Share> = {}): Share => ({
    id, name, volume: "vol1", path: `/volume1/${name}`, size, description, smb: true, nfs: false, snapshots: true, recycleBin: true, color, ...extra,
  });
  return [
    sh("familia", "Familia", 412 * GiB, "Lo compartido de la casa", "#F97316"),
    sh("fotos", "Fotos", 1.62 * TiB, "Biblioteca de fotos y videos", "#EC4899"),
    sh("media", "Media", 1.94 * TiB, "Películas, series y música", "#8B5CF6", { nfs: true }),
    sh("documentos", "Documentos", 38 * GiB, "Papeles, facturas y contratos", "#2F7CF6"),
    sh("backups", "Backups", 486 * GiB, "Time Machine y copias de las PCs", "#22C55E", { snapshots: false, recycleBin: false }),
    sh("homes", "homes", 61 * GiB, "Carpetas personales", "#8E8E96"),
  ];
}

export function snapshots(s: Scenario): Snapshot[] {
  if (s === "empty" || s === "migrate" || s === "lab" || s === "hybrid") return [];
  const out: Snapshot[] = [];
  for (const share of ["familia", "fotos", "media", "documentos", "homes"]) {
    for (let i = 0; i < 9; i++) {
      out.push({
        id: `${share}-${i}`, share, at: iso(i < 6 ? i * 4 * H + 37 * 60_000 : (i - 5) * D), size: Math.round((2 + ((i * 37) % 11)) * 97 * MiB),
        kind: i === 3 ? "manual" : "scheduled", locked: i === 3,
      });
    }
  }
  return out;
}

// ---- Árbol de archivos ------------------------------------------------------------

type Tree = { [name: string]: Tree | number };

const files: Record<string, Tree> = {
  Familia: {
    "Viaje a Mendoza": { "Itinerario.pdf": 1.2 * MiB, "Reservas.xlsx": 84 * KiB, "Bodegas.jpg": 4.8 * MiB, "Aconcagua.jpg": 6.1 * MiB, "Ruta 7.mp4": 412 * MiB },
    "Asado de fin de año": { "IMG_2301.jpg": 3.9 * MiB, "IMG_2302.jpg": 4.1 * MiB, "IMG_2305.jpg": 3.7 * MiB, "IMG_2309.jpg": 4.4 * MiB, "Brindis.mov": 286 * MiB },
    "Recetas de la abuela.pdf": 18 * MiB,
    "Lista de compras.md": 2 * KiB,
    "Presupuesto casa 2026.xlsx": 212 * KiB,
    "Wifi invitados.txt": 1 * KiB,
  },
  Fotos: {
    "2026": {
      "Bariloche": Object.fromEntries(Array.from({ length: 18 }, (_, i) => [`IMG_${4410 + i}.jpg`, (3 + (i % 4)) * MiB])),
      "Cumple de Sofi": Object.fromEntries(Array.from({ length: 12 }, (_, i) => [`IMG_${5120 + i}.jpg`, (3 + (i % 3)) * MiB])),
      "Escaneos": { "Partida de nacimiento.pdf": 2.4 * MiB, "Pasaporte.jpg": 1.9 * MiB },
    },
    "2025": {
      "Mar del Plata": Object.fromEntries(Array.from({ length: 14 }, (_, i) => [`DSC_${8001 + i}.jpg`, (5 + (i % 3)) * MiB])),
      "Navidad": Object.fromEntries(Array.from({ length: 8 }, (_, i) => [`IMG_${9930 + i}.jpg`, 4 * MiB])),
    },
    "Cámara del celular": Object.fromEntries(Array.from({ length: 24 }, (_, i) => [i % 7 === 6 ? `VID_${6000 + i}.mp4` : `IMG_${6000 + i}.jpg`, i % 7 === 6 ? 180 * MiB : 3.2 * MiB])),
  },
  Media: {
    "Películas": { "Nueve reinas (2000).mkv": 8.2 * GiB, "El secreto de sus ojos (2009).mkv": 11.4 * GiB, "Relatos salvajes (2014).mkv": 9.7 * GiB, "Argentina, 1985 (2022).mkv": 12.1 * GiB },
    "Series": { "Okupas": { "T01E01.mkv": 1.4 * GiB, "T01E02.mkv": 1.3 * GiB, "T01E03.mkv": 1.4 * GiB }, "Los simuladores": { "T01E01.mkv": 1.1 * GiB, "T01E02.mkv": 1.2 * GiB } },
    "Música": {
      "Astor Piazzolla": { "Libertango.flac": 38 * MiB, "Adiós Nonino.flac": 52 * MiB, "Oblivion.flac": 31 * MiB },
      "Carlos Gardel": { "Por una cabeza.flac": 22 * MiB, "Volver.flac": 24 * MiB, "Mi Buenos Aires querido.flac": 21 * MiB },
      "Bajofondo": { "Pa' bailar.flac": 41 * MiB },
    },
  },
  Documentos: {
    "Facturas": Object.fromEntries(["enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre"].map((m) => [`Luz ${m} 2026.pdf`, 210 * KiB])),
    "Contratos": { "Alquiler 2026.pdf": 1.1 * MiB, "Seguro auto.pdf": 640 * KiB },
    "Trabajo": { "Propuesta TangoOS.md": 18 * KiB, "Arquitectura.png": 640 * KiB, "tangod.go": 12 * KiB, "Presentación.pdf": 8.4 * MiB },
    "Manual del router.pdf": 6.2 * MiB,
    "Notas de la reunión.md": 6 * KiB,
    "Impuestos 2025.zip": 48 * MiB,
  },
  Backups: {
    "MacBook de Federico.sparsebundle": { "Info.plist": 1 * KiB },
    "Notebook de Sofi": { "Backup 2026-10-01.zip": 38 * GiB, "Backup 2026-10-08.zip": 39 * GiB },
  },
  homes: {
    "federico": { "Descargas": { "debian-13.4.0-amd64-netinst.iso": 754 * MiB, "factura-isp.pdf": 180 * KiB }, "Escritorio": { "ideas.txt": 3 * KiB } },
    "sofia": { "Tareas": { "Historia - Revolución de Mayo.docx": 220 * KiB } },
  },
};

const ext = (n: string) => n.slice(n.lastIndexOf(".") + 1).toLowerCase();
export function kindOf(name: string): FileKind {
  const e = ext(name);
  if (["jpg", "jpeg", "png", "heic", "webp", "gif", "avif"].includes(e)) return "image";
  if (["mp4", "mkv", "mov", "avi", "webm"].includes(e)) return "video";
  if (["flac", "mp3", "m4a", "wav", "ogg"].includes(e)) return "audio";
  if (e === "pdf") return "pdf";
  if (["doc", "docx", "odt", "md", "pages"].includes(e)) return e === "md" ? "text" : "doc";
  if (["xls", "xlsx", "ods", "csv"].includes(e)) return "sheet";
  if (["go", "ts", "tsx", "js", "json", "yaml", "sh", "plist"].includes(e)) return "code";
  if (["zip", "tar", "gz", "7z", "iso", "sparsebundle"].includes(e)) return "archive";
  if (["txt", "log"].includes(e)) return "text";
  return "other";
}

let seedCounter = 0;
function hash(s: string) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return h >>> 0;
}

/** Sistema de archivos en memoria: path absoluto ("/Fotos/2026") → entradas. */
export class MemFS {
  private dirs = new Map<string, FileEntry[]>();
  readonly trash: FileEntry[] = [];
  /** Cuándo se borró cada ítem de la papelera (por path original). */
  readonly deletedAt = new Map<string, string>();

  constructor(enabled: boolean) {
    this.dirs.set("/", []);
    if (!enabled) return;
    for (const [share, tree] of Object.entries(files)) this.load(`/${share}`, tree, 0);
    this.dirs.set("/", Object.keys(files).map((n) => this.entry(`/${n}`, "dir", 0, 3 * D)));
  }

  private entry(path: string, kind: FileKind, size: number, ageMs: number): FileEntry {
    const name = path.slice(path.lastIndexOf("/") + 1);
    seedCounter++;
    return { name, path, kind, size, mtime: iso(ageMs), seed: hash(path) };
  }

  private load(dir: string, tree: Tree, depth: number) {
    const list: FileEntry[] = [];
    for (const [name, v] of Object.entries(tree)) {
      const p = `${dir}/${name}`;
      const age = (hash(p) % 90) * D + (hash(name) % 20) * H;
      if (typeof v === "number" && !name.endsWith(".sparsebundle")) {
        list.push(this.entry(p, kindOf(name), Math.round(v), age));
      } else {
        const sub = typeof v === "number" ? {} : v;
        list.push(this.entry(p, name.endsWith(".sparsebundle") ? "archive" : "dir", 0, age));
        if (!name.endsWith(".sparsebundle")) this.load(p, sub, depth + 1);
      }
    }
    this.dirs.set(dir, list);
  }

  list(path: string): FileEntry[] | undefined {
    const l = this.dirs.get(path);
    return l?.map((e) => (e.kind === "dir" ? { ...e, size: this.dirSize(e.path) } : e));
  }

  private dirSize(path: string): number {
    return (this.dirs.get(path) ?? []).reduce((a, e) => a + (e.kind === "dir" ? this.dirSize(e.path) : e.size), 0);
  }

  search(q: string, within?: string): FileEntry[] {
    const needle = q.toLowerCase();
    const out: FileEntry[] = [];
    const inside = (p: string) => !within || within === "/" || p.startsWith(within + "/");
    for (const l of this.dirs.values()) for (const e of l) if (e.name.toLowerCase().includes(needle) && inside(e.path)) out.push(e);
    return out.slice(0, 50);
  }

  mkdir(path: string) {
    const parent = path.slice(0, path.lastIndexOf("/")) || "/";
    const e = this.entry(path, "dir", 0, 0);
    this.dirs.get(parent)?.push(e);
    this.dirs.set(path, []);
    return e;
  }

  add(dir: string, name: string, size: number) {
    const e = this.entry(`${dir}/${name}`, kindOf(name), size, 0);
    this.dirs.get(dir)?.push(e);
    return e;
  }

  rename(path: string, name: string) {
    const parent = path.slice(0, path.lastIndexOf("/")) || "/";
    const l = this.dirs.get(parent) ?? [];
    const i = l.findIndex((e) => e.path === path);
    if (i < 0) return undefined;
    const np = `${parent === "/" ? "" : parent}/${name}`;
    const e = { ...l[i], name, path: np, kind: l[i].kind === "dir" ? "dir" : kindOf(name) } as FileEntry;
    l[i] = e;
    if (this.dirs.has(path)) {
      // mover el subárbol
      for (const [k, v] of [...this.dirs]) {
        if (k === path || k.startsWith(path + "/")) {
          this.dirs.delete(k);
          this.dirs.set(np + k.slice(path.length), v.map((c) => ({ ...c, path: np + c.path.slice(path.length) })));
        }
      }
    }
    return e;
  }

  trashPaths(paths: string[]) {
    let n = 0;
    for (const p of paths) {
      const parent = p.slice(0, p.lastIndexOf("/")) || "/";
      const l = this.dirs.get(parent) ?? [];
      const i = l.findIndex((e) => e.path === p);
      if (i >= 0) {
        this.trash.push(l[i]);
        this.deletedAt.set(p, new Date().toISOString());
        l.splice(i, 1);
        n++;
      }
    }
    return n;
  }

  /** Vacía la papelera de una carpeta compartida (o toda si no se indica). */
  emptyTrash(share?: string) {
    const match = (e: FileEntry) => !share || e.path.toLowerCase().startsWith(`/${share.toLowerCase()}/`);
    const gone = this.trash.filter(match);
    for (const e of gone) this.deletedAt.delete(e.path);
    this.trash.splice(0, this.trash.length, ...this.trash.filter((e) => !match(e)));
    return gone.length;
  }

  find(path: string): FileEntry | undefined {
    const parent = path.slice(0, path.lastIndexOf("/")) || "/";
    return this.dirs.get(parent)?.find((e) => e.path === path);
  }

  has(path: string) {
    return this.dirs.has(path) || !!this.find(path);
  }

  /** Mueve entradas (con su subárbol) a otra carpeta. */
  move(paths: string[], dest: string) {
    const target = this.dirs.get(dest);
    if (!target) return 0;
    let n = 0;
    for (const p of paths) {
      if (dest === p || dest.startsWith(p + "/")) continue; // no se puede mover una carpeta dentro de sí misma
      const parent = p.slice(0, p.lastIndexOf("/")) || "/";
      const l = this.dirs.get(parent) ?? [];
      const i = l.findIndex((e) => e.path === p);
      if (i < 0 || parent === dest) continue;
      const [e] = l.splice(i, 1);
      const np = `${dest === "/" ? "" : dest}/${e.name}`;
      target.push({ ...e, path: np });
      for (const [k, v] of [...this.dirs]) {
        if (k === p || k.startsWith(p + "/")) {
          this.dirs.delete(k);
          this.dirs.set(np + k.slice(p.length), v.map((c) => ({ ...c, path: np + c.path.slice(p.length) })));
        }
      }
      n++;
    }
    return n;
  }

  restore(paths: string[]) {
    let n = 0;
    for (const p of paths) {
      const i = this.trash.findIndex((e) => e.path === p);
      if (i < 0) continue;
      const e = this.trash.splice(i, 1)[0];
      this.deletedAt.delete(p);
      const parent = p.slice(0, p.lastIndexOf("/")) || "/";
      this.dirs.get(parent)?.push(e);
      n++;
    }
    return n;
  }
}

// ---- Usuarios ---------------------------------------------------------------------

export function users(): User[] {
  return [
    { name: "federico", full: "Federico Pereira", email: "federico@casa.lan", groups: ["administrators", "familia"], admin: true, twofa: true, lastLogin: iso(2 * 60_000), quotaUsed: 38 * GiB, quota: null, color: "#2F7CF6" },
    { name: "sofia", full: "Sofía Pereira", email: "sofia@casa.lan", groups: ["familia"], admin: false, twofa: false, lastLogin: iso(5 * H), quotaUsed: 21 * GiB, quota: 200 * GiB, color: "#EC4899" },
    { name: "martin", full: "Martín Gómez", email: "martin@estudio.lan", groups: ["familia"], admin: false, twofa: true, lastLogin: iso(3 * D), quotaUsed: 2 * GiB, quota: 100 * GiB, color: "#22C55E" },
    { name: "tv-living", full: "Smart TV del living", email: "", groups: ["media"], admin: false, twofa: false, lastLogin: iso(40 * 60_000), quotaUsed: 0, quota: 0, color: "#8E8E96" },
  ];
}

export function groups(): Group[] {
  return [
    { name: "administrators", description: t("Administran el NAS"), members: ["federico"] },
    { name: "familia", description: "Acceso a Familia, Fotos y Documentos", members: ["federico", "sofia", "martin"] },
    { name: "media", description: "Solo lectura en Media", members: ["tv-living"] },
  ];
}

export function sessions(): Session[] {
  return [
    { id: "s1", user: "federico", client: "web", ip: "192.168.1.23", since: iso(2 * 60_000), current: true },
    { id: "s2", user: "federico", client: "desk-win", ip: "192.168.1.31", since: iso(3 * H) },
    { id: "s3", user: "sofia", client: "smb", ip: "192.168.1.44", since: iso(5 * H) },
    { id: "s4", user: "tv-living", client: "smb", ip: "192.168.1.60", since: iso(40 * 60_000) },
  ];
}

export function initialNotices(s: Scenario): Notice[] {
  const base: Notice[] = [
    { id: "n-up", level: "info", app: "tango.settings", title: t("Actualización disponible"), body: t("TangoOS {version} y {n} parches de seguridad de Debian.", { version: "0.1.1", n: 14 }), at: iso(3 * H), action: { label: t("Ver"), app: "tango.settings", route: "updates" } },
    { id: "n-snap", level: "ok", app: "tango.storage", title: t("Instantáneas creadas"), body: t("{n} carpetas compartidas, {size} nuevos.", { n: 5, size: fmtBytes(1.2e9) }), at: iso(37 * 60_000) },
  ];
  if (s === "degraded")
    base.unshift({ id: "n-deg", level: "crit", app: "tango.storage", title: t("{name} sin redundancia", { name: "volume1" }), body: t("El disco de la bahía {bay} dejó de responder. Tus datos siguen completos en el disco {other}.", { bay: 2, other: 1 }), at: iso(9 * 60_000), action: { label: t("Ver qué hacer"), app: "tango.storage" } });
  if (s === "migrate")
    base.unshift({ id: "n-mig", level: "info", app: "tango.storage", title: t("Encontramos discos de otro NAS"), body: t("Un volumen RAID híbrido de {n} discos. Podés montarlo en solo lectura y revisar tus datos.", { n: 2 }), at: iso(60_000), action: { label: t("Abrir Almacenamiento"), app: "tango.storage" } });
  if (s === "empty")
    base.splice(0, 2, { id: "n-new", level: "info", app: "tango.storage", title: t("Tu NAS está listo"), body: t("Creá tu primer volumen para empezar a guardar archivos."), at: iso(60_000), action: { label: t("Crear volumen"), app: "tango.storage" } });
  return base;
}
