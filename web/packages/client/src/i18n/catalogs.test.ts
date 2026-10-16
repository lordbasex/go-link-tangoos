// Todos los catálogos juntos: la misma clave en español no puede tener dos traducciones
// (el mapa es uno solo). Si una palabra tiene dos sentidos, usar t(texto, vars, "contexto").
import { describe, expect, it } from "vitest";
import { enConflicts } from "../i18n";
import "./server.en";
import "../i18n.en";
import "../../../app-apps/src/i18n.en";
import "../../../app-cluster/src/i18n.en";
import "../../../app-files/src/i18n.en";
import "../../../app-log/src/i18n.en";
import "../../../app-monitor/src/i18n.en";
import "../../../app-settings/src/i18n.en";
import "../../../app-snapshots/src/i18n.en";
import "../../../app-storage/src/i18n.en";
import "../../../app-terminal/src/i18n.en";
import "../../../shell/src/i18n.en";
import "../../../theme-milonga/src/i18n.en";
import "../../../ui/src/i18n.en";
import "../../../widgets/src/i18n.en";
import "../../../../apps/desktop/src/i18n.en";
import "../../../../apps/connect/src/i18n.en";

describe("catálogos en inglés", () => {
  it("ninguna clave tiene dos traducciones distintas", () => {
    expect(enConflicts).toEqual([]);
  });
});
