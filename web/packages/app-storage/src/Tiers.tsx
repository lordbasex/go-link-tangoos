import { fmtBytes, t } from "@tango/client";
import type { PoolTier, TierLevel } from "@tango/client";

export const levelName: Record<TierLevel, string> = {
  basic: t("Básico"),
  raid1: "RAID 1",
  raid5: "RAID 5",
  raid6: "RAID 6",
  raid10: "RAID 10",
  unused: t("Reservado"),
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
  const total = tiers.reduce((a, tier) => a + tier.size, 0);
  const sorted = [...disks].sort((a, b) => a.size - b.size);
  const usable = tiers.reduce((a, tier) => a + tier.usable, 0);
  return (
    <figure className="ts-tiers" aria-label={t("Tramos: {list}", { list: tiers.map((tier) => t("{level} de {n} discos", { level: levelName[tier.level], n: tier.members.length })).join(", ") })}>
      <div className="ts-tiers-cols" style={{ height }}>
        {sorted.map((d) => (
          <div key={d.id} className="ts-tiers-col">
            <div className="ts-tiers-stack">
              {tiers.map((tier) =>
                tier.members.includes(d.id) ? (
                  <div
                    key={tier.index}
                    className="ts-tier-block"
                    data-level={tier.level}
                    style={{ height: `${(tier.size / total) * 100}%` }}
                    title={t("Tramo {index} · {level} · {size} de este disco", { index: tier.index, level: levelName[tier.level], size: fmtBytes(tier.size) })}
                  />
                ) : (
                  <div key={tier.index} className="ts-tier-gap" style={{ height: `${(tier.size / total) * 100}%` }} />
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
        {tiers.map((tier) => (
          <span key={tier.index}>
            <i className="ts-tier-block" data-level={tier.level} />
            {tier.level === "unused"
              ? t("Tramo {index}: {level} · {size} esperan otro disco", { index: tier.index, level: levelName[tier.level], size: fmtBytes(tier.size * tier.members.length) })
              : t("Tramo {index}: {level} de {n} discos · {size} útiles", { index: tier.index, level: levelName[tier.level], n: tier.members.length, size: fmtBytes(tier.usable) })}
          </span>
        ))}
        <span className="ts-tiers-total">
          {t("Total útil")} <b className="tg-num">{fmtBytes(usable)}</b>
        </span>
      </figcaption>
    </figure>
  );
}
