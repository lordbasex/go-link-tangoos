// Grupos de almacenamiento simulados (protocol/README.md §16). Datos DEMO.
import { RpcError } from "../protocol";
import type { Disk, Job, Method, Notice, Params, Result, Scenario, Topic, Topics, Volume } from "../protocol";
import type { KeyDevice, Pool, PoolPlan, PoolProfile, PoolTier } from "../protocol-pools";
import { POOL_PROFILES, isHybrid, poolLayout, poolProfileLabel, tiersUnused, tiersUsable } from "../protocol-pools";
import { fmtBytes } from "../format";
import { t } from "../i18n";

export interface MockPoolsCtx {
  scenario: () => Scenario;
  disks: () => Disk[];
  vols: () => Volume[];
  setVols: (v: Volume[]) => void;
  emit: <T extends Topic>(topic: T, d: Topics[T]) => void;
  startJob: (type: string, title: string, detail: string, progress: number, step: number, rate?: string, onDone?: () => void) => Job & { onTick?: (p: number) => void };
  notify: (level: Notice["level"], app: string, title: string, body: string) => void;
}

interface PlanRec {
  kind: "create" | "expand" | "replace" | "delete" | "cache";
  name: string;
  profile?: PoolProfile;
  pool?: string;
  volume?: string;
  disks: string[];
  failed?: string | null;
  mode?: "read" | "readwrite";
  token: string;
  expires: number;
}

/** Frase que la demo acepta para todos los volúmenes cifrados (y cualquiera que no sea "error"). */
const demoKey: KeyDevice = { uuid: "5E2A-91C4", fs: "vfat", label: "LLAVE", size: 16e9, disk: "usb-SanDisk_Ultra_4C531001", usb: true };

export function poolsHandlers(ctx: MockPoolsCtx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  let pools: Pool[] | null = null;
  const plans = new Map<string, PlanRec>();
  const failures = new Map<string, number>();

  // Estado inicial perezoso (depende del escenario, que no existe al construir los handlers).
  const state = (): Pool[] => {
    if (pools) return pools;
    pools = [];
    if (ctx.scenario() === "pools" || ctx.scenario() === "locked") {
      const ds = ctx.disks().filter((d) => d.pool === "p1" && d.role === "data");
      const tiers = poolLayout("hybrid1", ds.map((d) => ({ id: d.id, size: d.size })));
      pools.push({ id: "p1", name: "pool1", profile: "hybrid1", state: "healthy", size: tiersUsable(tiers), allocated: 0, free: 0,
        disks: ds.map((d) => d.id), cacheDisks: ctx.disks().filter((d) => d.role === "cache").map((d) => d.id), tiers, volumes: [] });
    }
    return pools;
  };

  // Lo asignado y los volúmenes salen de la lista de volúmenes (los borra MockTransport).
  const refresh = (p: Pool): Pool => {
    const vols = ctx.vols().filter((v) => v.pool === p.id);
    const allocated = vols.reduce((a, v) => a + v.size, 0);
    return { ...p, allocated, free: Math.max(0, p.size - allocated), volumes: vols.map((v) => v.id) };
  };
  const find = (id: string) => {
    const p = state().find((x) => x.id === id || x.name === id);
    if (!p) throw new RpcError("POOL_NOT_FOUND", "No existe el grupo");
    return refresh(p);
  };
  const newPlan = (rec: Omit<PlanRec, "token" | "expires">) => {
    const plan_id = `p_${Math.random().toString(36).slice(2, 10)}`;
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
  const planDisks = (ds: Disk[]) => ds.map((d) => ({ id: d.id, model: d.model, serial: d.serial, size: d.size, content: t("Vacío: sin tabla de particiones") }));
  const changed = () => {
    ctx.emit("storage.changed", { what: "pools" });
    ctx.emit("storage.changed", { what: "volumes" });
    ctx.emit("storage.changed", { what: "disks" });
  };
  const vol = (id: string) => {
    const v = ctx.vols().find((x) => x.id === id);
    if (!v) throw new RpcError("VOLUME_NOT_FOUND", "No existe el volumen");
    return v;
  };

  return {
    "storage.pools.list": () => state().map(refresh),

    "storage.pools.plan": ({ name, profile, disks }) => {
      if (!/^[a-z][a-z0-9]{0,23}$/.test(name)) throw new RpcError("INVALID_PARAMS", "El nombre del grupo debe empezar con una letra y tener solo minúsculas y números");
      if (state().some((p) => p.name === name) || ctx.vols().some((v) => v.name === name)) throw new RpcError("POOL_EXISTS", t("Ya existe {name}", { name }));
      const why = POOL_PROFILES.find((x) => x.id === profile)?.check(disks.length);
      if (why) throw new RpcError("INVALID_PARAMS", why);
      const ds = freeDisks(disks);
      const tiers = poolLayout(profile, ds.map((d) => ({ id: d.id, size: d.size })));
      const unused = tiersUnused(tiers);
      const notes: string[] = [];
      if (isHybrid(profile) && unused > 0) notes.push(t("{size} quedan reservados hasta que sumes otro disco igual o más grande que el mayor: ahí se suman al grupo con redundancia.", { size: fmtBytes(unused) }));
      if (!isHybrid(profile) && profile !== "basic" && new Set(ds.map((d) => d.size)).size > 1) notes.push(t("Los discos tienen distinto tamaño: se usa {size} de cada uno. Con RAID híbrido se aprovecharían.", { size: fmtBytes(Math.min(...ds.map((d) => d.size))) }));
      notes.push(t("Después creás uno o más volúmenes dentro del grupo (Btrfs, con cifrado opcional)."));
      const plan: PoolPlan = { ...newPlan({ kind: "create", name, profile, disks }), summary: { name, profile, fs: "lvm", usable: tiersUsable(tiers), unused, tiers, disks: planDisks(ds), notes } };
      return plan;
    },

    "storage.pools.create": (p) => {
      const rec = take("create", p);
      const ds = freeDisks(rec.disks);
      const tiers = poolLayout(rec.profile!, ds.map((d) => ({ id: d.id, size: d.size })));
      const id = `p${state().length + 1}_${Math.random().toString(36).slice(2, 6)}`;
      const pool: Pool = { id, name: rec.name, profile: rec.profile!, state: "building", size: tiersUsable(tiers), allocated: 0, free: tiersUsable(tiers), disks: rec.disks, cacheDisks: [], tiers, volumes: [], task: "resync", progress: 0 };
      state().push(pool);
      ds.forEach((d) => Object.assign(d, { use: "member", pool: id, role: "data" }));
      changed();
      const j = ctx.startJob("storage.pool.create", t("Creando el grupo {name}", { name: rec.name }), `${poolProfileLabel(rec.profile!)} · ${t("{n} discos", { n: ds.length })}`, 0, 0.02, undefined, () => {
        Object.assign(pool, { state: "healthy", task: undefined, progress: undefined });
        changed();
        ctx.notify("ok", "tango.storage", t("El grupo {name} está listo", { name: rec.name }), t("Ya podés crear volúmenes en él."));
      });
      j.onTick = (x) => {
        pool.progress = x;
        ctx.emit("storage.changed", { what: "pools" });
      };
      return { job: j.id };
    },

    "storage.pools.expandPlan": ({ pool, add }) => {
      const p = find(pool);
      if (p.profile === "basic" || p.profile === "raid1") throw new RpcError("NOT_SUPPORTED", t("Un grupo {profile} no gana espacio sumando discos", { profile: poolProfileLabel(p.profile) }));
      if (p.state !== "healthy") throw new RpcError("VOLUME_BUSY", "El grupo tiene que estar sano para ampliarlo");
      const ds = freeDisks(add);
      const all = [...ctx.disks().filter((d) => p.disks.includes(d.id)), ...ds];
      if (isHybrid(p.profile) && ds.some((d) => d.size < Math.max(...all.filter((x) => !ds.includes(x)).map((x) => x.size)) * 0.99))
        throw new RpcError("DEVICE_TOO_SMALL", "El disco nuevo tiene que ser al menos tan grande como el más grande del grupo");
      const tiers = poolLayout(p.profile, all.map((d) => ({ id: d.id, size: d.size })));
      const usable = tiersUsable(tiers);
      const notes = [t("El grupo pasa de {from} a {to} útiles; los volúmenes no crecen solos: agrandalos después.", { from: fmtBytes(p.size), to: fmtBytes(usable) }), t("La reorganización mueve datos y puede tardar horas. Los volúmenes siguen disponibles.")];
      return { ...newPlan({ kind: "expand", name: p.name, pool: p.id, disks: add }), summary: { name: p.name, profile: p.profile, fs: "lvm", usable, unused: tiersUnused(tiers), tiers, disks: planDisks(ds), notes } };
    },

    "storage.pools.expand": (x) => {
      const rec = take("expand", x);
      const p = state().find((y) => y.id === rec.pool)!;
      const ds = freeDisks(rec.disks);
      ds.forEach((d) => Object.assign(d, { use: "member", pool: p.id, role: "data" }));
      Object.assign(p, { state: "building", task: "reshape", progress: 0 });
      changed();
      const j = ctx.startJob("storage.pool.expand", t("Ampliando el grupo {name}", { name: p.name }), t("{n} disco(s) nuevo(s)", { n: ds.length }), 0, 0.01, "61 MB/s", () => {
        p.disks = [...p.disks, ...rec.disks];
        const all = ctx.disks().filter((d) => p.disks.includes(d.id));
        p.tiers = poolLayout(p.profile, all.map((d) => ({ id: d.id, size: d.size })));
        Object.assign(p, { size: tiersUsable(p.tiers), state: "healthy", task: undefined, progress: undefined });
        changed();
        ctx.notify("ok", "tango.storage", t("El grupo {name} se amplió", { name: p.name }), t("Ahora tiene {size} libres para sus volúmenes.", { size: fmtBytes(refresh(p).free) }));
      });
      j.onTick = (v) => {
        p.progress = v;
        ctx.emit("storage.changed", { what: "pools" });
      };
      return { job: j.id };
    },

    "storage.pools.replacePlan": ({ pool, failed, replacement }) => {
      const p = find(pool);
      const [d] = freeDisks([replacement]);
      const from = failed ?? ctx.disks().find((x) => x.pool === p.id && x.use === "failed")?.id ?? null;
      if (!from && p.state !== "degraded") throw new RpcError("INVALID_PARAMS", "El grupo no está degradado: indicá qué disco reemplazar");
      return {
        ...newPlan({ kind: "replace", name: p.name, pool: p.id, disks: [replacement], failed: from }),
        summary: { name: p.name, profile: p.profile, fs: "lvm", usable: p.size, unused: 0, tiers: p.tiers, disks: planDisks([d]), rebuild: { from, to: d.id, estimatedSec: 5 * 3600 }, notes: [t("Mientras se reconstruye, los volúmenes siguen disponibles pero sin redundancia.")] },
      };
    },

    "storage.pools.replace": (x) => {
      const rec = take("replace", x);
      const p = state().find((y) => y.id === rec.pool)!;
      const [d] = freeDisks(rec.disks);
      if (rec.failed) {
        const old = ctx.disks().find((y) => y.id === rec.failed);
        if (old) Object.assign(old, { use: "free", pool: undefined, role: undefined, smart: old.smart });
        p.disks = p.disks.map((id) => (id === rec.failed ? d.id : id));
      }
      Object.assign(d, { use: "member", pool: p.id, role: "data", rebuilding: true });
      Object.assign(p, { state: "building", task: "rebuild", progress: 0 });
      changed();
      const j = ctx.startJob("storage.pool.replace", t("Reemplazando un disco del grupo {name}", { name: p.name }), t("Reconstrucción sobre {serial}", { serial: d.serial }), 0, 0.012, "152 MB/s", () => {
        d.rebuilding = false;
        Object.assign(p, { state: "healthy", task: undefined, progress: undefined });
        changed();
        ctx.notify("ok", "tango.storage", t("Disco reemplazado en el grupo {name}", { name: p.name }), t("La reconstrucción terminó: el grupo está sano."));
      });
      j.onTick = (v) => {
        p.progress = v;
        ctx.emit("storage.changed", { what: "pools" });
      };
      return { job: j.id };
    },

    "storage.pools.deletePlan": ({ pool }) => {
      const p = find(pool);
      if (p.volumes.length) throw new RpcError("POOL_NOT_EMPTY", t("El grupo {name} tiene {n} volumen(es): eliminalos primero", { name: p.name, n: p.volumes.length }));
      const ds = ctx.disks().filter((d) => d.pool === p.id);
      return {
        ...newPlan({ kind: "delete", name: p.name, pool: p.id, disks: ds.map((d) => d.id) }),
        summary: { name: p.name, shares: [], snapshots: 0, used: 0, disks: ds.map((d) => ({ id: d.id, model: d.model, serial: d.serial })) },
      };
    },

    "storage.pools.delete": (x) => {
      const rec = take("delete", x);
      const j = ctx.startJob("storage.pool.delete", t("Eliminando el grupo {name}", { name: rec.name }), t("{n} discos quedan libres", { n: rec.disks.length }), 0, 0.08, undefined, () => {
        pools = state().filter((p) => p.id !== rec.pool);
        ctx.disks().forEach((d) => {
          if (d.pool === rec.pool) Object.assign(d, { use: "free", pool: undefined, role: undefined });
        });
        changed();
        ctx.notify("ok", "tango.storage", t("El grupo {name} se eliminó", { name: rec.name }), t("Los {n} discos quedaron libres.", { n: rec.disks.length }));
      });
      return { job: j.id };
    },

    "storage.poolVolumes.create": ({ pool, name, size, fs, encryption }) => {
      const p = find(pool);
      if (!/^[a-z][a-z0-9]{0,31}$/.test(name)) throw new RpcError("INVALID_PARAMS", "El nombre debe empezar con una letra y tener solo minúsculas y números");
      if (ctx.vols().some((v) => v.name === name) || state().some((x) => x.name === name)) throw new RpcError("VOLUME_EXISTS", t("Ya existe {name}", { name }));
      const want = size || p.free;
      if (want > p.free) throw new RpcError("NO_SPACE", t("El grupo {name} tiene {size} libres", { name: p.name, size: fmtBytes(p.free) }));
      if (encryption && encryption.passphrase.length < 10) throw new RpcError("INVALID_PARAMS", "La frase tiene que tener entre 10 y 512 caracteres");
      const v: Volume = {
        id: `vol_${Math.random().toString(36).slice(2, 7)}`, name, mount: `/${name}`, kind: "pool", pool: p.id, profile: p.profile, fs, size: want, used: 0,
        state: "healthy", disks: p.disks, lastScrub: null, growth30d: [], encrypted: !!encryption, locked: false, keyDevice: encryption?.keyDevice ?? undefined,
      };
      const j = ctx.startJob("storage.volume.create", t("Creando {name}", { name }), `${fs} · ${t("{size} en el grupo {pool}", { size: fmtBytes(want), pool: p.name })}${encryption ? ` · ${t("cifrado")}` : ""}`, 0, 0.05, undefined, () => {
        ctx.setVols([...ctx.vols(), v]);
        changed();
        ctx.notify("ok", "tango.storage", t("{name} está listo", { name }), t("{size} montado en /{name}.", { size: fmtBytes(want), name }) + (encryption ? " " + t("Cifrado con LUKS2: guardá la frase en un lugar seguro.") : ""));
      });
      return { job: j.id };
    },

    "storage.poolVolumes.resize": ({ volume, size }) => {
      const v = vol(volume);
      const p = find(v.pool ?? "");
      if (v.locked) throw new RpcError("VOLUME_LOCKED", t("{name} está bloqueado: desbloquealo para agrandarlo", { name: v.name }));
      if (size <= v.size) throw new RpcError("NOT_SUPPORTED", "Solo se puede agrandar un volumen");
      if (size - v.size > p.free) throw new RpcError("NO_SPACE", t("El grupo {name} tiene {size} libres", { name: p.name, size: fmtBytes(p.free) }));
      const j = ctx.startJob("storage.volume.resize", t("Agrandando {name}", { name: v.name }), t("A {size}", { size: fmtBytes(size) }), 0, 0.1, undefined, () => {
        v.size = size;
        changed();
        ctx.notify("ok", "tango.storage", t("{name} se agrandó", { name: v.name }), t("Ahora tiene {size}.", { size: fmtBytes(size) }));
      });
      return { job: j.id };
    },

    "storage.volumes.unlock": ({ volume, passphrase, useKeyDevice }) => {
      const v = vol(volume);
      if (!v.encrypted) throw new RpcError("NOT_SUPPORTED", t("{name} no está cifrado", { name: v.name }));
      if ((failures.get(v.id) ?? 0) >= 5) throw new RpcError("RATE_LIMITED", "Demasiados intentos fallidos: esperá unos minutos");
      if (useKeyDevice && !v.keyDevice) throw new RpcError("KEY_DEVICE_MISSING", "Este volumen no tiene un dispositivo de clave");
      if (!useKeyDevice && (!passphrase || passphrase === "error" || passphrase.length < 10)) {
        failures.set(v.id, (failures.get(v.id) ?? 0) + 1);
        throw new RpcError("WRONG_PASSPHRASE", "La frase no abre este volumen");
      }
      Object.assign(v, { locked: false, state: "healthy", mount: `/${v.name}`, used: v.used || 0.42 * 1024 ** 4 });
      changed();
      ctx.emit("shares.changed", {});
      return {};
    },

    "storage.volumes.lock": ({ volume }) => {
      const v = vol(volume);
      if (!v.encrypted) throw new RpcError("NOT_SUPPORTED", t("{name} no está cifrado", { name: v.name }));
      Object.assign(v, { locked: true, state: "locked", mount: "" });
      changed();
      ctx.emit("shares.changed", {});
      return {};
    },

    "storage.volumes.passphrase": ({ volume, old, new: next }) => {
      const v = vol(volume);
      if (old === "error") throw new RpcError("WRONG_PASSPHRASE", "La frase actual no abre este volumen");
      if (next.length < 10) throw new RpcError("INVALID_PARAMS", "La frase tiene que tener entre 10 y 512 caracteres");
      void v;
      return {};
    },

    "storage.volumes.keyDevice": ({ volume, passphrase, device }) => {
      const v = vol(volume);
      if (passphrase === "error") throw new RpcError("WRONG_PASSPHRASE", "La frase no abre este volumen");
      v.keyDevice = device ?? undefined;
      changed();
      return {};
    },

    "storage.keyDevices.list": () => [demoKey],

    "storage.cache.plan": ({ volume, disks, mode }) => {
      const v = vol(volume);
      if (v.cache) throw new RpcError("CACHE_EXISTS", t("{name} ya tiene caché SSD", { name: v.name }));
      if (mode === "readwrite" && disks.length !== 2) throw new RpcError("INVALID_PARAMS", "La caché de lectura y escritura necesita 2 SSD (en RAID 1)");
      if (mode === "read" && (disks.length < 1 || disks.length > 2)) throw new RpcError("INVALID_PARAMS", "La caché de lectura usa 1 o 2 SSD");
      const ds = freeDisks(disks);
      if (ds.some((d) => d.kind === "hdd")) throw new RpcError("NOT_SSD", "Uno de los discos no es una SSD");
      const notes = [
        ...(ds.length === 2 ? [t("Las dos SSD van en RAID 1: si una falla, la caché sigue.")] : []),
        mode === "readwrite"
          ? t("Lectura y escritura: los datos nuevos quedan primero en las SSD y bajan a los discos después. Si fallan las dos SSD a la vez se pierden esas escrituras.")
          : t("Solo lectura: acelera lo que más se lee; todo lo que se escribe va también a los discos."),
      ];
      const tiers: PoolTier[] = [];
      return { ...newPlan({ kind: "cache", name: v.name, volume: v.id, disks, mode }), summary: { name: v.name, profile: (v.profile as PoolProfile) ?? "basic", fs: v.fs, usable: Math.min(...ds.map((d) => d.size)), unused: 0, tiers, disks: planDisks(ds), notes } };
    },

    "storage.cache.create": (x) => {
      const rec = take("cache", x);
      const v = vol(rec.volume!);
      const ds = freeDisks(rec.disks);
      const j = ctx.startJob("storage.cache.create", t("Caché SSD para {name}", { name: v.name }), rec.mode === "read" ? t("solo lectura") : t("lectura y escritura"), 0, 0.1, undefined, () => {
        ds.forEach((d) => Object.assign(d, { use: "member", pool: v.pool, role: "cache" }));
        v.cache = { mode: rec.mode!, disks: rec.disks, size: Math.min(...ds.map((d) => d.size)) * 0.98, used: 0, dirty: 0, readHits: 0, readMisses: 0, writeHits: 0, writeMisses: 0, hitRatio: null };
        changed();
        ctx.notify("ok", "tango.storage", t("{name} tiene caché SSD", { name: v.name }), t("Los aciertos se ven en el volumen y en el widget de almacenamiento."));
      });
      return { job: j.id };
    },

    "storage.cache.remove": ({ volume }) => {
      const v = vol(volume);
      if (!v.cache) throw new RpcError("NOT_FOUND", t("{name} no tiene caché SSD", { name: v.name }));
      const ids = v.cache.disks;
      const j = ctx.startJob("storage.cache.remove", t("Quitando la caché de {name}", { name: v.name }), t("Los bloques pendientes bajan a los discos"), 0, 0.1, undefined, () => {
        v.cache = undefined;
        ctx.disks().forEach((d) => {
          if (ids.includes(d.id)) Object.assign(d, { use: "free", pool: undefined, role: undefined });
        });
        changed();
        ctx.notify("ok", "tango.storage", t("Caché quitada de {name}", { name: v.name }), t("Las SSD quedaron libres."));
      });
      return { job: j.id };
    },
  };
}
