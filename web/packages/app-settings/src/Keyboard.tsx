import { shortcutLabel } from "@tango/shell";
import { Group } from "@tango/ui";

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
const cmd = isMac ? "⌘" : "Ctrl+";

const ROWS: [string, string, string][] = [
  ["Buscar (Spotlight)", `${cmd}K`, `${cmd}Espacio`],
  ["Cerrar ventana", shortcutLabel("W"), `${cmd}W`],
  ["Nueva ventana de la app", shortcutLabel("N"), `${cmd}N`],
  ["Ciclar ventanas", shortcutLabel("`"), `${cmd}\``],
  ["Minimizar", shortcutLabel("M"), `${cmd}M`],
  ["Ajustar a la izquierda / derecha", shortcutLabel("← / →"), "Ctrl+Alt+← / →"],
  ["Ampliar", shortcutLabel("↑"), "Ctrl+Alt+↑"],
  ["Aplicaciones", shortcutLabel("Espacio"), "F4"],
  ["Bloquear pantalla", shortcutLabel("L"), `${cmd}L`],
  ["Vista rápida (en Archivos)", "Espacio", "Espacio"],
];

export function KeyboardSection() {
  return (
    <>
      <p className="tg-lead">
        En el navegador, combinaciones como {cmd}W o {cmd}T las usa el propio navegador, así que TangoOS usa {isMac ? "⌥" : "Alt"}. En la app de escritorio funcionan los atajos clásicos.
      </p>
      <Group>
        <table className="tset-keys">
          <thead>
            <tr>
              <th>Acción</th>
              <th>Navegador</th>
              <th>App de escritorio</th>
            </tr>
          </thead>
          <tbody>
            {ROWS.map(([a, web, nat]) => (
              <tr key={a}>
                <td>{a}</td>
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
