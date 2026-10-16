// App de escritorio (ADR 0018) contra un NAS real: lanzador, huella fijada, escritorio del NAS y
// rechazo de un certificado distinto. Corre la app de Electron compilada en apps/native.
//
//   npm run build -w @tango/native
//   TANGO_E2E_NATIVE_NAS=172.17.0.48 TANGO_E2E_ADMIN_PASSWORD=… npx playwright test native.spec.ts
import { _electron as electron, expect, test } from "@playwright/test";
import { createHash } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import tls from "node:tls";
import { fileURLToPath } from "node:url";

const NAS = process.env.TANGO_E2E_NATIVE_NAS ?? "";
const PASSWORD = process.env.TANGO_E2E_ADMIN_PASSWORD ?? "";
const APP = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../apps/native");

function fingerprint(host: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const s = tls.connect({ host, port: 443, rejectUnauthorized: false }, () => {
      const hex = createHash("sha256").update(s.getPeerCertificate().raw).digest("hex").toUpperCase();
      s.end();
      resolve((hex.match(/../g) ?? []).join(":"));
    });
    s.on("error", reject);
  });
}

async function launch(userData: string) {
  return electron.launch({ args: [APP, `--user-data-dir=${userData}`], env: { ...process.env, LANG: "es_AR.UTF-8" } });
}

test("app de escritorio: elegir el NAS, fijar la huella y abrir su escritorio", async () => {
  test.skip(!NAS || !PASSWORD, "falta TANGO_E2E_NATIVE_NAS o TANGO_E2E_ADMIN_PASSWORD");
  const userData = fs.mkdtempSync(path.join(os.tmpdir(), "tango-native-"));
  const want = await fingerprint(NAS);

  let app = await launch(userData);
  const launcher = await app.firstWindow();
  await expect(launcher.locator(".nl-card")).toBeVisible();
  await launcher.getByRole("button", { name: /Agregar por dirección/ }).click();
  await launcher.getByLabel("Dirección del NAS").fill(NAS);
  await launcher.getByRole("button", { name: "Conectar" }).click();
  // La huella que muestra es la del certificado del NAS.
  await expect(launcher.locator(".nl-fp")).toHaveText(want);
  const opened = app.waitForEvent("window");
  await launcher.getByRole("button", { name: /Es la misma/ }).click();
  const nas = await opened;
  await expect(nas.locator("input[type=password]")).toBeVisible({ timeout: 40_000 });
  expect(nas.url()).toBe(`https://${NAS}/`);
  await nas.locator("input[type=password]").fill(PASSWORD);
  await nas.keyboard.press("Enter");
  await expect(nas.locator(".tg-menubar")).toBeVisible({ timeout: 30_000 });
  expect(await nas.evaluate(() => typeof (window as unknown as { tangoNative?: unknown }).tangoNative)).toBe("object");
  await app.close();
  await new Promise((r) => setTimeout(r, 1000)); // que termine antes de abrirla de nuevo (una sola instancia)

  // Si el NAS presenta otro certificado que el fijado, la ventana no lo abre (otra carpeta de datos con
  // una huella falsa fijada para el mismo NAS).
  const other = fs.mkdtempSync(path.join(os.tmpdir(), "tango-native-"));
  fs.writeFileSync(path.join(other, "nas.json"), JSON.stringify([{ id: `${NAS}:443`, name: "nas-falso", host: NAS, port: 443, fingerprint: "sha-256 " + Array(32).fill("00").join(":"), addedAt: new Date().toISOString() }]));
  app = await launch(other);
  const l2 = await app.firstWindow();
  const w2 = app.waitForEvent("window");
  await l2.locator(".nl-nas").filter({ hasText: "nas-falso" }).click();
  const bad = await w2;
  await expect(bad.locator("body")).toContainText("otro certificado", { timeout: 20_000 });
  expect(bad.url()).toContain("cert=1");
  await expect(bad.locator(".tg-menubar")).toHaveCount(0);
  await app.close();
});
