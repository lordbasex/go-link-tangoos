import type { Transport, TransportState } from "../transport";
import { RpcError } from "../protocol";
import type { Disk, Evt, VolumeProfile, Job, MetricsFrame, Method, Notice, Params, PermissionEntry, Result, RtcStats, Scenario, Share, Snapshot, Topic, Topics, User, Volume } from "../protocol";
import { profileInfo, profileLabel } from "../raid";
import * as data from "./data";
import { appsHandlers } from "./apps";
import { m22Handlers } from "./m22";
import { m4Handlers } from "./m4";
import { usersHandlers } from "./users";
import { connectHandlers } from "./connect";
import { v1Handlers } from "./v1";
import { MockTerminals } from "./terminal";
import type { Transfers, XferProgress } from "../transfers";
import { mimeFor } from "../rtc/files";

const GiB = data.GiB;
const MiB = data.MiB;

type Handler = (p: any) => unknown;
type InternalJob = Job & { step: number; onDone?: () => void; onTick?: (p: number) => void };

/**
 * Simula un NAS completo en el navegador: estado, jobs con progreso, eventos y
 * métricas a 4 Hz. Implementa el mismo contrato que RtcTransport.
 */
export class MockTransport implements Transport {
  readonly kind = "mock" as const;
  private seq = 0;
  private subs = new Map<string, Set<(e: Evt<any>) => void>>();
  private stateSubs = new Set<(s: TransportState) => void>();
  private st: TransportState = "connecting";

  private disks: Disk[];
  private vols: Volume[];
  private shares: Share[];
  private snaps: Snapshot[];
  private plans = new Map<string, { name: string; profile: VolumeProfile; disks: string[]; token: string; expires: number }>();
  private deletePlans = new Map<string, { volume: string; name: string; token: string; expires: number }>();
  private perms = new Map<string, PermissionEntry[]>();
  private users: User[] = data.users();
  /** Usuario de la sesión simulada (auth.login); por defecto el de la demo. */
  private currentUser: string | null = null;
  private fs: data.MemFS;
  private jobs: InternalJob[] = [];
  private notices: Notice[];
  private mem = { used: 5.1 * GiB, cache: 6.4 * GiB };
  private cpu = 0.12;
  private net = { rx: 1.2 * MiB, tx: 0.3 * MiB };
  private cores = [0.1, 0.1, 0.1, 0.1];

  constructor(readonly scenario: Scenario = "healthy") {
    this.disks = data.disks(scenario);
    this.vols = data.volumes(scenario, this.disks);
    this.shares = data.shares(scenario);
    this.snaps = data.snapshots(scenario);
    this.fs = new data.MemFS(scenario !== "empty" && scenario !== "migrate" && scenario !== "lab");
    if (scenario === "lab") this.fs.mkdir("/Familia");
    this.notices = data.initialNotices(scenario);
    if (scenario === "busy") {
      this.startJob("storage.scrub", "Verificando volume1", "Scrub de md + Btrfs", 0.37, 0.0009, "142 MB/s");
      this.startJob("files.upload", "Subiendo 214 fotos a Fotos/2026", "desde Notebook de Sofi", 0.12, 0.004, "38 MB/s");
      this.startJob("files.copy", "Copiando Películas a Backups", "4 archivos, 41 GB", 0.64, 0.002, "96 MB/s");
    }
    setTimeout(() => this.setState("connected"), 350);
    // Los avisos críticos pendientes llegan como evento al conectar (toast persistente).
    setTimeout(() => this.notices.filter((n) => n.level === "crit").forEach((n) => this.emit("notice", n)), 1500);
    setInterval(() => this.tickMetrics(), 250);
    setInterval(() => this.tickJobs(), 250);
  }

  // ---- Transport ---------------------------------------------------------------

  async call<M extends Method>(method: M, params: Params<M>): Promise<Result<M>> {
    const h = this.handlers[method] as Handler | undefined;
    await sleep(30 + Math.random() * 90);
    if (!h) throw new RpcError("METHOD_NOT_FOUND", `Método desconocido: ${method}`);
    return structuredClone(h(params)) as Result<M>;
  }

  subscribe<T extends Topic>(topic: T, fn: (e: Evt<T>) => void): () => void {
    let set = this.subs.get(topic);
    if (!set) this.subs.set(topic, (set = new Set()));
    set.add(fn as (e: Evt<any>) => void);
    return () => set!.delete(fn as (e: Evt<any>) => void);
  }

  state() {
    return this.st;
  }

  onState(fn: (s: TransportState) => void) {
    this.stateSubs.add(fn);
    return () => this.stateSubs.delete(fn);
  }

  isReal() {
    return false;
  }

  onCapabilities() {
    return () => {};
  }

  onAuthLost() {
    return () => {};
  }

  private setState(s: TransportState) {
    this.st = s;
    this.stateSubs.forEach((f) => f(s));
  }

  private emit<T extends Topic>(topic: T, d: Topics[T]) {
    const e: Evt<T> = { topic, seq: ++this.seq, data: d };
    this.subs.get(topic)?.forEach((f) => f(e));
  }

  // ---- Handlers -----------------------------------------------------------------

  private handlers: { [M in Method]?: (p: Params<M>) => Result<M> } = {
    ...m22Handlers({ scenario: () => this.scenario, disks: () => this.disks, vols: () => this.vols, shares: () => this.shares, snaps: () => this.snaps, emit: (t, d) => this.emit(t, d), startJob: (...a) => this.startJob(...a), notify: (...a) => this.notify(...a) }),
    ...m4Handlers({ scenario: () => this.scenario, emit: (t, d) => this.emit(t, d), startJob: (...a) => this.startJob(...a), notify: (...a) => this.notify(...a), vols: () => this.vols, shares: () => this.shares }),
    ...v1Handlers({ emit: (t, d) => this.emit(t, d), startJob: (...a) => this.startJob(...a), notify: (...a) => this.notify(...a), shares: () => this.shares, fs: () => this.fs }),
    ...usersHandlers({ me: () => this.currentUser ?? "federico" }),
    ...connectHandlers({ me: () => this.currentUser ?? "federico", emit: (t, d) => this.emit(t, d) }),
    ...appsHandlers({ scenario: () => this.scenario, emit: (t, d) => this.emit(t, d), startJob: (...a) => this.startJob(...a), notify: (...a) => this.notify(...a), volumes: () => this.vols }),
    "auth.users": () => data.users().filter((u) => u.name !== "tv-living").map(({ name, full, color }) => ({ name, full, color })),
    "auth.resume": () => {
      throw new RpcError("UNAUTHENTICATED", "El mock no guarda sesiones");
    },
    "auth.logout": () => ({}),
    "auth.login": ({ user, password }) => {
      if (password === "error") throw new RpcError("INVALID_CREDENTIALS", "Usuario o contraseña incorrectos");
      const u = this.users.find((x) => x.name === user) ?? data.users()[0];
      this.currentUser = u.name;
      return { user: u };
    },
    "system.info": () => data.systemInfo(),
    "storage.disks.list": () => this.disks,
    "storage.volumes.list": () => this.vols,
    "storage.volumes.plan": ({ name, profile, disks }) => {
      if (!/^[a-z][a-z0-9_-]{1,30}$/.test(name)) throw new RpcError("INVALID_PARAMS", "Nombre de volumen inválido");
      if (this.vols.some((v) => v.name === name)) throw new RpcError("VOLUME_EXISTS", `Ya existe un volumen llamado ${name}`);
      const chosen = this.disks.filter((d) => disks.includes(d.id));
      if (chosen.length !== disks.length) throw new RpcError("DEVICE_CHANGED", "Un disco ya no está conectado");
      if (chosen.some((d) => d.use !== "free" || d.foreign)) throw new RpcError("DEVICE_NOT_ELIGIBLE", "Uno de los discos no es elegible");
      const info = profileInfo(profile);
      const why = info.id === profile ? info.check(chosen.length) : "Perfil desconocido";
      if (why) throw new RpcError("INVALID_PARAMS", why);
      const sizes = chosen.map((d) => d.size);
      const usable = info.usable(sizes) * 0.98;
      const minSize = Math.min(...sizes);
      const notes = sizes.some((x) => x !== minSize)
        ? [`Los discos tienen tamaños distintos: se usan ${fmtBytes(minSize)} de cada uno y sobran ${fmtBytes(sizes.reduce((a, x) => a + x - minSize, 0))}.`]
        : [];
      const plan_id = `p_${Math.random().toString(36).slice(2, 10)}`;
      const confirm_token = Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2);
      const expires = new Date(Date.now() + 5 * 60_000).toISOString();
      this.plans.set(plan_id, { name, profile, disks: [...disks], token: confirm_token, expires: Date.now() + 5 * 60_000 });
      return {
        plan_id, expires, confirm_token,
        summary: {
          name, profile, fs: "btrfs", usable, notes,
          disks: chosen.map((d) => ({ id: d.id, model: d.model, serial: d.serial, size: d.size, content: d.bay === 3 ? "Una partición ext4 (sin montar)" : "Vacío: sin tabla de particiones" })),
        },
      };
    },
    "storage.volumes.create": ({ plan_id, confirm_token, typed_name }) => {
      const plan = this.plans.get(plan_id);
      if (!plan) throw new RpcError("PLAN_EXPIRED", "El plan no existe o ya se usó");
      if (Date.now() > plan.expires) {
        this.plans.delete(plan_id);
        throw new RpcError("PLAN_EXPIRED", "El plan venció");
      }
      if (confirm_token !== plan.token || typed_name !== plan.name) throw new RpcError("CONFIRMATION_MISMATCH", "La confirmación no coincide con el plan");
      const { name, profile, disks } = plan;
      if (this.vols.some((v) => v.name === name)) throw new RpcError("VOLUME_EXISTS", `Ya existe un volumen llamado ${name}`);
      const chosen = this.disks.filter((d) => disks.includes(d.id));
      if (chosen.length !== disks.length) throw new RpcError("DEVICE_CHANGED", "Un disco cambió desde que armaste el plan");
      if (chosen.some((d) => d.use !== "free" || d.foreign)) throw new RpcError("DEVICE_NOT_ELIGIBLE", "Uno de los discos ya no es elegible");
      this.plans.delete(plan_id);
      const size = profileInfo(profile).usable(chosen.map((d) => d.size)) * 0.98;
      const vol: Volume = {
        id: `vol${this.vols.length + 1}`, name, mount: `/${name}`, profile, fs: "btrfs", size, used: 0, state: "building", progress: 0,
        disks, lastScrub: null, growth30d: [],
      };
      this.vols.push(vol);
      chosen.forEach((d) => Object.assign(d, { use: "member", volume: vol.id }));
      this.emit("storage.changed", { what: "volumes" });
      this.emit("storage.changed", { what: "disks" });
      const job = this.startJob("storage.volume.create", `Creando ${name}`, `${profileLabel(profile)} · ${chosen.length} disco${chosen.length > 1 ? "s" : ""}`, 0, 0.012, undefined, () => {
        vol.state = "healthy";
        vol.progress = undefined;
        this.emit("storage.changed", { what: "volumes" });
        this.notify("ok", "tango.storage", `${name} está listo`, `${fmtBytes(size)} disponibles con ${profile === "basic" ? "perfil básico" : profileLabel(profile)}.`);
      });
      job.onTick = (p) => {
        vol.progress = p;
        this.emit("storage.changed", { what: "volumes" });
      };
      return { job: job.id };
    },
    "shares.create": ({ name, volume, description, smb, timeMachine, timeMachineMaxBytes }) => {
      if (!/^[\p{L}\p{N} _-]{1,64}$/u.test(name)) throw new RpcError("INVALID_PARAMS", "Nombre de carpeta inválido");
      if (this.shares.some((x) => x.name.toLowerCase() === name.toLowerCase())) throw new RpcError("SHARE_EXISTS", `Ya existe una carpeta llamada ${name}`);
      const v = this.vols.find((x) => x.id === volume);
      if (!v) throw new RpcError("VOLUME_NOT_FOUND", "No existe el volumen");
      const id = name.toLowerCase().normalize("NFD").replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || `share-${this.shares.length + 1}`;
      const j = this.startJob("shares.create", `Creando carpeta ${name}`, smb ? "Subvolumen Btrfs + Samba" : "Subvolumen Btrfs", 0, 0.25, undefined, () => {
        this.shares.push({ id, name, volume, path: `${v.mount}/@shares/${name}`, size: 0, description, smb, nfs: false, snapshots: true, recycleBin: true, color: "#2F7CF6", timeMachine: !!(smb && timeMachine), timeMachineMaxBytes: smb && timeMachine ? (timeMachineMaxBytes ?? null) : null });
        this.fs.mkdir(`/${name}`);
        this.emit("shares.changed", { id });
        this.notify("ok", "tango.settings", "Carpeta compartida creada", smb ? `${name} ya se ve en la red (SMB).` : `${name} está lista.`);
      });
      return { job: j.id };
    },
    "storage.volumes.deletePlan": ({ volume }) => {
      const v = this.vols.find((x) => x.id === volume);
      if (!v) throw new RpcError("VOLUME_NOT_FOUND", "No existe el volumen");
      if (v.state === "building" || v.state === "scrubbing") throw new RpcError("VOLUME_BUSY", "El volumen está ocupado: esperá a que termine la tarea en curso");
      const plan_id = `d_${Math.random().toString(36).slice(2, 10)}`;
      const confirm_token = Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2);
      this.deletePlans.set(plan_id, { volume, name: v.name, token: confirm_token, expires: Date.now() + 5 * 60_000 });
      const shares = this.shares.filter((s) => s.volume === volume);
      return {
        plan_id, confirm_token, expires: new Date(Date.now() + 5 * 60_000).toISOString(),
        summary: {
          name: v.name,
          shares: shares.map((s) => s.name),
          snapshots: this.snaps.filter((s) => shares.some((x) => x.id === s.share)).length,
          used: v.used,
          disks: this.disks.filter((d) => v.disks.includes(d.id)).map((d) => ({ id: d.id, model: d.model, serial: d.serial })),
        },
      };
    },
    "storage.volumes.delete": ({ plan_id, confirm_token, typed_name }) => {
      const plan = this.deletePlans.get(plan_id);
      if (!plan) throw new RpcError("PLAN_EXPIRED", "El plan no existe o ya se usó");
      if (Date.now() > plan.expires) {
        this.deletePlans.delete(plan_id);
        throw new RpcError("PLAN_EXPIRED", "El plan venció");
      }
      if (confirm_token !== plan.token || typed_name !== plan.name) throw new RpcError("CONFIRMATION_MISMATCH", "La confirmación no coincide con el plan");
      const v = this.vols.find((x) => x.id === plan.volume);
      if (!v) throw new RpcError("VOLUME_NOT_FOUND", "El volumen ya no existe");
      this.deletePlans.delete(plan_id);
      const j = this.startJob("storage.volume.delete", `Eliminando ${v.name}`, "Desmontar, detener el array y limpiar los discos", 0, 0.06, undefined, () => {
        const gone = this.shares.filter((s) => s.volume === v.id).map((s) => s.id);
        this.shares = this.shares.filter((s) => s.volume !== v.id);
        this.snaps = this.snaps.filter((s) => !gone.includes(s.share));
        this.vols = this.vols.filter((x) => x.id !== v.id);
        this.disks.forEach((d) => {
          if (d.volume === v.id) Object.assign(d, { use: "free", volume: undefined });
        });
        this.emit("storage.changed", { what: "volumes" });
        this.emit("storage.changed", { what: "disks" });
        this.emit("shares.changed", {});
        this.notify("ok", "tango.storage", `${v.name} se eliminó`, "Los discos quedaron libres para un volumen nuevo.");
      });
      return { job: j.id };
    },
    "shares.delete": ({ share, typed_name }) => {
      const s = this.shares.find((x) => x.id === share);
      if (!s) throw new RpcError("SHARE_NOT_FOUND", "No existe la carpeta compartida");
      if (typed_name !== s.name) throw new RpcError("CONFIRMATION_MISMATCH", "El nombre escrito no coincide");
      const j = this.startJob("shares.delete", `Eliminando ${s.name}`, "Subvolumen, instantáneas y bloque SMB", 0, 0.2, undefined, () => {
        this.shares = this.shares.filter((x) => x.id !== share);
        this.snaps = this.snaps.filter((x) => x.share !== share);
        this.perms.delete(share);
        this.emit("shares.changed", { id: share });
        this.emit("snapshots.changed", { share });
        this.notify("ok", "tango.settings", "Carpeta compartida eliminada", `${s.name} y sus instantáneas se borraron.`);
      });
      return { job: j.id };
    },
    "shares.permissions.get": ({ share }) => {
      if (!this.shares.some((x) => x.id === share)) throw new RpcError("SHARE_NOT_FOUND", "No existe la carpeta compartida");
      const saved = this.perms.get(share);
      if (saved) return { entries: saved };
      const entries: PermissionEntry[] = [
        { kind: "group", name: "administrators", access: "rw" },
        ...data.groups().filter((g) => g.name !== "administrators").map((g) => ({ kind: "group" as const, name: g.name, access: (g.name === "media" ? "ro" : "rw") as PermissionEntry["access"] })),
        ...this.users.map((u) => ({ kind: "user" as const, name: u.name, access: (u.admin ? "rw" : "none") as PermissionEntry["access"] })),
      ];
      return { entries };
    },
    "shares.permissions.set": ({ share, entries }) => {
      if (!this.shares.some((x) => x.id === share)) throw new RpcError("SHARE_NOT_FOUND", "No existe la carpeta compartida");
      if (entries.some((e) => !["rw", "ro", "none"].includes(e.access))) throw new RpcError("INVALID_PARAMS", "Permiso inválido");
      this.perms.set(share, entries.map((e) => ({ ...e })));
      this.emit("shares.changed", { id: share });
      return {};
    },
    "storage.scrub.start": ({ volume }) => {
      const v = this.vols.find((x) => x.id === volume);
      if (!v) throw new RpcError("VOLUME_NOT_FOUND", "No existe el volumen");
      if (v.state !== "healthy") throw new RpcError("VOLUME_BUSY", "El volumen está ocupado");
      v.state = "scrubbing";
      v.progress = 0;
      this.emit("storage.changed", { what: "volumes" });
      const j = this.startJob("storage.scrub", `Verificando ${v.name}`, "Scrub de md + Btrfs", 0, 0.006, "151 MB/s", () => {
        v.state = "healthy";
        v.progress = undefined;
        v.lastScrub = new Date().toISOString();
        this.emit("storage.changed", { what: "volumes" });
        this.notify("ok", "tango.storage", `Verificación de ${v.name} terminada`, "Sin errores. Todos los datos coinciden con sus checksums.");
      });
      j.onTick = (p) => {
        v.progress = p;
        this.emit("storage.changed", { what: "volumes" });
      };
      return { job: j.id };
    },
    "storage.foreign.mount": ({ label }) => {
      const j = this.startJob("storage.foreign.mount", "Montando volumen de Synology", `${label} · solo lectura`, 0, 0.02, undefined, () => {
        this.notify("ok", "tango.storage", "Volumen de Synology montado", "Está en /import/volume_1 en solo lectura. Nada se modificó en los discos.");
      });
      return { job: j.id };
    },
    "shares.list": () => this.shares,
    "snapshots.list": ({ share }) => (share ? this.snaps.filter((s) => s.share === share) : this.snaps),
    "snapshots.create": ({ share }) => {
      const name = this.shares.find((s) => s.id === share)?.name ?? share;
      const j = this.startJob("snapshots.create", `Instantánea de ${name}`, "Btrfs, solo lectura", 0, 0.08, undefined, () => {
        this.snaps.unshift({ id: `${share}-${Date.now()}`, share, at: new Date().toISOString(), size: 12 * MiB, kind: "manual", locked: false });
        this.emit("snapshots.changed", { share });
        this.notify("ok", "tango.storage", "Instantánea creada", `Se guardó el estado actual de ${name}.`);
      });
      return { job: j.id };
    },
    "files.list": ({ path }) => {
      const l = this.fs.list(path);
      if (!l) throw new RpcError("PATH_NOT_FOUND", `No existe ${path}`);
      return l;
    },
    "files.search": ({ q, path }) => this.fs.search(q, path),
    "files.move": ({ paths, dest }) => {
      if (!this.fs.list(dest)) throw new RpcError("PATH_NOT_FOUND", `No existe ${dest}`);
      const count = this.fs.move(paths, dest);
      new Set([...paths.map(parentOf), dest]).forEach((dir) => this.emit("files.changed", { dir }));
      return { count };
    },
    "files.trash.list": ({ share }) =>
      this.fs.trash
        .filter((e) => !share || e.path.toLowerCase().startsWith(`/${share.toLowerCase()}/`))
        .map((e) => ({ name: e.name, path: e.path, origin: parentOf(e.path), deletedAt: this.fs.deletedAt.get(e.path) ?? new Date().toISOString(), size: e.size, kind: e.kind })),
    "files.trash.empty": ({ share }) => {
      const count = this.fs.emptyTrash(share);
      this.emit("files.changed", { dir: "trash" });
      return { count };
    },
    "files.restoreVersion": ({ path, snapshot }) => {
      const e = this.fs.find(path);
      const snap = this.snaps.find((s) => s.id === snapshot);
      if (!e || !snap) throw new RpcError("PATH_NOT_FOUND", "No existe esa versión");
      const when = new Date(snap.at).toLocaleString("es-AR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).replace(/[/:]/g, ".").replace(",", "");
      const dot = e.name.lastIndexOf(".");
      const name = dot > 0 ? `${e.name.slice(0, dot)} (restaurado ${when})${e.name.slice(dot)}` : `${e.name} (restaurado ${when})`;
      const dir = parentOf(path);
      const copy = this.fs.add(dir, name, e.size);
      this.emit("files.changed", { dir });
      return copy;
    },
    "files.thumb": () => {
      throw new RpcError("NOT_SUPPORTED", "El demo genera sus propias miniaturas");
    },
    "files.mkdir": ({ path }) => {
      const e = this.fs.mkdir(path);
      this.emit("files.changed", { dir: parentOf(path) });
      return e;
    },
    "files.rename": ({ path, name }) => {
      if (/[/\0]/.test(name)) throw new RpcError("INVALID_NAME", "El nombre no puede tener /");
      const e = this.fs.rename(path, name);
      if (!e) throw new RpcError("PATH_NOT_FOUND", `No existe ${path}`);
      this.emit("files.changed", { dir: parentOf(path) });
      return e;
    },
    "files.trash": ({ paths }) => {
      const count = this.fs.trashPaths(paths);
      new Set(paths.map(parentOf)).forEach((dir) => this.emit("files.changed", { dir }));
      return { count };
    },
    "files.restore": ({ paths }) => {
      const count = this.fs.restore(paths);
      new Set(paths.map(parentOf)).forEach((dir) => this.emit("files.changed", { dir }));
      return { count };
    },
    "files.versions": ({ path }) => {
      const share = path.split("/")[1]?.toLowerCase();
      const e = this.fs.find(path);
      return this.snaps
        .filter((s) => s.share === share)
        .slice(0, 8)
        .map((s, i) => ({ snapshot: s, size: Math.max(0, Math.round((e?.size ?? 0) * (1 - i * 0.03))), mtime: new Date(new Date(s.at).getTime() - 3_600_000).toISOString() }));
    },
    "users.list": () => this.users,
    "users.create": ({ name, full, email, password, admin, groups }) => {
      if (!/^[a-z][a-z0-9_-]{1,31}$/.test(name)) throw new RpcError("INVALID_PARAMS", "Nombre de usuario inválido");
      if (this.users.some((u) => u.name === name)) throw new RpcError("USER_EXISTS", `Ya existe el usuario ${name}`);
      if (password.length < 10) throw new RpcError("WEAK_PASSWORD", "La contraseña tiene que tener al menos 10 caracteres");
      const palette = ["#2F7CF6", "#EC4899", "#22C55E", "#F97316", "#8B5CF6", "#EAB308"];
      const u: User = {
        name, full: full || name, email, groups: admin ? ["administrators", ...groups.filter((g) => g !== "administrators")] : groups, admin, twofa: false,
        lastLogin: "", quotaUsed: 0, quota: null, color: palette[this.users.length % palette.length],
      };
      this.users = [...this.users, u];
      return u;
    },
    "users.delete": ({ name, typed_name, keepFiles: _keep }) => {
      const u = this.users.find((x) => x.name === name);
      if (!u) throw new RpcError("USER_NOT_FOUND", `No existe el usuario ${name}`);
      if (typed_name !== name) throw new RpcError("CONFIRMATION_MISMATCH", "Escribí el nombre del usuario para confirmar");
      if (name === (this.currentUser ?? "federico")) throw new RpcError("CANNOT_DELETE_SELF", "No podés borrar tu propio usuario");
      if (u.admin && this.users.filter((x) => x.admin).length <= 1) throw new RpcError("LAST_ADMIN", "No se puede borrar el último administrador");
      this.users = this.users.filter((x) => x.name !== name);
      this.emit("users.changed", { user: name });
      this.emit("shares.changed", {});
      return {};
    },
    "users.setPassword": ({ name, password }) => {
      if (!this.users.some((u) => u.name === name)) throw new RpcError("USER_NOT_FOUND", `No existe el usuario ${name}`);
      if (password.length < 10) throw new RpcError("WEAK_PASSWORD", "La contraseña tiene que tener al menos 10 caracteres");
      return {};
    },
    "groups.list": () => data.groups(),
    "sessions.list": () => data.sessions(),
    "jobs.list": () => this.jobs.map(strip),
    "notices.list": () => this.notices,
    "rtc.stats": () => this.rtcStats(),
  };

  // ---- Transferencias simuladas (protocol §10) -------------------------------------------

  readonly terminals = new MockTerminals();

  readonly transfers: Transfers = {
    upload: async (dir, file, onProgress, opts = {}) => {
      if (!this.fs.list(dir)) throw new RpcError("PATH_NOT_FOUND", `No existe ${dir}`);
      const target = `${dir === "/" ? "" : dir}/${file.name}`;
      if (this.fs.has(target) && !opts.overwrite) throw new RpcError("EXISTS", `Ya existe «${file.name}»`);
      await this.simulateXfer(file.size, onProgress, opts.signal);
      const e = this.fs.add(dir, file.name, file.size);
      this.emit("files.changed", { dir });
      return e;
    },
    download: async (path, onProgress, opts = {}) => {
      const e = this.fs.find(path);
      if (!e || e.kind === "dir") throw new RpcError("PATH_NOT_FOUND", `No existe ${path}`);
      await this.simulateXfer(e.size, onProgress, opts.signal);
      const body = new TextEncoder().encode(`TangoOS · demo\n\nContenido simulado de «${e.name}» (${e.size} bytes en el NAS de prueba).\n`);
      if (opts.sink) {
        await opts.sink.write(body, 0);
        await opts.sink.close();
        return null;
      }
      return new Blob([body], { type: mimeFor(e.name) });
    },
  };

  /** Progreso realista: 40–90 MB/s, pero nunca más de ~4 s para no aburrir en la demo. */
  private simulateXfer(total: number, onProgress?: (p: XferProgress) => void, signal?: AbortSignal) {
    const rate = Math.max(total / 4, (40 + Math.random() * 50) * MiB);
    return new Promise<void>((resolve, reject) => {
      let bytes = 0;
      const tick = () => {
        if (signal?.aborted) return reject(new RpcError("CANCELLED", "Transferencia cancelada"));
        bytes = Math.min(total, bytes + rate * 0.1 * (0.8 + Math.random() * 0.4));
        onProgress?.({ bytes, total, rate });
        if (bytes >= total) resolve();
        else setTimeout(tick, 100);
      };
      setTimeout(tick, 60);
    });
  }

  // ---- Jobs ---------------------------------------------------------------------------

  private startJob(type: string, title: string, detail: string, progress: number, step: number, rate?: string, onDone?: () => void) {
    const job: InternalJob = {
      id: `j_${Math.random().toString(36).slice(2, 8)}`, type, title, detail, progress, state: "running", startedAt: new Date().toISOString(), rate, step, onDone,
    };
    this.jobs.push(job);
    this.emit("jobs.changed", strip(job));
    return job;
  }

  private tickJobs() {
    for (const j of this.jobs) {
      if (j.state !== "running") continue;
      j.progress = Math.min(1, j.progress + j.step * (0.6 + Math.random() * 0.8));
      j.onTick?.(j.progress);
      if (j.progress >= 1) {
        j.state = "succeeded";
        j.onDone?.();
        setTimeout(() => {
          this.jobs = this.jobs.filter((x) => x !== j);
          this.emit("jobs.changed", strip(j));
        }, 4000);
      }
      this.emit("jobs.changed", strip(j));
    }
  }

  private notify(level: Notice["level"], app: string, title: string, body: string) {
    const n: Notice = { id: `n_${Date.now()}`, level, app, title, body, at: new Date().toISOString() };
    this.notices.unshift(n);
    this.emit("notice", n);
  }

  // ---- Métricas -----------------------------------------------------------------------

  private tickMetrics() {
    const busy = this.jobs.some((j) => j.state === "running");
    const target = busy ? 0.34 : 0.1;
    this.cpu = clamp(this.cpu + (target - this.cpu) * 0.08 + (Math.random() - 0.5) * 0.06 + (Math.random() < 0.015 ? 0.35 : 0), 0.02, 0.98);
    this.cores = this.cores.map((c, i) => clamp(c + (this.cpu - c) * 0.3 + (Math.random() - 0.5) * 0.12 + (i === 0 ? 0.03 : 0), 0.01, 1));
    this.mem.used = clamp(this.mem.used + (Math.random() - 0.5) * 40 * MiB, 4.6 * GiB, 7.5 * GiB);
    const rxT = busy ? 48 * MiB : 1.4 * MiB;
    this.net.rx = Math.max(0, this.net.rx + (rxT - this.net.rx) * 0.1 + (Math.random() - 0.5) * rxT * 0.3);
    this.net.tx = Math.max(0, this.net.tx + (rxT * 0.25 - this.net.tx) * 0.1 + (Math.random() - 0.5) * rxT * 0.1);
    const f: MetricsFrame = {
      t: Date.now(), cpu: this.cpu, perCore: [...this.cores], load: [this.cpu * 4 * 0.9, 0.62, 0.48],
      memUsed: this.mem.used, memCache: this.mem.cache, memTotal: 16 * GiB,
      netRx: this.net.rx, netTx: this.net.tx,
      diskRead: busy ? 140 * MiB * (0.8 + Math.random() * 0.4) : Math.random() * 3 * MiB,
      diskWrite: busy ? 42 * MiB * (0.7 + Math.random() * 0.6) : Math.random() * 1.5 * MiB,
      tempCpu: 44 + this.cpu * 28 + Math.random(), fanRpm: Math.round(900 + this.cpu * 1400),
    };
    this.emit("metrics", f);
  }

  private rtcStats(): RtcStats {
    const r = () => Math.random();
    return {
      ice: "connected",
      pair: { local: "192.168.1.23:52114", remote: "192.168.1.50:8443", type: "host", protocol: "udp" },
      rttMs: 0.6 + r() * 0.8,
      dtls: "DTLS 1.2 · ECDHE-ECDSA-AES128-GCM-SHA256",
      channels: [
        { label: "rpc", ordered: true, buffered: 0, rate: 1800 + r() * 900, messages: 1204 },
        { label: "events", ordered: true, buffered: 0, rate: 3200 + r() * 1200, messages: 8812 },
        { label: "metrics", ordered: false, buffered: 0, rate: 1400 + r() * 50, messages: 22410 },
        ...[0, 1, 2, 3].map((i) => ({ label: `files.${i}`, ordered: true, buffered: this.jobs.length ? r() * 2 * MiB : 0, rate: this.jobs.length ? 9 * MiB * r() : 0, messages: 120 * i })),
        { label: "term", ordered: true, buffered: 0, rate: 0, messages: 0 },
      ],
    };
  }
}

function strip(j: InternalJob): Job {
  const { step: _s, onDone: _d, onTick: _t, ...rest } = j;
  return rest;
}
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const clamp = (v: number, a: number, b: number) => Math.min(b, Math.max(a, v));
const parentOf = (p: string) => p.slice(0, p.lastIndexOf("/")) || "/";

export function fmtBytes(n: number, digits = 1): string {
  const u = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  while (n >= 1000 && i < u.length - 1) {
    n /= 1000;
    i++;
  }
  return `${n.toLocaleString("es-AR", { maximumFractionDigits: i === 0 ? 0 : digits })} ${u[i]}`;
}
