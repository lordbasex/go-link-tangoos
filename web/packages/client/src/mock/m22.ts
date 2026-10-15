// Mantenimiento de volúmenes simulado (protocol/README.md §11). Datos DEMO.
import { RpcError } from "../protocol";
import type { Disk, Job, Method, Notice, Params, Result, Scenario, Share, Snapshot, Topic, Topics, Volume } from "../protocol";
import type { MaintenancePlan, SnapshotSchedule, StorageAlert } from "../protocol-m22";
import { defaultSchedule } from "../protocol-m22";
import { profileInfo, profileLabel } from "../raid";
import { fmtBytes } from "../format";

export interface MockM22Ctx {
  scenario: () => Scenario;
  disks: () => Disk[];
  vols: () => Volume[];
  shares: () => Share[];
  snaps: () => Snapshot[];
  emit: <T extends Topic>(topic: T, d: Topics[T]) => void;
  startJob: (type: string, title: string, detail: string, progress: number, step: number, rate?: string, onDone?: () => void) => Job & { onTick?: (p: number) => void };
  notify: (level: Notice["level"], app: string, title: string, body: string) => void;
}

interface PlanRec {
  kind: "replace" | "expand";
  volume: string;
  failed: string | null;
  disks: string[];
  token: string;
  expires: number;
  newUsable?: number;
}

const REBUILD_SPEED = 152e6; // bytes/s simulados
const RESHAPE_SPEED = 61e6;

function newPlan(plans: Map<string, PlanRec>, rec: Omit<PlanRec, "token" | "expires">) {
  const plan_id = `p_${Math.random().toString(36).slice(2, 10)}`;
  const token = Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2);
  const expires = Date.now() + 5 * 60_000;
  plans.set(plan_id, { ...rec, token, expires });
  return { plan_id, confirm_token: token, expires: new Date(expires).toISOString() };
}

function takePlan(plans: Map<string, PlanRec>, kind: PlanRec["kind"], p: { plan_id: string; confirm_token: string; typed_name: string }, vols: Volume[]) {
  const rec = plans.get(p.plan_id);
  if (!rec || rec.kind !== kind) throw new RpcError("PLAN_EXPIRED", "El plan no existe o ya se usó");
  if (Date.now() > rec.expires) {
    plans.delete(p.plan_id);
    throw new RpcError("PLAN_EXPIRED", "El plan venció");
  }
  const v = vols.find((x) => x.id === rec.volume);
  if (!v) throw new RpcError("VOLUME_NOT_FOUND", "El volumen ya no existe");
  if (p.confirm_token !== rec.token || p.typed_name !== v.name) throw new RpcError("CONFIRMATION_MISMATCH", "La confirmación no coincide con el plan");
  plans.delete(p.plan_id);
  return { rec, v };
}

const content = (d: Disk) => (d.bay === 3 ? "Una partición ext4 (sin montar)" : "Vacío: sin tabla de particiones");

export function m22Handlers(ctx: MockM22Ctx): { [M in Method]?: (p: Params<M>) => Result<M> } {
  const plans = new Map<string, PlanRec>();
  const schedules = new Map<string, SnapshotSchedule>();
  const resolved: StorageAlert[] = [
    {
      id: "a_old_scrub", level: "info", source: "btrfs", resolved: true, at: new Date(Date.now() - 12 * 86400_000).toISOString(),
      title: "Verificación mensual terminada", body: "volume1: sin errores, todos los datos coinciden con sus checksums.",
    },
  ];

  const members = (v: Volume) => ctx.disks().filter((d) => v.disks.includes(d.id) || d.volume === v.id);
  const memberSize = (v: Volume) => Math.min(...members(v).filter((d) => d.use !== "failed").map((d) => d.size));
  const changed = () => {
    ctx.emit("storage.changed", { what: "volumes" });
    ctx.emit("storage.changed", { what: "disks" });
    ctx.emit("alerts.changed", {});
  };

  const getSchedule = (share: string): SnapshotSchedule => {
    let s = schedules.get(share);
    if (!s) {
      const demo = ctx.scenario() !== "empty" && ctx.scenario() !== "lab";
      s = demo ? { ...defaultSchedule, enabled: true } : structuredClone(defaultSchedule);
      schedules.set(share, s);
    }
    return s;
  };

  return {
    "storage.disks.replacePlan": ({ volume, failed, replacement }) => {
      const v = ctx.vols().find((x) => x.id === volume);
      if (!v) throw new RpcError("VOLUME_NOT_FOUND", "El volumen ya no existe");
      if (v.profile === "basic") throw new RpcError("INVALID_PARAMS", "Un volumen Básico no tiene redundancia: no se puede reconstruir");
      if (v.task) throw new RpcError("VOLUME_BUSY", "El volumen ya está reconstruyendo o reorganizándose");
      const d = ctx.disks().find((x) => x.id === replacement);
      if (!d) throw new RpcError("DEVICE_CHANGED", "El disco nuevo ya no está conectado");
      if (d.use !== "free" || d.foreign) throw new RpcError("DEVICE_NOT_ELIGIBLE", "El disco elegido no está libre");
      const need = memberSize(v);
      if (d.size < need) throw new RpcError("DEVICE_TOO_SMALL", `El disco tiene que ser de ${fmtBytes(need)} o más`);
      const bad = failed ? members(v).find((x) => x.id === failed) : members(v).find((x) => x.use === "failed");
      const p = newPlan(plans, { kind: "replace", volume, failed: bad?.id ?? null, disks: [d.id] });
      const plan: MaintenancePlan = {
        ...p,
        summary: {
          name: v.name, profile: v.profile, fs: "btrfs", usable: v.size,
          disks: [{ id: d.id, model: d.model, serial: d.serial, size: d.size, content: content(d) }],
          rebuild: { from: bad?.id ?? null, to: d.id, estimatedSec: Math.round(need / REBUILD_SPEED) },
          notes: d.size > need ? [`El disco nuevo es más grande: se usan ${fmtBytes(need)} y sobran ${fmtBytes(d.size - need)}.`] : [],
        },
      };
      return plan;
    },

    "storage.disks.replace": (p) => {
      const { rec, v } = takePlan(plans, "replace", p, ctx.vols());
      const d = ctx.disks().find((x) => x.id === rec.disks[0]);
      if (!d || d.use !== "free") throw new RpcError("DEVICE_CHANGED", "El disco nuevo cambió desde que armaste el plan");
      const old = rec.failed ? ctx.disks().find((x) => x.id === rec.failed) : undefined;
      if (old) {
        old.volume = undefined;
        v.disks = v.disks.filter((id) => id !== old.id);
      }
      d.use = "member";
      d.volume = v.id;
      d.rebuilding = true;
      v.disks = [...v.disks, d.id];
      v.task = "rebuild";
      v.taskSpeed = REBUILD_SPEED;
      v.progress = 0;
      if (v.state === "healthy") v.state = "degraded";
      const total = memberSize(v);
      changed();
      const j = ctx.startJob("storage.disk.replace", `Reconstruyendo ${v.name}`, `${d.model} · bahía ${d.bay ?? "—"}`, 0, 0.01, `${Math.round(REBUILD_SPEED / 1e6)} MB/s`, () => {
        d.rebuilding = undefined;
        v.task = undefined;
        v.taskSpeed = undefined;
        v.taskEtaSec = undefined;
        v.progress = undefined;
        v.state = "healthy";
        changed();
        ctx.notify("ok", "tango.storage", `${v.name} recuperó la redundancia`, `La reconstrucción terminó: ${profileLabel(v.profile)} vuelve a soportar la falla de un disco.`);
      });
      j.onTick = (pr) => {
        v.progress = pr;
        v.taskEtaSec = Math.round(((1 - pr) * total) / REBUILD_SPEED);
        ctx.emit("storage.changed", { what: "volumes" });
      };
      return { job: j.id };
    },

    "storage.volumes.expandPlan": ({ volume, add }) => {
      const v = ctx.vols().find((x) => x.id === volume);
      if (!v) throw new RpcError("VOLUME_NOT_FOUND", "El volumen ya no existe");
      if (v.profile === "basic" || v.profile === "raid1") throw new RpcError("INVALID_PARAMS", `Un volumen ${profileLabel(v.profile)} no se puede ampliar sumando discos`);
      if (v.state !== "healthy" || v.task) throw new RpcError("VOLUME_BUSY", "Esperá a que el volumen esté en orden y sin tareas");
      if (!add.length) throw new RpcError("INVALID_PARAMS", "Elegí al menos un disco");
      const chosen = ctx.disks().filter((d) => add.includes(d.id));
      if (chosen.length !== add.length) throw new RpcError("DEVICE_CHANGED", "Un disco ya no está conectado");
      if (chosen.some((d) => d.use !== "free" || d.foreign)) throw new RpcError("DEVICE_NOT_ELIGIBLE", "Uno de los discos no está libre");
      const need = memberSize(v);
      const small = chosen.find((d) => d.size < need);
      if (small) throw new RpcError("DEVICE_TOO_SMALL", `${small.model} es más chico que los discos del volumen (${fmtBytes(need)})`);
      const n = members(v).length + chosen.length;
      const why = profileInfo(v.profile).check(n);
      if (why) throw new RpcError("INVALID_PARAMS", why);
      const newUsable = profileInfo(v.profile).usable(Array(n).fill(need)) * 0.98;
      const p = newPlan(plans, { kind: "expand", volume, failed: null, disks: [...add], newUsable });
      const notes = [`La reorganización mueve datos entre todos los discos: con ${n} discos de ${fmtBytes(need)} tarda alrededor de ${Math.max(1, Math.round((need * n) / RESHAPE_SPEED / 60))} min.`];
      if (chosen.some((d) => d.size > need)) notes.push(`Los discos más grandes se usan hasta ${fmtBytes(need)}.`);
      const plan: MaintenancePlan = {
        ...p,
        summary: {
          name: v.name, profile: v.profile, fs: "btrfs", usable: v.size, newUsable, notes,
          disks: chosen.map((d) => ({ id: d.id, model: d.model, serial: d.serial, size: d.size, content: content(d) })),
        },
      };
      return plan;
    },

    "storage.volumes.expand": (p) => {
      const { rec, v } = takePlan(plans, "expand", p, ctx.vols());
      const chosen = ctx.disks().filter((d) => rec.disks.includes(d.id));
      if (chosen.length !== rec.disks.length || chosen.some((d) => d.use !== "free")) throw new RpcError("DEVICE_CHANGED", "Un disco cambió desde que armaste el plan");
      chosen.forEach((d) => Object.assign(d, { use: "member", volume: v.id }));
      v.disks = [...v.disks, ...chosen.map((d) => d.id)];
      v.task = "reshape";
      v.taskSpeed = RESHAPE_SPEED;
      v.progress = 0;
      const total = memberSize(v) * v.disks.length;
      changed();
      const j = ctx.startJob("storage.volume.expand", `Ampliando ${v.name}`, `+${chosen.length} disco${chosen.length > 1 ? "s" : ""} · reorganización de ${profileLabel(v.profile)}`, 0, 0.006, `${Math.round(RESHAPE_SPEED / 1e6)} MB/s`, () => {
        v.size = rec.newUsable ?? v.size;
        v.task = undefined;
        v.taskSpeed = undefined;
        v.taskEtaSec = undefined;
        v.progress = undefined;
        changed();
        ctx.notify("ok", "tango.storage", `${v.name} se amplió`, `Ahora tiene ${fmtBytes(v.size)} de capacidad.`);
      });
      j.onTick = (pr) => {
        v.progress = pr;
        v.taskEtaSec = Math.round(((1 - pr) * total) / RESHAPE_SPEED);
        ctx.emit("storage.changed", { what: "volumes" });
      };
      return { job: j.id };
    },

    "storage.disks.locate": ({ disk }) => {
      const d = ctx.disks().find((x) => x.id === disk);
      if (!d) throw new RpcError("DEVICE_NOT_FOUND", "El disco ya no está conectado");
      // Los discos virtuales del laboratorio y el NVMe interno no tienen LED de identificación.
      if (ctx.scenario() === "lab" || d.bay == null) throw new RpcError("NOT_SUPPORTED", "Sin LED de identificación");
      return {};
    },

    "snapshots.schedule.get": ({ share }) => {
      if (!ctx.shares().some((s) => s.id === share)) throw new RpcError("SHARE_NOT_FOUND", "La carpeta ya no existe");
      return structuredClone(getSchedule(share));
    },

    "snapshots.schedule.set": ({ share, schedule }) => {
      if (!ctx.shares().some((s) => s.id === share)) throw new RpcError("SHARE_NOT_FOUND", "La carpeta ya no existe");
      if (![1, 2, 4, 6, 12].includes(schedule.hourly.every)) throw new RpcError("INVALID_PARAMS", "Frecuencia horaria inválida");
      if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(schedule.daily.at) || !/^([01]\d|2[0-3]):[0-5]\d$/.test(schedule.weekly.at)) throw new RpcError("INVALID_PARAMS", "Hora inválida");
      if ([schedule.hourly.keep, schedule.daily.keep, schedule.weekly.keep].some((k) => k < 0 || k > 500 || !Number.isInteger(k))) throw new RpcError("INVALID_PARAMS", "Cantidad a conservar inválida");
      schedules.set(share, structuredClone(schedule));
      ctx.emit("snapshots.changed", { share });
      return {};
    },

    "snapshots.delete": ({ id }) => {
      const list = ctx.snaps();
      const i = list.findIndex((s) => s.id === id);
      if (i < 0) throw new RpcError("SNAPSHOT_NOT_FOUND", "La instantánea ya no existe");
      if (list[i].locked) throw new RpcError("SNAPSHOT_LOCKED", "La instantánea está protegida");
      const [s] = list.splice(i, 1);
      ctx.emit("snapshots.changed", { share: s.share });
      return {};
    },

    "alerts.list": () => {
      const out: StorageAlert[] = [];
      const now = new Date().toISOString();
      for (const v of ctx.vols()) {
        const bad = members(v).find((d) => d.use === "failed");
        if (v.task === "rebuild")
          out.push({ id: `a_rebuild_${v.id}`, level: "warn", source: "md", resolved: false, at: now, title: `${v.name} se está reconstruyendo`, body: `Hasta que termine, el volumen no tiene redundancia. Progreso: ${Math.round((v.progress ?? 0) * 100)} %.` });
        else if (v.state === "degraded")
          out.push({
            id: `a_deg_${v.id}`, level: "crit", source: "md", resolved: false, at: new Date(Date.now() - 9 * 60_000).toISOString(),
            title: `${v.name} sin redundancia`, body: bad ? `El disco de la bahía ${bad.bay ?? "—"} (${bad.model}) dejó de responder. Reemplazalo cuanto antes.` : "Falta un disco del array.",
          });
        if (v.size && v.used / v.size > 0.9)
          out.push({ id: `a_space_${v.id}`, level: "warn", source: "space", resolved: false, at: now, title: `${v.name} casi lleno`, body: `Quedan ${fmtBytes(v.size - v.used)} libres (menos del 10 %).` });
      }
      for (const d of ctx.disks())
        if (d.smart === "warn")
          out.push({ id: `a_smart_${d.id}`, level: "warn", source: "smart", resolved: false, at: now, title: `SMART: el disco de la bahía ${d.bay ?? "—"} muestra desgaste`, body: `${d.model} · ${d.reallocated} sectores reasignados. Todavía funciona; conviene tener un repuesto.` });
      return [...out, ...resolved];
    },
  };
}
