import { Check } from "lucide-react";
import { getLang, LANGS, setLang, t } from "@tango/client";
import type { Lang } from "@tango/client";
import { prefs } from "@tango/shell";
import type { Prefs } from "@tango/shell";
import { Group, Row, Segmented, Toggle } from "@tango/ui";
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
      <Group title={t("Apariencia")}>
        <div className="tset-modes" role="radiogroup" aria-label={t("Apariencia")}>
          {modes.map((m) => (
            <button key={m.id} type="button" role="radio" aria-checked={p.mode === m.id} className="tset-mode" onClick={() => set({ mode: m.id })}>
              <ModePreview mode={m.id} />
              <span>{t(m.label)}</span>
            </button>
          ))}
        </div>
        <Row label={t("Color de acento")} hint={t("Botones, selección y controles activos")}>
          <div className="tset-accents" role="radiogroup" aria-label={t("Color de acento")}>
            {accents.map((a) => {
              const on = p.accent.toLowerCase() === a.hex.toLowerCase();
              return (
                <button key={a.id} type="button" role="radio" aria-checked={on} aria-label={t(a.name)} title={t(a.name)} className="tset-accent" style={{ background: a.hex }} onClick={() => set({ accent: a.hex })}>
                  {on && <span />}
                </button>
              );
            })}
          </div>
        </Row>
        <Row label={t("Idioma")} hint={t("La interfaz se vuelve a cargar en el idioma elegido.")}>
          <Segmented<Lang> label={t("Idioma")} value={getLang()} onChange={(l) => l !== getLang() && setLang(l)} options={LANGS.map((l) => ({ value: l.id, label: l.name }))} />
        </Row>
      </Group>

      <Group title={t("Fondo de pantalla")}>
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
                <span className="tset-wall-name">{t(w.name)}</span>
              </button>
            );
          })}
        </div>
      </Group>

      <Group title={t("Dock y escritorio")}>
        <Row label={t("Tamaño del Dock")}>
          <div className="tset-range">
            <small>{t("Chico")}</small>
            <input type="range" min={40} max={72} step={2} value={p.dockSize} onChange={(e) => set({ dockSize: Number(e.target.value) })} aria-label={t("Tamaño del Dock")} style={{ ["--pct" as string]: `${((p.dockSize - 40) / 32) * 100}%` }} />
            <small>{t("Grande")}</small>
          </div>
        </Row>
        <Row label={t("Ampliación")} hint={t("Los íconos crecen al pasar el mouse")}>
          <Toggle checked={p.magnify} onChange={(v) => set({ magnify: v })} label={t("Ampliación del Dock")} />
        </Row>
        <Row label={t("Widgets en el escritorio")} hint={t("CPU, memoria, red, volúmenes y tareas en vivo")}>
          <Toggle checked={p.widgets} onChange={(v) => set({ widgets: v })} label={t("Widgets")} />
        </Row>
        <Row label={t("No molestar")} hint={t("Silencia los avisos que no son críticos")}>
          <Toggle checked={p.dnd} onChange={(v) => set({ dnd: v })} label={t("No molestar")} />
        </Row>
      </Group>
      <p className="tset-foot">{t("Estos ajustes se guardan en este navegador. En la versión final se guardan en tu usuario del NAS.")}</p>
    </>
  );
}
