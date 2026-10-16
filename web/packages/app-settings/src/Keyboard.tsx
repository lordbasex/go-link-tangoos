import { t } from "@tango/client";
import { shortcutLabel } from "@tango/shell";
import { Group } from "@tango/ui";

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
const cmd = isMac ? "⌘" : "Ctrl+";

const ROWS: [string, string, string][] = [
  ["Buscar (Spotlight)", `${cmd}K`, `${cmd}${t("Espacio")}`],
  ["Cerrar ventana", shortcutLabel("W"), `${cmd}W`],
  ["Nueva ventana de la app", shortcutLabel("N"), `${cmd}N`],
  ["Ciclar ventanas", shortcutLabel("`"), `${cmd}\``],
  ["Minimizar", shortcutLabel("M"), `${cmd}M`],
  ["Ajustar a la izquierda / derecha", shortcutLabel("← / →"), "Ctrl+Alt+← / →"],
  ["Ampliar", shortcutLabel("↑"), "Ctrl+Alt+↑"],
  ["Aplicaciones", shortcutLabel(t("Espacio")), "F4"],
  ["Bloquear pantalla", shortcutLabel("L"), `${cmd}L`],
  ["Vista rápida (en Archivos)", t("Espacio"), t("Espacio")],
];

export function KeyboardSection() {
  return (
    <>
      <p className="tg-lead">
        {t("En el navegador, combinaciones como {w} o {tab} las usa el propio navegador, así que TangoOS usa {alt}. En la app de escritorio funcionan los atajos clásicos.", { w: `${cmd}W`, tab: `${cmd}T`, alt: isMac ? "⌥" : "Alt" })}
      </p>
      <Group>
        <table className="tset-keys">
          <thead>
            <tr>
              <th>{t("Acción")}</th>
              <th>{t("Navegador")}</th>
              <th>{t("App de escritorio")}</th>
            </tr>
          </thead>
          <tbody>
            {ROWS.map(([a, web, nat]) => (
              <tr key={a}>
                <td>{t(a)}</td>
                <td>
                  <kbd>{web}</kbd>
                </td>
                <td>
                  <kbd>{nat}</kbd>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Group>
    </>
  );
}
