// Replicación ZFS simulada (protocol/README.md §18.1). Con ?scenario=zfs: una tarea que
// manda vol1 cada hora a "nas-oficina", y una réplica recibida de "nas-casa".
import { RpcError } from "../protocol";
import type { Job, Method, Notice, Params, Result, Scenario, Topic, Topics, Volume } from "../protocol";
import type { ReplReplica, ReplSource, ReplStatus, ReplTarget, ReplTask } from "../protocol-zfsrepl";

export interface MockReplCtx {
  scenario: () => Scenario;
  vols: () => Volume[];
  setVols: (v: Volume[]) => void;
  emit: <T extends Topic>(topic: T, d: Topics[T]) => void;
  startJob: (type: string, title: string, detail: string, progress: number, step: number, rate?: string, onDone?: () => void) => Job & { onTick?: (p: number) => void };
  notify: (level: Notice["level"], app: string, title: string, body: string) => void;
}

const H = 3600e3;
const iso = (agoMs: number) => new Date(Date.now() - agoMs).toISOString();
const snapName = (task: string, d: Date) => `tgrepl-${task}-${d.toISOString().replace(/[-:]/g, "").replace(/\.\d+Z$/, "Z")}`;

export function replHandlers(ctx: MockReplCtx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  let st: ReplStatus | null = null;
  const probes = new Map<string, { name: string; address: string; fingerprint: string }>();
  const changed = () => ctx.emit("storage.changed", { what: "repl" });
  const state = (): ReplStatus => {
    if (st) return st;
    const demo = ctx.scenario() === "zfs";
    const targets: ReplTarget[] = demo ? [{ id: "7c41d09a2be3", name: "nas-oficina", addr: "192.168.1.40:24020", fingerprint: "7C41 D09A 2BE3 51F0 98AA 03C2 6E1D 44B7", addedAt: iso(30 * 24 * H) }] : [];
    const sources: ReplSource[] = demo ? [{ id: "a83f2c19d0e4", name: "nas-casa", fingerprint: "A83F 2C19 D0E4 7B21 C5D9 0F3E 1288 AB60", addedAt: iso(12 * 24 * H) }] : [];
    const tasks: ReplTask[] = demo
      ? [
          {
            id: "k3x9q2m1", pool: "tanque", vol: "vol1", target: "7c41d09a2be3", every: 1, enabled: true, volumeId: "vol1", targetLabel: "nas-oficina",
            lastOk: iso(0.4 * H), last: { at: iso(0.4 * H), ok: true, bytes: 1.8 * 1024 ** 3, seconds: 74, snap: snapName("k3x9q2m1", new Date(Date.now() - 0.42 * H)) }, next: iso(-0.6 * H),
          },
        ]
      : [];
    const replicas: ReplReplica[] = demo
      ? [{ pool: "tanque", origin: "a83f2c19d0e4", from: "nas-casa", vol: "volume1", used: 412 * 1024 ** 3, encrypted: true, partial: false, lastSnap: snapName("p0w8r7t2", new Date(Date.now() - 2 * H)), lastAt: iso(2 * H), snapshots: 38 }]
      : [];
    st = {
      identity: { name: "tangonas", fingerprint: "3E9B 77A0 C412 08DD 5F61 B2C9 0A7E 9D13" },
      receive: { enabled: demo, pool: demo ? "tanque" : "", port: 24020, addrs: ["192.168.1.20"] },
      targets, sources, tasks, replicas,
    };
    return st;
  };
  const task = (id: string) => {
    const t = state().tasks.find((x) => x.id === id);
    if (!t) throw new RpcError("NOT_FOUND", "No existe la tarea");
    return t;
  };
  const run = (t: ReplTask, manual: boolean) => {
    if (t.job) return t.job;
    const first = !t.lastOk;
    const j = ctx.startJob("storage.zfs.repl", `Replicando ${t.vol}`, `A ${t.targetLabel}`, 0, first ? 0.04 : 0.15, undefined, () => {
      const at = new Date();
      t.job = undefined;
      t.lastOk = at.toISOString();
      t.last = { at: t.lastOk, ok: true, bytes: (first ? 640 : 1.3) * 1024 ** 3, seconds: first ? 412 : 38, snap: snapName(t.id, at) };
      if (t.every > 0) t.next = new Date(at.getTime() + t.every * H).toISOString();
      if (t.target.startsWith("local:")) {
        const pool = t.target.slice(6);
        const r = state().replicas.find((x) => x.origin === "local" && x.vol === t.vol && x.pool === pool);
        if (r) Object.assign(r, { lastSnap: t.last.snap, lastAt: t.lastOk, snapshots: r.snapshots + 1 });
        else state().replicas.push({ pool, origin: "local", from: "Este NAS", vol: t.vol, used: 640 * 1024 ** 3, encrypted: false, partial: false, lastSnap: t.last.snap, lastAt: t.lastOk, snapshots: 12 });
      }
      if (manual) ctx.notify("ok", "tango.storage", `Se replicó ${t.vol}`, `Llegó a ${t.targetLabel}.`);
      changed();
    });
    t.job = j.id;
    changed();
    return j.id;
  };
  return {
    "storage.zfs.repl.status": () => structuredClone(state()),
    "storage.zfs.repl.receive": ({ enabled, pool }) => {
      if (enabled && !pool) throw new RpcError("POOL_NOT_FOUND", "Elegí un pool ZFS donde guardar las réplicas");
      Object.assign(state().receive, { enabled, pool });
      if (!enabled) state().receive.invite = undefined;
      changed();
      return {};
    },
    "storage.zfs.repl.invite": () => {
      if (!state().receive.enabled) throw new RpcError("NOT_SUPPORTED", 'Prendé "Recibir réplicas" primero');
      const abc = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
      const pick = () => Array.from({ length: 5 }, () => abc[Math.floor(Math.random() * abc.length)]).join("");
      const inv = { code: `${pick()}-${pick()}`, expires: new Date(Date.now() + 15 * 60e3).toISOString() };
      state().receive.invite = inv;
      changed();
      return inv;
    },
    "storage.zfs.repl.targets.probe": ({ address }) => {
      if (!address.trim() || address.includes("/")) throw new RpcError("INVALID_PARAMS", "Dirección inválida");
      if (address.startsWith("10.9.")) throw new RpcError("UNREACHABLE", `No se pudo conectar a ${address}:24020: ¿tiene "Recibir réplicas" prendido?`);
      const probe = Math.random().toString(36).slice(2, 12);
      const hex = () => Math.floor(Math.random() * 65536).toString(16).toUpperCase().padStart(4, "0");
      const p = { name: "nas-" + address.split(".").pop(), address: address.includes(":") ? address : `${address}:24020`, fingerprint: Array.from({ length: 8 }, hex).join(" ") };
      probes.set(probe, p);
      return { probe, ...p };
    },
    "storage.zfs.repl.targets.add": ({ probe, code }) => {
      const p = probes.get(probe);
      if (!p) throw new RpcError("NOT_FOUND", "La conexión de prueba venció: empezá de nuevo");
      if (code.replace(/[-\s]/g, "").length !== 10) throw new RpcError("INVALID_PARAMS", "El código tiene 10 caracteres");
      if (code.toUpperCase().startsWith("AAAAA")) throw new RpcError("PAIR_FAILED", "código incorrecto");
      probes.delete(probe);
      const t: ReplTarget = { id: p.fingerprint.replace(/ /g, "").slice(0, 12).toLowerCase(), name: p.name, addr: p.address, fingerprint: p.fingerprint, addedAt: new Date().toISOString() };
      state().targets = [...state().targets.filter((x) => x.id !== t.id), t];
      changed();
      return t;
    },
    "storage.zfs.repl.targets.remove": ({ id }) => {
      if (state().tasks.some((t) => t.target === id)) throw new RpcError("BUSY", "Hay tareas que mandan a ese destino: borralas primero");
      state().targets = state().targets.filter((t) => t.id !== id);
      changed();
      return {};
    },
    "storage.zfs.repl.sources.remove": ({ id }) => {
      state().sources = state().sources.filter((s) => s.id !== id);
      changed();
      return {};
    },
    "storage.zfs.repl.tasks.create": ({ volume, target, every, runNow }) => {
      const v = ctx.vols().find((x) => x.id === volume && x.kind === "zfs");
      if (!v) throw new RpcError("VOLUME_NOT_FOUND", "No existe el volumen ZFS");
      const pool = (v.pool ?? "").replace(/^zfs:/, "");
      if (target === `local:${pool}`) throw new RpcError("INVALID_PARAMS", "Elegí otro pool: una réplica en el mismo pool no protege de que falle");
      const tg = state().targets.find((x) => x.id === target);
      if (!target.startsWith("local:") && !tg) throw new RpcError("NOT_FOUND", "No existe ese destino");
      if (state().tasks.some((t) => t.vol === v.name && t.target === target)) throw new RpcError("ALREADY_EXISTS", `Ya hay una tarea que replica ${v.name} a ese destino`);
      const t: ReplTask = { id: Math.random().toString(36).slice(2, 10), pool, vol: v.name, target, every, enabled: true, volumeId: v.id, targetLabel: tg ? tg.name : `Pool ${target.slice(6)} (este NAS)` };
      state().tasks.push(t);
      const out: { id: string; job?: string } = { id: t.id };
      if (runNow) out.job = run(t, true);
      changed();
      return out;
    },
    "storage.zfs.repl.tasks.update": ({ id, every, enabled }) => {
      Object.assign(task(id), { every, enabled });
      changed();
      return {};
    },
    "storage.zfs.repl.tasks.delete": ({ id }) => {
      if (task(id).job) throw new RpcError("BUSY", "La tarea está enviando: esperá a que termine");
      state().tasks = state().tasks.filter((t) => t.id !== id);
      changed();
      return {};
    },
    "storage.zfs.repl.tasks.run": ({ id }) => ({ job: run(task(id), true) }),
    "storage.zfs.repl.replicas.promote": ({ pool, origin, vol, name, passphrase }) => {
      const r = state().replicas.find((x) => x.pool === pool && x.origin === origin && x.vol === vol);
      if (!r) throw new RpcError("NOT_FOUND", "No existe la réplica");
      if (!/^[a-z][a-z0-9]{0,31}$/.test(name)) throw new RpcError("INVALID_PARAMS", "El nombre debe empezar con una letra y tener solo minúsculas y números");
      if (ctx.vols().some((v) => v.name === name)) throw new RpcError("ALREADY_EXISTS", `Ya hay un volumen ${name}`);
      if (r.encrypted && passphrase === "error") throw new RpcError("WRONG_PASSPHRASE", "La frase no abre esta réplica");
      const locked = r.encrypted && !passphrase;
      state().replicas = state().replicas.filter((x) => x !== r);
      ctx.setVols([
        ...ctx.vols(),
        { id: `zvol_${name}`, name, mount: locked ? "" : `/${name}`, kind: "zfs", pool: `zfs:${pool}`, profile: "raidz1", fs: "zfs", size: 12.8 * 1024 ** 4, used: r.used, state: locked ? "locked" : "healthy", disks: [], lastScrub: null, growth30d: [], encrypted: r.encrypted, locked },
      ]);
      ctx.emit("storage.changed", { what: "volumes" });
      ctx.emit("shares.changed", {});
      changed();
      return { locked };
    },
    "storage.zfs.repl.replicas.delete": ({ pool, origin, vol }) => {
      state().replicas = state().replicas.filter((x) => !(x.pool === pool && x.origin === origin && x.vol === vol));
      changed();
      return {};
    },
  };
}
