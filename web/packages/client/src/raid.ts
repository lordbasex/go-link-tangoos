// Reglas de perfiles RAID compartidas por el mock y la UI (protocol/README.md §8).
import type { Bytes, VolumeProfile } from "./protocol";

export interface ProfileInfo {
  id: VolumeProfile;
  label: string;
  /** Explicación corta para la tarjeta del asistente. */
  description: string;
  /** Tolerancia a fallas, en castellano. */
  tolerance: string;
  min: number;
  /** null si la cantidad de discos es válida; si no, el motivo. */
  check: (n: number) => string | null;
  /** Capacidad útil antes de Btrfs. */
  usable: (sizes: Bytes[]) => Bytes;
  /** Solo para grupos de almacenamiento (RAID híbrido, §16): no hay volumen clásico con este perfil. */
  poolOnly?: true;
}

const min = (s: Bytes[]) => (s.length ? Math.min(...s) : 0);

export const PROFILES: ProfileInfo[] = [
  {
    id: "basic",
    label: "Básico",
    description: "Un solo disco, sin redundancia.",
    tolerance: "No soporta la falla del disco: si falla, se pierden los datos.",
    min: 1,
    check: (n) => (n === 1 ? null : "Elegí exactamente 1 disco"),
    usable: (s) => s[0] ?? 0,
  },
  {
    id: "raid1",
    label: "RAID 1",
    description: "Dos discos espejados: cada dato se guarda dos veces.",
    tolerance: "Soporta la falla de 1 disco.",
    min: 2,
    check: (n) => (n === 2 ? null : "Elegí exactamente 2 discos"),
    usable: (s) => min(s),
  },
  {
    id: "raid5",
    label: "RAID 5",
    description: "Tres discos o más, con paridad distribuida.",
    tolerance: "Soporta la falla de 1 disco.",
    min: 3,
    check: (n) => (n >= 3 ? null : "Elegí al menos 3 discos"),
    usable: (s) => (s.length - 1) * min(s),
  },
  {
    id: "raid6",
    label: "RAID 6",
    description: "Cuatro discos o más, con doble paridad.",
    tolerance: "Soporta la falla de 2 discos a la vez.",
    min: 4,
    check: (n) => (n >= 4 ? null : "Elegí al menos 4 discos"),
    usable: (s) => (s.length - 2) * min(s),
  },
  {
    id: "raid10",
    label: "RAID 10",
    description: "Espejos de a pares, unidos en franjas. Rápido y redundante.",
    tolerance: "Soporta la falla de 1 disco por par espejado.",
    min: 4,
    check: (n) => (n < 4 ? "Elegí al menos 4 discos" : n % 2 ? "La cantidad de discos tiene que ser par" : null),
    usable: (s) => (s.length / 2) * min(s),
  },
  {
    id: "hybrid1",
    label: "Híbrido-1",
    description: "Discos de distinto tamaño, aprovechados casi enteros.",
    tolerance: "Soporta la falla de 1 disco.",
    min: 2,
    check: (n) => (n >= 2 ? null : "Elegí al menos 2 discos"),
    usable: (s) => (s.length - 1) * min(s),
    poolOnly: true,
  },
  {
    id: "hybrid2",
    label: "Híbrido-2",
    description: "Discos de distinto tamaño, con doble protección.",
    tolerance: "Soporta la falla de 2 discos a la vez.",
    min: 4,
    check: (n) => (n >= 4 ? null : "Elegí al menos 4 discos"),
    usable: (s) => (s.length - 2) * min(s),
    poolOnly: true,
  },
  // Formas de vdev de ZFS (§18): solo en pools ZFS, nunca en el asistente clásico.
  { id: "mirror", label: "Espejo ZFS", description: "Cada dato en todos los discos.", tolerance: "Soporta la falla de todos los discos menos uno.", min: 2, check: (n) => (n >= 2 ? null : "Elegí al menos 2 discos"), usable: (s) => min(s), poolOnly: true },
  { id: "raidz1", label: "RAIDZ1", description: "Paridad simple.", tolerance: "Soporta la falla de 1 disco.", min: 3, check: (n) => (n >= 3 ? null : "Elegí al menos 3 discos"), usable: (s) => (s.length - 1) * min(s), poolOnly: true },
  { id: "raidz2", label: "RAIDZ2", description: "Doble paridad.", tolerance: "Soporta la falla de 2 discos a la vez.", min: 4, check: (n) => (n >= 4 ? null : "Elegí al menos 4 discos"), usable: (s) => (s.length - 2) * min(s), poolOnly: true },
  { id: "raidz3", label: "RAIDZ3", description: "Triple paridad.", tolerance: "Soporta la falla de 3 discos a la vez.", min: 5, check: (n) => (n >= 5 ? null : "Elegí al menos 5 discos"), usable: (s) => (s.length - 3) * min(s), poolOnly: true },
  { id: "stripe", label: "ZFS sin redundancia", description: "Suma la capacidad de los discos.", tolerance: "No soporta la falla de ningún disco.", min: 1, check: (n) => (n >= 1 ? null : "Elegí al menos 1 disco"), usable: (s) => s.reduce((a, b) => a + b, 0), poolOnly: true },
  { id: "mixed", label: "ZFS mixto", description: "Vdevs de formas distintas.", tolerance: "Según cada vdev.", min: 1, check: () => null, usable: (s) => s.reduce((a, b) => a + b, 0), poolOnly: true },
];

export const profileInfo = (p: VolumeProfile) => PROFILES.find((x) => x.id === p) ?? PROFILES[0];
export const profileLabel = (p: VolumeProfile) => profileInfo(p).label;

/** Espacio que queda sin usar cuando se mezclan tamaños (se iguala al disco más chico). */
export function unusedBytes(sizes: Bytes[]): Bytes {
  const m = min(sizes);
  return sizes.reduce((a, s) => a + (s - m), 0);
}
