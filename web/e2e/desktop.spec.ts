import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";
import { mkdirSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// Recorrido E2E de UI sobre el producto real (docs/14 §E2E de UI).
const ART = join(import.meta.dirname, "../../tests/e2e-ui/artifacts");
mkdirSync(ART, { recursive: true });
for (const f of readdirSync(ART)) if (f.endsWith(".png") && !f.startsWith("connect-") && !f.startsWith("pools-") && !f.startsWith("installer-")) rmSync(join(ART, f)); // capturas de esta corrida (TangoConnect, grupos e instalador tienen las suyas)
const PASSWORD = process.env.TANGO_E2E_ADMIN_PASSWORD ?? "";
const DESTRUCTIVE = process.env.TANGO_E2E_DESTRUCTIVE === "1";
let shot = 0;

async function snap(page: Page, name: string) {
  shot++;
  await page.screenshot({ path: join(ART, `${String(shot).padStart(2, "0")}-${name}.png`) });
}

async function openApp(page: Page, id: string) {
  await page.locator(`[data-dock-app="${id}"]`).click();
  await expect(page.locator(".tg-window[data-focused]")).toBeVisible();
}

async function closeFocused(page: Page) {
  await page.locator(".tg-window[data-focused] .tg-tl-close").click();
  await page.waitForTimeout(300);
}

test.describe.configure({ mode: "serial" });

test("recorrido completo del escritorio", async ({ page }) => {
  test.skip(!PASSWORD, "falta TANGO_E2E_ADMIN_PASSWORD");
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await test.step("login", async () => {
    await page.goto("/");
    const pw = page.getByLabel("Contraseña");
    await expect(pw).toBeVisible({ timeout: 30_000 });
    if (!(await page.getByText("Administrador", { exact: true }).isVisible())) {
      await page.getByTitle("Administrador").click();
    }
    await snap(page, "login");
    await page.getByLabel("Contraseña").fill("contraseña-incorrecta");
    await page.keyboard.press("Enter");
    await expect(page.locator(".tg-login-hint")).not.toHaveText(/^$/);
    await page.getByLabel("Contraseña").fill(PASSWORD);
    await page.keyboard.press("Enter");
    await expect(page.locator(".tg-menubar")).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(".tg-widgets")).toBeVisible();
    await page.waitForTimeout(1500); // métricas reales en los widgets
    await snap(page, "escritorio");
  });

  await test.step("almacenamiento: chasis y discos reales", async () => {
    await openApp(page, "tango.storage");
    await expect(page.getByText("volume1").first()).toBeVisible();
    await snap(page, "almacenamiento-resumen");
    await page.getByRole("button", { name: /Discos/ }).first().click();
    await expect(page.getByText("TANGO-D1").first()).toBeVisible();
    await snap(page, "almacenamiento-discos");
  });

  if (DESTRUCTIVE) {
    await test.step("crear RAID 5 desde el asistente y borrarlo", async () => {
      // "Crear volumen" crea dentro de un grupo (ADR 0011): el volumen clásico va por el menú.
      await page.locator(".tg-menubar").getByRole("button", { name: "Volumen", exact: true }).click();
      await page.getByRole("menuitem", { name: /Volumen clásico/ }).click();
      for (const s of ["TANGO-S3", "TANGO-S6", "TANGO-M1"]) {
        await page.getByRole("button", { name: new RegExp(`serie ${s}`) }).click();
      }
      await snap(page, "asistente-discos");
      await page.getByRole("button", { name: "Continuar" }).click();
      await expect(page.getByRole("radiogroup", { name: "Perfil" })).toBeVisible();
      await snap(page, "asistente-perfil");
      await page.getByRole("button", { name: "Continuar" }).click();
      await page.getByLabel("Nombre del volumen").fill("e2eui");
      await page.getByRole("button", { name: "Continuar" }).click();
      const confirm = page.getByLabel("Escribí e2eui para confirmar");
      await expect(confirm).toBeVisible();
      await snap(page, "asistente-revision");
      await confirm.fill("e2eui");
      await page.getByRole("button", { name: "Borrar discos y crear volumen" }).click();
      await expect(page.locator(".tg-sheet").getByRole("button", { name: "Cerrar", exact: true })).toBeVisible({ timeout: 10 * 60_000 });
      await snap(page, "asistente-listo");
      await page.locator(".tg-sheet").getByRole("button", { name: "Cerrar", exact: true }).click();

      await page.getByRole("button", { name: /Volúmenes/ }).first().click();
      const card = page.locator("section").filter({ hasText: "e2eui" });
      const del = card.getByRole("button", { name: "Eliminar volumen…" });
      await expect(del).toBeEnabled({ timeout: 10 * 60_000 }); // espera el resync
      await snap(page, "volumen-e2eui");
      await del.click();
      const typed = page.getByLabel("Escribí e2eui para confirmar");
      await expect(typed).toBeVisible();
      await typed.fill("e2eui");
      await snap(page, "borrar-volumen");
      await page.getByRole("button", { name: "Eliminar volumen y borrar sus datos" }).click();
      await expect(page.locator(".tg-sheet").getByRole("button", { name: "Cerrar", exact: true })).toBeVisible({ timeout: 5 * 60_000 });
      await page.locator(".tg-sheet").getByRole("button", { name: "Cerrar", exact: true }).click();
      await expect(page.locator("section").filter({ hasText: "e2eui" })).toHaveCount(0, { timeout: 60_000 });
    });
  }
  await closeFocused(page);

  await test.step("ajustes: carpetas y usuarios", async () => {
    await openApp(page, "tango.settings");
    await page.getByRole("button", { name: "Carpetas compartidas" }).click();
    await expect(page.getByText("Familia").first()).toBeVisible();
    await snap(page, "ajustes-carpetas");
    await page.getByRole("button", { name: "Usuarios y grupos" }).click();
    await expect(page.getByText("sofia").first()).toBeVisible();
    await snap(page, "ajustes-usuarios");
    await closeFocused(page);
  });

  await test.step("archivos: subir, ver y mandar a la papelera", async () => {
    await openApp(page, "tango.files");
    await page.getByRole("button", { name: "Familia" }).first().click();
    await expect(page.locator(".tg-window[data-focused]").getByText("Familia").first()).toBeVisible();
    const f = join(tmpdir(), `e2e-ui-${Date.now()}.txt`);
    writeFileSync(f, "subido desde Playwright\n");
    await page.locator('.tg-window[data-focused] input[type="file"]').first().setInputFiles(f);
    const name = f.split("/").pop()!;
    const item = page.locator(".tg-window[data-focused]").getByText(name).first();
    await expect(item).toBeVisible({ timeout: 60_000 });
    await snap(page, "archivos-familia");
    await item.click();
    await page.keyboard.press("Delete");
    await expect(item).toBeHidden({ timeout: 30_000 });
    await closeFocused(page);
  });

  await test.step("centro de apps", async () => {
    await openApp(page, "tango.apps");
    await page.getByRole("button", { name: /Instaladas/ }).click();
    await expect(page.getByText("Jellyfin").first()).toBeVisible();
    await snap(page, "apps-instaladas");
    await closeFocused(page);
  });

  await test.step("monitor y WebRTC", async () => {
    await openApp(page, "tango.monitor");
    await page.getByRole("button", { name: "WebRTC" }).click();
    await expect(page.getByText("rpc").first()).toBeVisible();
    await page.waitForTimeout(1500);
    await snap(page, "monitor-webrtc");
    await closeFocused(page);
  });

  await test.step("tema claro y oscuro", async () => {
    await page.getByRole("button", { name: "Centro de control" }).click();
    await page.getByRole("button", { name: "Oscuro" }).click();
    await page.keyboard.press("Escape");
    await page.waitForTimeout(600);
    await snap(page, "escritorio-oscuro");
    await page.getByRole("button", { name: "Centro de control" }).click();
    await page.getByRole("button", { name: "Claro" }).click();
    await page.keyboard.press("Escape");
    await page.waitForTimeout(600);
    await snap(page, "escritorio-claro");
    await page.getByRole("button", { name: "Centro de control" }).click();
    await page.getByRole("button", { name: "Auto" }).click();
    await page.keyboard.press("Escape");
  });

  await test.step("spotlight y cerrar sesión", async () => {
    await page.keyboard.press("Control+k");
    await page.locator(".tg-spot-input input").fill("almacen");
    await expect(page.locator(".tg-spot-hit").first()).toBeVisible();
    await snap(page, "spotlight");
    await page.keyboard.press("Escape");
    await page.locator(".tg-mb-item").first().click();
    await page.getByText(/Cerrar sesión de/).click();
    await expect(page.getByLabel("Contraseña")).toBeVisible({ timeout: 30_000 });
    await snap(page, "sesion-cerrada");
  });

  expect(errors, `errores de JS en la página: ${errors.join(" | ")}`).toEqual([]);
});
