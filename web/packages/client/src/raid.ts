// Reglas de perfiles RAID compartidas por el mock y la UI (protocol/README.md §8).
import type { Bytes, VolumeProfile } from "./protocol";
import { t } from "./i18n";

/** "Elegí al menos N discos" (N ≥ 2). */
const atLeast = (n: number) => t("Elegí al menos {n} discos", { n });

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

// Los textos son getters: se traducen al leerlos (t() con el catálogo ya registrado).
export const PROFILES: ProfileInfo[] = [
  {
    id: "basic",
    get label() { return t("Básico"); },
    get description() { return t("Un solo disco, sin redundancia."); },
    get tolerance() { return t("No soporta la falla del disco: si falla, se pierden los datos."); },
    min: 1,
    check: (n) => (n === 1 ? null : t("Elegí exactamente 1 disco")),
    usable: (s) => s[0] ?? 0,
  },
  {
    id: "raid1",
    label: "RAID 1",
    get description() { return t("Dos discos espejados: cada dato se guarda dos veces."); },
    get tolerance() { return t("Soporta la falla de 1 disco."); },
    min: 2,
    check: (n) => (n === 2 ? null : t("Elegí exactamente 2 discos")),
    usable: (s) => min(s),
  },
  {
    id: "raid5",
    label: "RAID 5",
    get description() { return t("Tres discos o más, con paridad distribuida."); },
    get tolerance() { return t("Soporta la falla de 1 disco."); },
    min: 3,
    check: (n) => (n >= 3 ? null : atLeast(3)),
    usable: (s) => (s.length - 1) * min(s),
  },
  {
    id: "raid6",
    label: "RAID 6",
    get description() { return t("Cuatro discos o más, con doble paridad."); },
    get tolerance() { return t("Soporta la falla de 2 discos a la vez."); },
    min: 4,
    check: (n) => (n >= 4 ? null : atLeast(4)),
    usable: (s) => (s.length - 2) * min(s),
  },
  {
    id: "raid10",
    label: "RAID 10",
    get description() { return t("Espejos de a pares, unidos en franjas. Rápido y redundante."); },
    get tolerance() { return t("Soporta la falla de 1 disco por par espejado."); },
    min: 4,
    check: (n) => (n < 4 ? atLeast(4) : n % 2 ? t("La cantidad de discos tiene que ser par") : null),
    usable: (s) => (s.length / 2) * min(s),
  },
  {
    id: "hybrid1",
    get label() { return t("Híbrido-1"); },
    get description() { return t("Discos de distinto tamaño, aprovechados casi enteros."); },
    get tolerance() { return t("Soporta la falla de 1 disco."); },
    min: 2,
    check: (n) => (n >= 2 ? null : atLeast(2)),
    usable: (s) => (s.length - 1) * min(s),
    poolOnly: true,
  },
  {
    id: "hybrid2",
    get label() { return t("Híbrido-2"); },
    get description() { return t("Discos de distinto tamaño, con doble protección."); },
    get tolerance() { return t("Soporta la falla de 2 discos a la vez."); },
    min: 4,
    check: (n) => (n >= 4 ? null : atLeast(4)),
    usable: (s) => (s.length - 2) * min(s),
    poolOnly: true,
  },
  // Formas de vdev de ZFS (§18): solo en pools ZFS, nunca en el asistente clásico.
  { id: "mirror", get label() { return t("Espejo ZFS"); }, get description() { return t("Cada dato en todos los discos."); }, get tolerance() { return t("Soporta la falla de todos los discos menos uno."); }, min: 2, check: (n) => (n >= 2 ? null : atLeast(2)), usable: (s) => min(s), poolOnly: true },
  { id: "raidz1", label: "RAIDZ1", get description() { return t("Paridad simple."); }, get tolerance() { return t("Soporta la falla de 1 disco."); }, min: 3, check: (n) => (n >= 3 ? null : atLeast(3)), usable: (s) => (s.length - 1) * min(s), poolOnly: true },
  { id: "raidz2", label: "RAIDZ2", get description() { return t("Doble paridad."); }, get tolerance() { return t("Soporta la falla de 2 discos a la vez."); }, min: 4, check: (n) => (n >= 4 ? null : atLeast(4)), usable: (s) => (s.length - 2) * min(s), poolOnly: true },
  { id: "raidz3", label: "RAIDZ3", get description() { return t("Triple paridad."); }, get tolerance() { return t("Soporta la falla de 3 discos a la vez."); }, min: 5, check: (n) => (n >= 5 ? null : atLeast(5)), usable: (s) => (s.length - 3) * min(s), poolOnly: true },
  { id: "stripe", get label() { return t("ZFS sin redundancia"); }, get description() { return t("Suma la capacidad de los discos."); }, get tolerance() { return t("No soporta la falla de ningún disco."); }, min: 1, check: (n) => (n >= 1 ? null : t("Elegí al menos 1 disco")), usable: (s) => s.reduce((a, b) => a + b, 0), poolOnly: true },
  { id: "mixed", get label() { return t("ZFS mixto"); }, get description() { return t("Vdevs de formas distintas."); }, get tolerance() { return t("Según cada vdev."); }, min: 1, check: () => null, usable: (s) => s.reduce((a, b) => a + b, 0), poolOnly: true },
];

export const profileInfo = (p: VolumeProfile) => PROFILES.find((x) => x.id === p) ?? PROFILES[0];
export const profileLabel = (p: VolumeProfile) => profileInfo(p).label;

/** Espacio que queda sin usar cuando se mezclan tamaños (se iguala al disco más chico). */
export function unusedBytes(sizes: Bytes[]): Bytes {
  const m = min(sizes);
  return sizes.reduce((a, s) => a + (s - m), 0);
}
