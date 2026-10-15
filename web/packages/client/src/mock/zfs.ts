// Pools ZFS simulados (protocol/README.md §18). Datos DEMO: con ?scenario=zfs hay un pool
// RAIDZ1 con caché L2ARC; en los demás escenarios ZFS está sin habilitar y "Habilitar
// ZFS" simula la instalación con DKMS.
import { RpcError } from "../protocol";
import type { DeletePlan, Disk, Job, Method, Notice, Params, Result, Scenario, Topic, Topics, Volume } from "../protocol";
import type { ZfsImportable, ZfsLayout, ZfsPlan, ZfsPool, ZfsScrubSchedule, ZfsVdev } from "../protocol-zfs";
import { zfsCheck, zfsLayoutLabel, zfsTolerance, zfsUsable } from "../protocol-zfs";
import { fmtBytes } from "../format";

export interface MockZfsCtx {
  scenario: () => Scenario;
  disks: () => Disk[];
  vols: () => Volume[];
  setVols: (v: Volume[]) => void;
  emit: <T extends Topic>(topic: T, d: Topics[T]) => void;
  startJob: (type: string, title: string, detail: string, progress: number, step: number, rate?: string, onDone?: () => void) => Job & { onTick?: (p: number) => void };
  notify: (level: Notice["level"], app: string, title: string, body: string) => void;
}

interface PlanRec {
  kind: "create" | "add" | "replace" | "destroy" | "voldelete";
  name: string;
  layout?: ZfsLayout;
  addKind?: "data" | "cache" | "log";
  pool?: string;
  old?: string;
  volume?: string;
  disks: string[];
  token: string;
  expires: number;
}

const TiB = 1024 ** 4;
const iso = (agoMs: number) => new Date(Date.now() - agoMs).toISOString();

const leafOf = (d: Disk, state = "ONLINE") => ({ id: d.id, name: d.id, guid: String(1e18 + d.size + (d.bay ?? 0)), state, read: 0, write: 0, cksum: 0 });

export function zfsHandlers(ctx: MockZfsCtx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  let enabled: boolean | null = null;
  let enabling: string | undefined;
  let pools: ZfsPool[] | null = null;
  let importable: ZfsImportable[] = [];
  const plans = new Map<string, PlanRec>();
  const schedules = new Map<string, ZfsScrubSchedule>();

  const state = (): ZfsPool[] => {
    if (pools) return pools;
    pools = [];
    enabled = ctx.scenario() === "zfs";
    if (ctx.scenario() === "zfs") {
      const data = ctx.disks().filter((d) => d.pool === "zfs:tanque" && d.role === "data");
      const cache = ctx.disks().filter((d) => d.pool === "zfs:tanque" && d.role === "cache");
      const raw = data.reduce((a, d) => a + d.size, 0);
      pools.push({
        id: "zfs:tanque", name: "tanque", guid: "4815162342108", health: "ONLINE", state: "healthy", readonly: false, layout: "raidz1",
        size: 21.4 * TiB, allocated: 8.6 * TiB, free: 12.8 * TiB, raw, frag: 3,
        vdevs: [{ name: "raidz1-0", type: "raidz1", state: "ONLINE", read: 0, write: 0, cksum: 0, size: raw, disks: data.map((d) => leafOf(d)) }],
        logs: [], cache: cache.map((d) => leafOf(d)),
        scan: { function: "scrub", state: "finished", progress: 1, start: iso(6 * 86400e3 + 3 * 3600e3), end: iso(6 * 86400e3), errors: 0 },
        volumes: ["vol1", "vol2"], zvols: [{ name: "vm-windows", size: 500 * 1024 ** 3, used: 61 * 1024 ** 3, device: "/dev/zvol/tanque/zvol/vm-windows" }],
        encrypted: ["vol2"], disks: data.map((d) => d.id), other: [],
      });
      importable = [{ name: "respaldo", guid: "9001237718201", state: "ONLINE", status: "Se usó por última vez en otro equipo.", disks: [] }];
    }
    return pools;
  };
  const isEnabled = () => {
    state();
    return !!enabled;
  };
  const changed = (...what: Topics["storage.changed"]["what"][]) => {
    for (const w of ["zfs", "pools", "volumes", "disks", ...what] as const) ctx.emit("storage.changed", { what: w });
  };
  const find = (id: string) => {
    const p = state().find((x) => x.id === id || x.name === id);
    if (!p) throw new RpcError("POOL_NOT_FOUND", "No existe el pool");
    return p;
  };
  const need = () => {
    if (!isEnabled()) throw new RpcError("ZFS_UNAVAILABLE", "ZFS no está habilitado: habilitalo en Almacenamiento → Pools ZFS");
  };
  const newPlan = (rec: Omit<PlanRec, "token" | "expires">) => {
    const plan_id = `z_${Math.random().toString(36).slice(2, 10)}`;
    const token = Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2);
    const expires = Date.now() + 5 * 60_000;
    plans.set(plan_id, { ...rec, token, expires });
    return { plan_id, confirm_token: token, expires: new Date(expires).toISOString() };
  };
  const take = (kind: PlanRec["kind"], p: { plan_id: string; confirm_token: string; typed_name: string }) => {
    const rec = plans.get(p.plan_id);
    if (!rec || rec.kind !== kind || Date.now() > rec.expires) throw new RpcError("PLAN_EXPIRED", "El plan venció o no existe: volvé a revisar los discos");
    if (rec.token !== p.confirm_token || p.typed_name.trim() !== rec.name) throw new RpcError("CONFIRMATION_MISMATCH", "La confirmación no coincide con el plan");
    plans.delete(p.plan_id);
    return rec;
  };
  const freeDisks = (ids: string[]) => {
    const ds = ctx.disks().filter((d) => ids.includes(d.id));
    if (ds.length !== ids.length) throw new RpcError("DEVICE_NOT_FOUND", "Un disco ya no está conectado");
    if (ds.some((d) => d.use !== "free" || d.foreign)) throw new RpcError("DEVICE_NOT_ELIGIBLE", "Uno de los discos ya está en uso");
    return ds;
  };
  const planDisks = (ds: Disk[]) => ds.map((d) => ({ id: d.id, model: d.model, serial: d.serial, size: d.size, content: "Vacío: sin tabla de particiones" }));
  const summary = (name: string, kind: ZfsPlan["summary"]["kind"], layout: ZfsLayout, ds: Disk[]): ZfsPlan["summary"] => {
    const sizes = ds.map((d) => d.size);
    const m = Math.min(...sizes);
    const wasted = layout === "stripe" ? 0 : sizes.reduce((a, s) => a + s - m, 0);
    const tol = zfsTolerance(layout, ds.length);
    const notes = [tol === 0 ? "Sin redundancia: si falla un disco se pierde el pool entero." : tol === 1 ? "Sigue funcionando si falla un disco." : `Sigue funcionando si fallan hasta ${tol} discos a la vez.`];
    if (wasted > 0) notes.push(`Los discos tienen distinto tamaño: ZFS usa ${fmtBytes(m)} de cada uno y quedan ${fmtBytes(wasted)} sin usar.`);
    notes.push("La capacidad es aproximada: ZFS reserva alrededor de un 3 % para sí mismo.");
    return { name, kind, layout, usable: zfsUsable(layout, sizes), raw: sizes.reduce((a, b) => a + b, 0), wasted, tolerance: tol, disks: planDisks(ds), notes };
  };
  const job = (type: string, title: string, detail: string, step: number, done: () => void) => {
    const j = ctx.startJob(type, title, detail, 0, step, undefined, done);
    return { job: j.id };
  };

  return {
    "storage.zfs.status": () => ({ enabled: isEnabled(), installed: isEnabled(), version: isEnabled() ? "2.3.9-0+deb13u1" : undefined, enabling, pools: state() }),
    "storage.zfs.importable": () => {
      need();
      return importable;
    },
    "storage.zfs.enable": () => {
      if (isEnabled()) throw new RpcError("INVALID_PARAMS", "ZFS ya está habilitado");
      const j = ctx.startJob("storage.zfs.enable", "Habilitando ZFS", "Instala OpenZFS y compila el módulo del kernel (DKMS): tarda unos minutos", 0, 0.012, undefined, () => {
        enabled = true;
        enabling = undefined;
        changed();
        ctx.notify("ok", "tango.storage", "ZFS está habilitado", "Ya podés crear o importar pools ZFS en Almacenamiento.");
      });
      enabling = j.id;
      ctx.emit("storage.changed", { what: "zfs" });
      return { job: j.id };
    },

    "storage.zfs.plan": ({ name, layout, disks }) => {
      need();
      if (!/^[a-z][a-z0-9_]{0,31}$/.test(name) || /^(mirror|raidz|draid|log|cache|spare)/.test(name)) throw new RpcError("INVALID_PARAMS", "El nombre del pool debe empezar con una letra y tener solo minúsculas, números o _ (y no ser una palabra de ZFS)");
      if (state().some((p) => p.name === name) || ctx.vols().some((v) => v.name === name)) throw new RpcError("POOL_EXISTS", `Ya existe ${name}`);
      const why = zfsCheck(layout, disks.length);
      if (why) throw new RpcError("INVALID_PARAMS", why);
      const ds = freeDisks(disks);
      const s = summary(name, "create", layout, ds);
      s.notes.push("Después creás uno o más volúmenes dentro del pool (con cifrado opcional).");
      return { ...newPlan({ kind: "create", name, layout, disks }), summary: s };
    },
    "storage.zfs.create": (p) => {
      const rec = take("create", p);
      const ds = freeDisks(rec.disks);
      const layout = rec.layout!;
      const usable = zfsUsable(layout, ds.map((d) => d.size)) * 0.97;
      const pool: ZfsPool = {
        id: `zfs:${rec.name}`, name: rec.name, guid: String(Date.now()), health: "ONLINE", state: "healthy", readonly: false, layout,
        size: usable, allocated: 0, free: usable, raw: ds.reduce((a, d) => a + d.size, 0), frag: 0,
        vdevs: layout === "stripe" ? ds.map((d) => ({ name: d.id, type: "disk" as const, state: "ONLINE", read: 0, write: 0, cksum: 0, size: d.size, disks: [leafOf(d)] })) : [{ name: `${layout}-0`, type: layout, state: "ONLINE", read: 0, write: 0, cksum: 0, size: ds.reduce((a, d) => a + d.size, 0), disks: ds.map((d) => leafOf(d)) }],
        logs: [], cache: [], scan: null, volumes: [], zvols: [], encrypted: [], disks: rec.disks, other: [],
      };
      return job("storage.zfs.create", `Creando el pool ZFS ${rec.name}`, `${zfsLayoutLabel(layout)} · ${ds.length} discos`, 0.08, () => {
        state().push(pool);
        ds.forEach((d) => Object.assign(d, { use: "member", pool: pool.id, role: "data" }));
        changed();
        ctx.notify("ok", "tango.storage", `El pool ${rec.name} está listo`, "Ya podés crear volúmenes en él.");
      });
    },

    "storage.zfs.addPlan": ({ pool, kind, disks }) => {
      const p = find(pool);
      const ds = freeDisks(disks);
      let layout: ZfsLayout = kind === "log" && ds.length === 2 ? "mirror" : "stripe";
      if (kind === "data") {
        if (p.layout === "mixed") throw new RpcError("NOT_SUPPORTED", "El pool mezcla formas de vdev");
        layout = p.layout;
        const why = zfsCheck(layout, ds.length);
        if (why) throw new RpcError("INVALID_PARAMS", `El vdev nuevo tiene que ser ${zfsLayoutLabel(layout)}: ${why}`);
      } else {
        if (ds.length < 1 || ds.length > 2) throw new RpcError("INVALID_PARAMS", "Elegí uno o dos SSD");
        if (ds.some((d) => d.kind === "hdd")) throw new RpcError("NOT_SSD", "Elegí SSD o NVMe");
      }
      const s = summary(p.name, kind, layout, ds);
      if (kind === "data") s.notes.unshift(`Se suma un vdev ${zfsLayoutLabel(layout)} al pool: la capacidad crece al instante. Un vdev no se puede quitar después.`);
      if (kind === "cache") s.notes = ["Caché de lectura (L2ARC): acelera las lecturas repetidas. Si el SSD falla, no se pierde nada."];
      if (kind === "log") s.notes = ["Registro de escritura (SLOG): acelera las escrituras sincrónicas (NFS, iSCSI, bases de datos)."];
      return { ...newPlan({ kind: "add", name: p.name, addKind: kind, layout, disks }), summary: s };
    },
    "storage.zfs.add": (p) => {
      const rec = take("add", p);
      const pool = find(rec.name);
      const ds = freeDisks(rec.disks);
      return job("storage.zfs.add", `Ampliando el pool ${pool.name}`, `${ds.length} discos`, 0.2, () => {
        if (rec.addKind === "data") {
          const v: ZfsVdev = { name: `${rec.layout}-${pool.vdevs.length}`, type: rec.layout!, state: "ONLINE", read: 0, write: 0, cksum: 0, size: ds.reduce((a, d) => a + d.size, 0), disks: ds.map((d) => leafOf(d)) };
          pool.vdevs.push(v);
          const add = zfsUsable(rec.layout!, ds.map((d) => d.size)) * 0.97;
          pool.size += add;
          pool.free += add;
          pool.raw += v.size;
          pool.disks.push(...ds.map((d) => d.id));
        } else if (rec.addKind === "cache") {
          pool.cache.push(...ds.map((d) => leafOf(d)));
        } else {
          pool.logs.push({ name: ds.length === 2 ? `mirror-${pool.vdevs.length + 1}` : ds[0].id, type: ds.length === 2 ? "mirror" : "disk", state: "ONLINE", read: 0, write: 0, cksum: 0, size: ds[0].size, disks: ds.map((d) => leafOf(d)) });
        }
        ds.forEach((d) => Object.assign(d, { use: "member", pool: pool.id, role: rec.addKind }));
        changed();
        ctx.notify("ok", "tango.storage", `Listo: ${pool.name}`, rec.addKind === "data" ? "El vdev nuevo ya suma espacio." : "El dispositivo ya está en uso.");
      });
    },
    "storage.zfs.auxRemove": ({ pool, kind, vdev }) => {
      const p = find(pool);
      return job("storage.zfs.aux", `Quitando ${vdev} de ${p.name}`, "", 0.3, () => {
        const ids = kind === "cache" ? p.cache.filter((l) => l.name === vdev).map((l) => l.id) : p.logs.filter((v) => v.name === vdev).flatMap((v) => v.disks.map((l) => l.id));
        if (kind === "cache") p.cache = p.cache.filter((l) => l.name !== vdev);
        else p.logs = p.logs.filter((v) => v.name !== vdev);
        ctx.disks().filter((d) => ids.includes(d.id)).forEach((d) => Object.assign(d, { use: "free", pool: undefined, role: undefined }));
        changed();
      });
    },

    "storage.zfs.replacePlan": ({ pool, old, replacement }) => {
      const p = find(pool);
      const leaf = p.vdevs.flatMap((v) => v.disks).find((l) => l.id === old || l.name === old || l.guid === old);
      if (!leaf) throw new RpcError("INVALID_PARAMS", "Ese disco no es parte del pool");
      const ds = freeDisks([replacement]);
      return {
        ...newPlan({ kind: "replace", name: p.name, old: leaf.id, disks: [replacement] }),
        summary: { ...summary(p.name, "replace", p.layout === "mixed" ? "stripe" : p.layout, ds), notes: [`Se reemplaza ${leaf.name} (${leaf.state.toLowerCase()}) y ZFS reconstruye solo los datos usados (resilver).`] },
      };
    },
    "storage.zfs.replace": (p) => {
      const rec = take("replace", p);
      const pool = find(rec.name);
      const [nd] = freeDisks(rec.disks);
      pool.scan = { function: "resilver", state: "scanning", progress: 0, errors: 0, start: new Date().toISOString() };
      const j = ctx.startJob("storage.zfs.replace", `Reemplazando un disco de ${pool.name}`, "Resilver: ZFS copia solo los datos usados", 0, 0.05, undefined, () => {
        for (const v of pool.vdevs) v.disks = v.disks.map((l) => (l.id === rec.old ? leafOf(nd) : l));
        pool.vdevs.forEach((v) => (v.state = "ONLINE"));
        Object.assign(pool, { health: "ONLINE", state: "healthy", scan: { function: "resilver", state: "finished", progress: 1, errors: 0, end: new Date().toISOString() } });
        pool.disks = pool.disks.map((id) => (id === rec.old ? nd.id : id));
        ctx.disks().forEach((d) => {
          if (d.id === rec.old) Object.assign(d, { use: "free", pool: undefined, role: undefined });
          if (d.id === nd.id) Object.assign(d, { use: "member", pool: pool.id, role: "data" });
        });
        changed();
        ctx.notify("ok", "tango.storage", `${pool.name} recuperó la redundancia`, "El resilver terminó.");
      });
      j.onTick = (x) => {
        if (pool.scan) pool.scan.progress = x;
        ctx.emit("storage.changed", { what: "zfs" });
      };
      return { job: j.id };
    },

    "storage.zfs.scrub": ({ pool, stop }) => {
      const p = find(pool);
      if (stop) {
        p.scan = p.scan ? { ...p.scan, state: "canceled" } : null;
        changed();
        return { job: "" };
      }
      if (p.scan?.state === "scanning") throw new RpcError("VOLUME_BUSY", "El pool ya está verificándose o reconstruyéndose");
      p.scan = { function: "scrub", state: "scanning", progress: 0, errors: 0, start: new Date().toISOString() };
      const j = ctx.startJob("storage.zfs.scrub", `Verificando el pool ${p.name}`, "Scrub de ZFS: lee todo y compara checksums", 0, 0.04, undefined, () => {
        p.scan = { function: "scrub", state: "finished", progress: 1, errors: 0, start: p.scan?.start, end: new Date().toISOString() };
        changed();
        ctx.notify("ok", "tango.storage", `Verificación de ${p.name} terminada`, "Sin errores: todos los bloques coinciden con sus checksums.");
      });
      j.onTick = (x) => {
        if (p.scan) p.scan.progress = x;
        ctx.emit("storage.changed", { what: "zfs" });
      };
      changed();
      return { job: j.id };
    },
    "storage.zfs.scrubSchedule.get": ({ pool }) => schedules.get(pool) ?? { enabled: true, days: 30, last: find(pool).scan?.end },
    "storage.zfs.scrubSchedule.set": ({ pool, enabled: on, days }) => {
      if (days < 1 || days > 365) throw new RpcError("INVALID_PARAMS", "Cada 1 a 365 días");
      const s = { enabled: on, days, last: find(pool).scan?.end };
      schedules.set(pool, s);
      return s;
    },

    "storage.zfs.import": ({ pool, readonly }) => {
      need();
      const im = importable.find((x) => x.name === pool || x.guid === pool);
      if (!im) throw new RpcError("POOL_NOT_FOUND", "No se encontró ese pool en los discos");
      return job("storage.zfs.import", `Importando el pool ${im.name}`, readonly ? `solo lectura, en /import/${im.name}` : "lectura y escritura", 0.15, () => {
        importable = importable.filter((x) => x !== im);
        state().push({
          id: `zfs:${im.name}`, name: im.name, guid: im.guid, health: "ONLINE", state: readonly ? "readonly" : "healthy", readonly, layout: "mirror",
          size: 3.6 * TiB, allocated: 1.2 * TiB, free: 2.4 * TiB, raw: 8 * TiB, frag: 7,
          vdevs: [{ name: "mirror-0", type: "mirror", state: "ONLINE", read: 0, write: 0, cksum: 0, size: 8 * TiB, disks: [] }],
          logs: [], cache: [], scan: null, volumes: [], zvols: [], encrypted: [], disks: [],
          other: [{ name: `${im.name}/fotos`, mountpoint: `/import/${im.name}/fotos`, mounted: true, used: 0.9 * TiB }, { name: `${im.name}/viejo`, mountpoint: `/import/${im.name}/viejo`, mounted: true, used: 0.3 * TiB }],
        });
        changed();
        ctx.notify("ok", "tango.storage", `Se importó el pool ${im.name}`, readonly ? `Está en solo lectura en /import/${im.name}.` : "Sus volúmenes ya están montados.");
      });
    },
    "storage.zfs.export": ({ pool }) => {
      const p = find(pool);
      if (p.volumes.length) throw new RpcError("VOLUME_BUSY", "En la demo, exportá un pool sin volúmenes (los volúmenes tienen carpetas en uso)");
      return job("storage.zfs.export", `Exportando el pool ${p.name}`, "Queda listo para llevarlo a otro equipo", 0.25, () => {
        pools = state().filter((x) => x !== p);
        importable.push({ name: p.name, guid: p.guid, state: "ONLINE", disks: p.disks });
        ctx.disks().filter((d) => d.pool === p.id).forEach((d) => Object.assign(d, { use: "free", pool: undefined, role: undefined }));
        changed();
      });
    },
    "storage.zfs.destroyPlan": ({ pool }) => {
      const p = find(pool);
      if (p.zvols.length) throw new RpcError("VOLUME_BUSY", `Borrá primero los volúmenes de bloque de ${p.name}`);
      const vols = ctx.vols().filter((v) => v.pool === p.id);
      const ds = ctx.disks().filter((d) => d.pool === p.id);
      const plan: DeletePlan = {
        ...newPlan({ kind: "destroy", name: p.name, disks: ds.map((d) => d.id) }),
        summary: { name: p.name, shares: [], snapshots: 0, used: p.allocated, disks: ds.map((d) => ({ id: d.id, model: d.model, serial: d.serial })) },
      } as DeletePlan;
      if (vols.length) plan.summary.shares = vols.map((v) => `(volumen ${v.name})`);
      return plan;
    },
    "storage.zfs.destroy": (p) => {
      const rec = take("destroy", p);
      const pool = find(rec.name);
      return job("storage.zfs.destroy", `Destruyendo el pool ${pool.name}`, `${rec.disks.length} discos quedan libres`, 0.2, () => {
        pools = state().filter((x) => x !== pool);
        ctx.setVols(ctx.vols().filter((v) => v.pool !== pool.id));
        ctx.disks().filter((d) => d.pool === pool.id).forEach((d) => Object.assign(d, { use: "free", pool: undefined, role: undefined }));
        changed("shares");
        ctx.notify("ok", "tango.storage", `Se destruyó el pool ${pool.name}`, "Sus discos quedaron libres.");
      });
    },

    "storage.zfs.volumes.create": ({ pool, name, quota, encryption }) => {
      const p = find(pool);
      if (p.readonly) throw new RpcError("NOT_SUPPORTED", "El pool está importado en solo lectura");
      if (!/^[a-z][a-z0-9]{0,31}$/.test(name)) throw new RpcError("INVALID_PARAMS", "El nombre debe empezar con una letra y tener solo minúsculas y números");
      if (ctx.vols().some((v) => v.name === name)) throw new RpcError("VOLUME_EXISTS", `Ya existe ${name}`);
      if (encryption && encryption.passphrase.length < 8) throw new RpcError("INVALID_PARAMS", "La frase tiene que tener entre 8 y 512 caracteres");
      const id = `zvol_${Math.random().toString(36).slice(2, 7)}`;
      return job("storage.zfs.volume.create", `Creando el volumen ${name}`, `Dataset ZFS en ${p.name}${encryption ? " · cifrado (AES-256-GCM)" : ""}`, 0.2, () => {
        const v: Volume = { id, name, mount: `/${name}`, kind: "zfs", pool: p.id, profile: p.layout, fs: "zfs", size: quota || p.free, used: 0, state: "healthy", disks: p.disks, lastScrub: p.scan?.end ?? null, growth30d: [], encrypted: !!encryption };
        ctx.setVols([...ctx.vols(), v]);
        p.volumes.push(id);
        if (encryption) p.encrypted.push(id);
        changed();
        ctx.notify("ok", "tango.storage", `El volumen ${name} está listo`, "Ya podés crear carpetas compartidas en él.");
      });
    },
    "storage.zfs.volumes.set": ({ volume, quota }) => {
      const v = ctx.vols().find((x) => x.id === volume && x.kind === "zfs");
      if (!v) throw new RpcError("VOLUME_NOT_FOUND", "No existe el volumen ZFS");
      if (quota && quota < v.used) throw new RpcError("INVALID_PARAMS", `La cuota no puede ser menor que lo que ya ocupa (${fmtBytes(v.used)})`);
      const p = find(v.pool!);
      ctx.setVols(ctx.vols().map((x) => (x.id === volume ? { ...x, size: quota || p.free + x.used } : x)));
      changed();
      return {};
    },
    "storage.zfs.volumes.deletePlan": ({ volume }) => {
      const v = ctx.vols().find((x) => x.id === volume && x.kind === "zfs");
      if (!v) throw new RpcError("VOLUME_NOT_FOUND", "No existe el volumen ZFS");
      return { ...newPlan({ kind: "voldelete", name: v.name, volume, disks: [] }), summary: { name: v.name, shares: [], snapshots: 0, used: v.used, disks: [] } } as DeletePlan;
    },
    "storage.zfs.volumes.delete": (p) => {
      const rec = take("voldelete", p);
      return job("storage.zfs.volume.delete", `Eliminando el volumen ${rec.name}`, "Borra el dataset, sus carpetas e instantáneas", 0.3, () => {
        const v = ctx.vols().find((x) => x.id === rec.volume);
        ctx.setVols(ctx.vols().filter((x) => x.id !== rec.volume));
        if (v?.pool) {
          const pool = find(v.pool);
          pool.volumes = pool.volumes.filter((x) => x !== rec.volume);
        }
        changed("shares");
      });
    },
    "storage.zfs.zvols.create": ({ pool, name, size, sparse }) => {
      const p = find(pool);
      if (!/^[a-z][a-z0-9_-]{0,31}$/.test(name)) throw new RpcError("INVALID_PARAMS", "Nombre inválido");
      if (size < 64 * 1024 ** 2) throw new RpcError("INVALID_PARAMS", "Al menos 64 MiB");
      if (p.zvols.some((z) => z.name === name)) throw new RpcError("ALREADY_EXISTS", `Ya existe ${name}`);
      if (!sparse && size > p.free) throw new RpcError("NO_SPACE", `No hay ${fmtBytes(size)} libres en ${p.name}`);
      p.zvols.push({ name, size, used: 0, device: `/dev/zvol/${p.name}/zvol/${name}` });
      if (!sparse) p.free -= size;
      changed();
      return {};
    },
    "storage.zfs.zvols.delete": ({ pool, name }) => {
      const p = find(pool);
      p.zvols = p.zvols.filter((z) => z.name !== name);
      changed();
      return {};
    },
  };
}
