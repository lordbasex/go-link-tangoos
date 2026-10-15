import type { Disk, Volume } from "@tango/client";
import { fmtBytes, profileLabel } from "@tango/client";
import { ledOf, volumeColor } from "./util";

const ledText = { ok: "Funcionando", busy: "Con actividad", warn: "Con advertencias", crit: "Falló", off: "Bahía vacía", rebuild: "Reconstruyendo" } as const;

type Layout = "tower4" | "tower8" | "rack";

/** Cantidad de bahías del gabinete: la mayor bahía ocupada, redondeada al gabinete típico. */
function bayCount(disks: Disk[]): number {
  const top = Math.max(0, ...disks.map((d) => d.bay ?? 0));
  if (top <= 4) return 4;
  if (top <= 8) return 8;
  if (top <= 12) return 12;
  return 16;
}

/**
 * Frente del NAS dibujado. Se adapta a la cantidad de bahías:
 * 1–4 torre de escritorio, 5–8 torre ancha (2 filas), 9–16 rack 2U con bandejas horizontales.
 * Cada volumen tiñe sus bandejas con su color; la leyenda abajo los nombra.
 */
export function Chassis({ disks, vols, selected, onSelect }: { disks: Disk[]; vols: Volume[]; selected?: string | null; onSelect: (d: Disk) => void }) {
  const n = bayCount(disks);
  const layout: Layout = n <= 4 ? "tower4" : n <= 8 ? "tower8" : "rack";
  const bays = Array.from({ length: n }, (_, i) => disks.find((d) => d.bay === i + 1));
  const anyCrit = bays.some((d) => d && ledOf(d, vols) === "crit");
  const used = vols.filter((v) => bays.some((d) => d?.volume === v.id));
  const free = bays.filter((d) => d && d.use === "free").length;

  return (
    <div className="ts-chassis-wrap">
      <div className="ts-chassis" data-layout={layout} role="group" aria-label={`Frente del NAS, ${n} bahías`}>
        <div className="ts-chassis-panel">
          <div className="ts-power" aria-hidden="true">
            <span />
          </div>
          <div className="ts-status-led" data-state={anyCrit ? "crit" : "ok"} title={anyCrit ? "Estado: atención" : "Estado: normal"} />
          <div className="ts-brand">tango</div>
          <div className="ts-usb" aria-hidden="true" />
        </div>
        <div className="ts-bays" style={{ ["--cols" as string]: layout === "tower8" ? 4 : layout === "rack" ? 4 : n }}>
          {bays.map((d, i) => {
            const led = ledOf(d, vols);
            const color = d?.volume ? volumeColor(d.volume, vols) : undefined;
            return (
              <button
                key={i}
                type="button"
                className="ts-bay"
                data-empty={!d || undefined}
                data-led={led}
                data-free={(d && d.use === "free") || undefined}
                data-selected={(d && selected === d.id) || undefined}
                disabled={!d}
                onClick={() => d && onSelect(d)}
                title={d ? `Bahía ${i + 1} · ${d.model} · ${d.serial} · ${fmtBytes(d.size, 0)}` : `Bahía ${i + 1} vacía`}
                aria-label={d ? `Bahía ${i + 1}: ${d.model}, ${fmtBytes(d.size, 0)}, ${ledText[led]}` : `Bahía ${i + 1} vacía`}
                style={color ? { ["--vol" as string]: color } : undefined}
              >
                <span className="ts-tray">
                  {color && <span className="ts-vol-stripe" aria-hidden="true" />}
                  <span className="ts-grille" aria-hidden="true">
                    {Array.from({ length: layout === "rack" ? 6 : 9 }, (_, k) => (
                      <i key={k} />
                    ))}
                  </span>
                  <span className="ts-handle" aria-hidden="true" />
                  {layout === "rack" && d && <span className="ts-tray-size">{fmtBytes(d.size, 0)}</span>}
                  <span className="ts-bay-foot">
                    <span className="ts-led" aria-hidden="true" />
                    <span className="ts-bay-num">{i + 1}</span>
                  </span>
                </span>
                {layout !== "rack" && (
                  <span className="ts-bay-label">
                    {d ? (
                      <>
                        <strong>{fmtBytes(d.size, 0)}</strong>
                        <small>{d.use === "free" ? "Libre" : ledText[led]}</small>
                      </>
                    ) : (
                      <small>Vacía</small>
                    )}
                  </span>
                )}
              </button>
            );
          })}
        </div>
      </div>
      {(used.length > 0 || free > 0) && (
        <div className="ts-legend" aria-label="Volúmenes por bahía">
          {used.map((v) => (
            <span key={v.id} className="ts-legend-item">
              <i style={{ background: volumeColor(v.id, vols) }} />
              {v.name} · {profileLabel(v.profile)}
            </span>
          ))}
          {free > 0 && (
            <span className="ts-legend-item">
              <i data-free />
              {free === 1 ? "1 disco libre" : `${free} discos libres`}
            </span>
          )}
        </div>
      )}
    </div>
  );
}
