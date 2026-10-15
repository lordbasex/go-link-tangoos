import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

// Grupos de almacenamiento desde la UI (protocol/README.md §16, docs/14 fila 18): el
// asistente con RAID híbrido, un volumen cifrado, bloquear y desbloquear, y borrar todo.
// Usa SOLO discos libres del laboratorio (S3, S6, M1). Capturas: tests/e2e-ui/artifacts/pools-*.png
const ART = join(import.meta.dirname, "../../tests/e2e-ui/artifacts");
mkdirSync(ART, { recursive: true });
const PASSWORD = process.env.TANGO_E2E_ADMIN_PASSWORD ?? "";
const DESTRUCTIVE = process.env.TANGO_E2E_DESTRUCTIVE === "1";
let n = 0;

async function snap(page: Page, name: string) {
  n++;
  await page.screenshot({ path: join(ART, `pools-${String(n).padStart(2, "0")}-${name}.png`) });
}

async function openStorage(page: Page) {
  await page.locator('[data-dock-app="tango.storage"]').click();
  await expect(page.locator(".tg-window[data-focused]")).toBeVisible();
  await page.locator(".tg-window[data-focused]").evaluate((el) => {
    el.style.left = "40px";
    el.style.top = "36px";
    el.style.width = "1320px";
    el.style.height = "900px";
  });
}

const sheet = (page: Page) => page.locator(".tg-sheet");

test.describe.configure({ mode: "serial" });

test("demo: grupos, RAID híbrido y volumen bloqueado", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/?skip&mock&scenario=pools");
  await openStorage(page);
  await page.getByRole("button", { name: /^Grupos/ }).first().click();
  await expect(page.getByText("Cómo se reparten los discos")).toBeVisible();
  await expect(page.getByText(/Tramo 3: Reservado/)).toBeVisible();
  await snap(page, "demo-grupo");
  await page.getByRole("button", { name: /^Volúmenes/ }).first().click();
  await expect(page.getByText("Aciertos de lectura")).toBeVisible();
  await snap(page, "demo-volumenes-cache");

  await page.goto("/?skip&mock&scenario=hybrid");
  await openStorage(page);
  await page.getByRole("button", { name: "Crear grupo" }).click();
  for (const s of ["ZDH8P2L1", "WD-CA1K9L2M", "ZA1F3K7Q", "WD-8KJ2P0QN"]) await page.getByRole("button", { name: new RegExp(`serie ${s}`) }).click();
  await page.getByRole("button", { name: "Continuar" }).click();
  await expect(page.getByText("Tramo 1: RAID 5 de 4 discos")).toBeVisible();
  await snap(page, "demo-asistente-hibrido");

  await page.goto("/?skip&mock&scenario=locked");
  await openStorage(page);
  await page.getByRole("button", { name: /^Volúmenes/ }).first().click();
  await expect(page.getByText("privado está bloqueado")).toBeVisible();
  await page.getByRole("button", { name: "Desbloquear…" }).first().click();
  await sheet(page).getByLabel("Frase de cifrado").fill("una frase de demo larga");
  await snap(page, "demo-desbloquear");
  await sheet(page).getByRole("button", { name: "Desbloquear", exact: true }).click();
  await expect(page.getByText("privado está bloqueado")).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("NAS real: grupo híbrido con discos mixtos, volumen cifrado, bloquear y borrar", async ({ page }) => {
  test.skip(!PASSWORD || !DESTRUCTIVE, "prueba destructiva: TANGO_E2E_ADMIN_PASSWORD y TANGO_E2E_DESTRUCTIVE=1");
  test.setTimeout(30 * 60_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const pass = `frase-${Date.now()}-de-prueba`;

  await page.goto("/");
  await expect(page.getByLabel("Contraseña")).toBeVisible({ timeout: 30_000 });
  if (!(await page.getByText("Administrador", { exact: true }).isVisible())) await page.getByTitle("Administrador").click();
  await page.getByLabel("Contraseña").fill(PASSWORD);
  await page.keyboard.press("Enter");
  await expect(page.locator(".tg-menubar")).toBeVisible({ timeout: 30_000 });
  await openStorage(page);

  await test.step("asistente: Híbrido-1 con 2 + 2 + 4 GB", async () => {
    await page.getByRole("button", { name: "Crear grupo" }).click();
    for (const s of ["TANGO-S3", "TANGO-S6", "TANGO-M1"]) await page.getByRole("button", { name: new RegExp(`serie ${s}`) }).click();
    await snap(page, "asistente-discos");
    await page.getByRole("button", { name: "Continuar" }).click();
    await expect(page.getByRole("radio", { name: /^Híbrido-1/ })).toHaveAttribute("aria-checked", "true");
    await expect(page.getByText(/Tramo 2: Reservado/)).toBeVisible();
    await snap(page, "asistente-proteccion");
    await page.getByRole("button", { name: "Continuar" }).click();
    await page.getByLabel("Nombre del grupo").fill("pe2eui");
    await page.getByRole("button", { name: "Continuar" }).click();
    const confirm = page.getByLabel("Escribí pe2eui para confirmar");
    await expect(confirm).toBeVisible();
    await snap(page, "asistente-revision");
    await confirm.fill("pe2eui");
    await page.getByRole("button", { name: "Borrar discos y crear el grupo" }).click();
    await expect(sheet(page).getByRole("button", { name: /Crear un volumen en pe2eui/ })).toBeVisible({ timeout: 10 * 60_000 });
    await snap(page, "grupo-listo");
  });

  await test.step("volumen cifrado dentro del grupo", async () => {
    await sheet(page).getByRole("button", { name: /Crear un volumen en pe2eui/ }).click();
    await sheet(page).getByLabel("Nombre del volumen").fill("pvui");
    await sheet(page).getByRole("switch", { name: "Cifrar" }).click();
    await sheet(page).getByLabel("Frase de cifrado").fill(pass);
    await sheet(page).getByLabel("Repetí la frase").fill(pass);
    await snap(page, "volumen-cifrado");
    await sheet(page).getByRole("button", { name: "Crear volumen" }).click();
    await expect(sheet(page).getByRole("heading", { name: "pvui está listo" })).toBeVisible({ timeout: 10 * 60_000 });
    await sheet(page).getByRole("button", { name: "Cerrar", exact: true }).click();
    await page.getByRole("button", { name: /^Grupos/ }).first().click();
    await expect(page.locator(".ts-pool-vol").filter({ hasText: "pvui" })).toBeVisible({ timeout: 30_000 });
    await snap(page, "grupo-con-volumen");
  });

  await test.step("bloquear y desbloquear", async () => {
    await page.getByRole("button", { name: /^Volúmenes/ }).first().click();
    const card = page.locator(".ts-stack").filter({ hasText: "/dev/pe2eui/pvui" }).last();
    await card.getByRole("button", { name: "Bloquear" }).click();
    await expect(page.getByText("pvui está bloqueado")).toBeVisible({ timeout: 30_000 });
    await snap(page, "bloqueado");
    await page.locator(".ts-locked-card").filter({ hasText: "pvui" }).getByRole("button", { name: "Desbloquear…" }).click();
    await sheet(page).getByLabel("Frase de cifrado").fill("una frase equivocada");
    await sheet(page).getByRole("button", { name: "Desbloquear", exact: true }).click();
    await expect(sheet(page).getByText("La frase no abre este volumen.")).toBeVisible();
    await sheet(page).getByLabel("Frase de cifrado").fill(pass);
    await sheet(page).getByRole("button", { name: "Desbloquear", exact: true }).click();
    await expect(page.getByText("pvui está bloqueado")).toHaveCount(0, { timeout: 30_000 });
    await snap(page, "desbloqueado");
  });

  await test.step("borrar el volumen y el grupo", async () => {
    const card = page.locator(".ts-stack").filter({ hasText: "/dev/pe2eui/pvui" }).last();
    const del = card.getByRole("button", { name: "Eliminar volumen…" });
    await expect(del).toBeEnabled({ timeout: 10 * 60_000 }); // espera la sincronización inicial
    await del.click();
    await page.getByLabel("Escribí pvui para confirmar").fill("pvui");
    await page.getByRole("button", { name: "Eliminar volumen y borrar sus datos" }).click();
    await expect(sheet(page).getByRole("button", { name: "Cerrar", exact: true })).toBeVisible({ timeout: 5 * 60_000 });
    await sheet(page).getByRole("button", { name: "Cerrar", exact: true }).click();
    await page.getByRole("button", { name: /^Grupos/ }).first().click();
    const pool = page.locator("section").filter({ hasText: "pe2eui" });
    await expect(pool.getByRole("button", { name: "Eliminar grupo…" })).toBeEnabled({ timeout: 60_000 });
    await pool.getByRole("button", { name: "Eliminar grupo…" }).click();
    await page.getByLabel("Escribí pe2eui para confirmar").fill("pe2eui");
    await snap(page, "borrar-grupo");
    await sheet(page).getByRole("button", { name: "Eliminar el grupo" }).click();
    await expect(page.locator("section").filter({ hasText: "pe2eui" })).toHaveCount(0, { timeout: 5 * 60_000 });
  });
  expect(errors).toEqual([]);
});
