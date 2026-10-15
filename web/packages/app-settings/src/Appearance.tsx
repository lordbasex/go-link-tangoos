import { Check } from "lucide-react";
import { prefs } from "@tango/shell";
import type { Prefs } from "@tango/shell";
import { Group, Row, Toggle } from "@tango/ui";
import { accents, wallpapers, wallpaperURL } from "@tango/theme-milonga";

const modes: { id: Prefs["mode"]; label: string }[] = [
  { id: "light", label: "Claro" },
  { id: "dark", label: "Oscuro" },
  { id: "auto", label: "Automático" },
];

function ModePreview({ mode }: { mode: Prefs["mode"] }) {
  return (
    <div className="tset-mode-prev" data-mode={mode}>
      <div className="tset-mode-half tset-light">
        <i className="tset-mp-bar" />
        <i className="tset-mp-win" />
      </div>
      <div className="tset-mode-half tset-dark">
        <i className="tset-mp-bar" />
        <i className="tset-mp-win" />
      </div>
    </div>
  );
}

export function Appearance() {
  const p = prefs.use();
  const set = (patch: Partial<Prefs>) => prefs.set((s) => ({ ...s, ...patch }));

  return (
    <>
      <Group title="Apariencia">
        <div className="tset-modes" role="radiogroup" aria-label="Apariencia">
          {modes.map((m) => (
            <button key={m.id} type="button" role="radio" aria-checked={p.mode === m.id} className="tset-mode" onClick={() => set({ mode: m.id })}>
              <ModePreview mode={m.id} />
              <span>{m.label}</span>
            </button>
          ))}
        </div>
        <Row label="Color de acento" hint="Botones, selección y controles activos">
          <div className="tset-accents" role="radiogroup" aria-label="Color de acento">
            {accents.map((a) => {
              const on = p.accent.toLowerCase() === a.hex.toLowerCase();
              return (
                <button key={a.id} type="button" role="radio" aria-checked={on} aria-label={a.name} title={a.name} className="tset-accent" style={{ background: a.hex }} onClick={() => set({ accent: a.hex })}>
                  {on && <span />}
                </button>
              );
            })}
          </div>
        </Row>
      </Group>

      <Group title="Fondo de pantalla">
        <div className="tset-walls">
          {wallpapers.map((w) => {
            const on = p.wallpaper === w.id;
            return (
              <button key={w.id} type="button" className="tset-wall" aria-pressed={on} onClick={() => set({ wallpaper: w.id })}>
                <span className="tset-wall-img" style={{ backgroundImage: wallpaperURL(w.id) }}>
                  {on && (
                    <span className="tset-wall-check">
                      <Check size={12} strokeWidth={3} />
                    </span>
                  )}
                </span>
                <span className="tset-wall-name">{w.name}</span>
              </button>
            );
          })}
        </div>
      </Group>

      <Group title="Dock y escritorio">
        <Row label="Tamaño del Dock">
          <div className="tset-range">
            <small>Chico</small>
            <input type="range" min={40} max={72} step={2} value={p.dockSize} onChange={(e) => set({ dockSize: Number(e.target.value) })} aria-label="Tamaño del Dock" style={{ ["--pct" as string]: `${((p.dockSize - 40) / 32) * 100}%` }} />
            <small>Grande</small>
          </div>
        </Row>
        <Row label="Ampliación" hint="Los íconos crecen al pasar el mouse">
          <Toggle checked={p.magnify} onChange={(v) => set({ magnify: v })} label="Ampliación del Dock" />
        </Row>
        <Row label="Widgets en el escritorio" hint="CPU, memoria, red, volúmenes y tareas en vivo">
          <Toggle checked={p.widgets} onChange={(v) => set({ widgets: v })} label="Widgets" />
        </Row>
        <Row label="No molestar" hint="Silencia los avisos que no son críticos">
          <Toggle checked={p.dnd} onChange={(v) => set({ dnd: v })} label="No molestar" />
        </Row>
      </Group>
      <p className="tset-foot">Estos ajustes se guardan en este navegador. En la versión final se guardan en tu usuario del NAS.</p>
    </>
  );
}
