import { fmtBytes } from "@tango/client";
import type { PoolTier, TierLevel } from "@tango/client";

export const levelName: Record<TierLevel, string> = {
  basic: "Básico",
  raid1: "RAID 1",
  raid5: "RAID 5",
  raid6: "RAID 6",
  raid10: "RAID 10",
  unused: "Reservado",
};

export interface TierDisk {
  id: string;
  label: string;
  size: number;
}

/**
 * Cómo se reparten los discos en tramos: una columna por disco (alto proporcional a su
 * tamaño) con un bloque por tramo que alcanza. Los tramos sin redundancia todavía se ven
 * rayados: es espacio reservado para cuando llegue otro disco.
 */
export function TierDiagram({ tiers, disks, height = 132 }: { tiers: PoolTier[]; disks: TierDisk[]; height?: number }) {
  if (!tiers.length || !disks.length) return null;
  const total = tiers.reduce((a, t) => a + t.size, 0);
  const sorted = [...disks].sort((a, b) => a.size - b.size);
  const usable = tiers.reduce((a, t) => a + t.usable, 0);
  return (
    <figure className="ts-tiers" aria-label={`Tramos: ${tiers.map((t) => `${levelName[t.level]} de ${t.members.length} discos`).join(", ")}`}>
      <div className="ts-tiers-cols" style={{ height }}>
        {sorted.map((d) => (
          <div key={d.id} className="ts-tiers-col">
            <div className="ts-tiers-stack">
              {tiers.map((t) =>
                t.members.includes(d.id) ? (
                  <div
                    key={t.index}
                    className="ts-tier-block"
                    data-level={t.level}
                    style={{ height: `${(t.size / total) * 100}%` }}
                    title={`Tramo ${t.index} · ${levelName[t.level]} · ${fmtBytes(t.size)} de este disco`}
                  />
                ) : (
                  <div key={t.index} className="ts-tier-gap" style={{ height: `${(t.size / total) * 100}%` }} />
                ),
              )}
            </div>
            <small className="tg-num" title={d.label}>
              {fmtBytes(d.size)}
            </small>
          </div>
        ))}
      </div>
      <figcaption className="ts-tiers-legend">
        {tiers.map((t) => (
          <span key={t.index}>
            <i className="ts-tier-block" data-level={t.level} />
            Tramo {t.index}: {levelName[t.level]}
            {t.level === "unused" ? ` · ${fmtBytes(t.size * t.members.length)} esperan otro disco` : ` de ${t.members.length} discos · ${fmtBytes(t.usable)} útiles`}
          </span>
        ))}
        <span className="ts-tiers-total">
          Total útil <b className="tg-num">{fmtBytes(usable)}</b>
        </span>
      </figcaption>
    </figure>
  );
}
