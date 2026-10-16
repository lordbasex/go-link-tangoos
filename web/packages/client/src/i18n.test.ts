import { afterEach, describe, expect, it } from "vitest";
import { RpcError } from "./protocol";
import { registerEn, registerEnFormats, t, tn, tr, useLangForTests } from "./i18n";

registerEn({ "Crear volumen": "Create volume", "Montado en /{name}.": "Mounted at /{name}.", "un archivo": "one file", "{n} archivos": "{n} files", Carpeta: "Folder" });
registerEnFormats({
  "no existe el volumen %s": "volume %s does not exist",
  "%s: no se pudo conectar a %s (%s)": "%s: could not connect to %s (%s)",
  "el volumen %[2]s del pool %[1]s": "volume %[2]s in pool %[1]s",
  "i/o timeout": "i/o timeout",
  "Carpeta %s": "Folder %s",
  "Quedan %d %% libres": "Only %d %% free",
});

describe("i18n", () => {
  afterEach(() => useLangForTests("es"));
  it("español es la fuente", () => {
    useLangForTests("es");
    expect(t("Crear volumen")).toBe("Crear volumen");
    expect(t("Montado en /{name}.", { name: "volume1" })).toBe("Montado en /volume1.");
    expect(tr("no existe el volumen x")).toBe("no existe el volumen x");
  });
  it("inglés con catálogo y variables", () => {
    useLangForTests("en");
    expect(t("Crear volumen")).toBe("Create volume");
    expect(t("Montado en /{name}.", { name: "volume1" })).toBe("Mounted at /volume1.");
    expect(t("Sin traducir")).toBe("Sin traducir");
    expect(tn(1, "un archivo", "{n} archivos")).toBe("one file");
    expect(tn(3, "un archivo", "{n} archivos")).toBe("3 files");
  });
  it("mensajes del NAS con formatos de Go, anidados y posicionales", () => {
    useLangForTests("en");
    expect(tr("no existe el volumen volume3")).toBe("volume volume3 does not exist");
    expect(tr("Carpeta Docs: no se pudo conectar a nas2 (i/o timeout)")).toBe("Folder Docs: could not connect to nas2 (i/o timeout)");
    expect(tr("el volumen v1 del pool tanque")).toBe("volume v1 in pool tanque");
    expect(tr("algo desconocido")).toBe("algo desconocido");
    expect(tr("Quedan 7 % libres")).toBe("Only 7 % free");
    expect(new RpcError("NOT_FOUND", "no existe el volumen v9").message).toBe("volume v9 does not exist");
  });
});
