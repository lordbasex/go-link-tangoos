import { fmtAgo, profileInfo, unusedBytes, useIsReal, useTransport } from "@tango/client";
import type { Method } from "@tango/client";
import type { Disk, DiskHealth, Pool, Volume, VolumeProfile } from "@tango/client";
import type { Tone } from "@tango/ui";

export type Section = "overview" | "pools" | "zfs" | "repl" | "volumes" | "disks" | "snapshots" | "iscsi";

export type Led = "ok" | "warn" | "crit" | "off" | "busy" | "rebuild";

/**
 * ¿Se puede usar este método? Con el servidor real (modo híbrido) solo si lo implementa: si no,
 * el mock respondería con datos de otro NAS. En la demo pura siempre.
 */
export function useAvailable(m: Method): boolean {
  const real = useIsReal(m);
  return real || useTransport().kind === "mock";
}

/** Motivo por el que un disco no puede usarse para un volumen nuevo (null = elegible). */
export function ineligibleReason(d: Disk, vols: Volume[], pools: Pool[] = []): string | null {
  if (d.use === "system") return "Disco del sistema";
  if (d.use === "failed") return "Falló: no responde";
  if (d.foreign) return "Viene de otro NAS";
  if (d.pool) return `${d.role === "cache" ? "Caché SSD del grupo" : "Miembro del grupo"} ${pools.find((p) => p.id === d.pool)?.name ?? ""}`.trim();
  if (d.use === "member") return `Miembro de ${vols.find((v) => v.id === d.volume)?.name ?? "un volumen"}`;
  return null;
}

/** Nombre corto de para qué se usa un disco (tabla de discos, chasis). */
export function diskUse(d: Disk, vols: Volume[], pools: Pool[] = []): string {
  if (d.use === "system") return "Disco del sistema";
  if (d.use === "failed") return "Falló";
  if (d.foreign) return "Otro NAS (ajeno)";
  if (d.pool) {
    const p = pools.find((x) => x.id === d.pool)?.name ?? "grupo";
    return d.role === "cache" ? `Caché SSD · ${p}` : p;
  }
  if (d.use === "member") return vols.find((v) => v.id === d.volume)?.name ?? "En uso";
  return "Sin usar";
}

export function ledOf(d: Disk | undefined, vols: Volume[], pools: Pool[] = []): Led {
  if (!d) return "off";
  if (d.use === "failed" || d.smart === "crit") return "crit";
  if (d.smart === "warn") return "warn";
  const p = d.pool ? pools.find((x) => x.id === d.pool) : undefined;
  if (p) {
    if (d.rebuilding && p.task === "rebuild") return "rebuild";
    return p.task || p.state === "building" || p.state === "scrubbing" ? "busy" : "ok";
  }
  const v = vols.find((x) => x.id === d.volume);
  if (d.rebuilding && v?.task === "rebuild") return "rebuild";
  if (v && (v.task || v.state === "scrubbing" || v.state === "building")) return "busy";
  return "ok";
}

/** Nombre de un sistema de archivos. */
export const fsName: Record<Volume["fs"], string> = { btrfs: "Btrfs", xfs: "XFS", ext4: "ext4", zfs: "ZFS" };

const VOL_COLORS = ["#2F7CF6", "#22C55E", "#F97316", "#8B5CF6", "#EC4899", "#14B8A6", "#EAB308"];

/** Color estable por volumen (orden de la lista), para teñir bandejas y leyendas. */
export function volumeColor(volId: string, vols: Volume[]): string {
  const i = vols.findIndex((v) => v.id === volId);
  return VOL_COLORS[(i < 0 ? 0 : i) % VOL_COLORS.length];
}

export const profileName: Record<VolumeProfile, string> = {
  basic: "Básico",
  raid1: "RAID 1",
  raid5: "RAID 5",
  raid6: "RAID 6",
  raid10: "RAID 10",
  hybrid1: "Híbrido-1",
  hybrid2: "Híbrido-2",
  stripe: "ZFS sin redundancia",
  mirror: "Espejo ZFS",
  raidz1: "RAIDZ1",
  raidz2: "RAIDZ2",
  raidz3: "RAIDZ3",
  mixed: "ZFS mixto",
};

export const profileHelp: Record<VolumeProfile, string> = {
  basic: `${profileInfo("basic").description} ${profileInfo("basic").tolerance}`,
  raid1: `${profileInfo("raid1").description} ${profileInfo("raid1").tolerance}`,
  raid5: `${profileInfo("raid5").description} ${profileInfo("raid5").tolerance}`,
  raid6: `${profileInfo("raid6").description} ${profileInfo("raid6").tolerance}`,
  raid10: `${profileInfo("raid10").description} ${profileInfo("raid10").tolerance}`,
  hybrid1: `${profileInfo("hybrid1").description} ${profileInfo("hybrid1").tolerance}`,
  hybrid2: `${profileInfo("hybrid2").description} ${profileInfo("hybrid2").tolerance}`,
  stripe: `${profileInfo("stripe").description} ${profileInfo("stripe").tolerance}`,
  mirror: `${profileInfo("mirror").description} ${profileInfo("mirror").tolerance}`,
  raidz1: `${profileInfo("raidz1").description} ${profileInfo("raidz1").tolerance}`,
  raidz2: `${profileInfo("raidz2").description} ${profileInfo("raidz2").tolerance}`,
  raidz3: `${profileInfo("raidz3").description} ${profileInfo("raidz3").tolerance}`,
  mixed: `${profileInfo("mixed").description} ${profileInfo("mixed").tolerance}`,
};

/** Capacidad útil / protección / sin usar para un perfil y un conjunto de discos (reglas de @tango/client). */
export function capacity(profile: VolumeProfile, sizes: number[]) {
  const total = sizes.reduce((a, b) => a + b, 0);
  if (!sizes.length) return { usable: 0, protection: 0, unused: 0, total };
  const unused = unusedBytes(sizes);
  const usable = profileInfo(profile).usable(sizes);
  return { usable, protection: Math.max(0, total - unused - usable), unused, total };
}

/** Contenido detectado en el disco (demo, determinístico). */
export function detectedContent(d: Disk): string {
  if (d.foreign) return `Volumen ${d.foreign.profile} de otro NAS (${d.foreign.label})`;
  if (d.bay === 3) return "Una partición ext4 sin montar (3,9 TB)";
  return "Vacío: sin tabla de particiones";
}

export const smartLabel = { ok: "Saludable", warn: "Con advertencias", crit: "Falló", unknown: "Sin SMART" } as const;
/** SMART "unknown" (discos virtuales, controladoras sin passthrough) se muestra como info, no como problema. */
export const smartTone = (h: DiskHealth): Tone => (h === "unknown" ? "info" : h);

/** Última verificación: el servidor manda null o "" cuando el volumen nunca se verificó. */
export const scrubAgo = (v: Pick<Volume, "lastScrub">) => (v.lastScrub ? fmtAgo(v.lastScrub) : "Nunca");
