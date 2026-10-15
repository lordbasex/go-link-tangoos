import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";
import { mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

// Pools ZFS desde la UI (protocol/README.md §18, docs/14 fila 27): demo (habilitar, pool
// con vdevs y caché, asistente) y, en el NAS real, crear un espejo con S3 + S6, un volumen
// y destruirlo. Usa SOLO discos libres. Capturas: tests/e2e-ui/artifacts/zfs-*.png
const ART = join(import.meta.dirname, "../../tests/e2e-ui/artifacts");
mkdirSync(ART, { recursive: true });
// La contraseña por variable o por archivo (TANGO_E2E_ADMIN_PASSWORD_FILE, así no pasa por argv).
const PASSWORD = process.env.TANGO_E2E_ADMIN_PASSWORD ?? (process.env.TANGO_E2E_ADMIN_PASSWORD_FILE ? readFileSync(process.env.TANGO_E2E_ADMIN_PASSWORD_FILE, "utf8").trim() : "");
const DESTRUCTIVE = process.env.TANGO_E2E_DESTRUCTIVE === "1";
let n = 0;

async function snap(page: Page, name: string) {
  n++;
  await page.screenshot({ path: join(ART, `zfs-${String(n).padStart(2, "0")}-${name}.png`) });
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

test("demo: habilitar ZFS, pool RAIDZ1 y asistente", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/?skip&mock&scenario=hybrid");
  await openStorage(page);
  await page.getByRole("button", { name: /^Pools ZFS/ }).click();
  await expect(page.getByRole("button", { name: "Habilitar ZFS" })).toBeVisible();
  await expect(page.getByText(/licencia CDDL/)).toBeVisible();
  await snap(page, "habilitar");
  await page.getByRole("button", { name: "Habilitar ZFS" }).click();
  await expect(page.locator(".ts-zfs-enable .tg-progress, .ts-zfs-enable [role=progressbar]").first()).toBeVisible();
  await snap(page, "habilitando");

  await page.goto("/?skip&mock&scenario=zfs");
  await openStorage(page);
  await page.getByRole("button", { name: /^Pools ZFS/ }).click();
  await expect(page.getByText("Caché (L2ARC)")).toBeVisible();
  await expect(page.getByText("vm-windows", { exact: true })).toBeVisible();
  await snap(page, "pool");
  await page.getByRole("button", { name: "Buscar pools para importar" }).click();
  await expect(page.getByText("Pools encontrados en los discos")).toBeVisible();
  await snap(page, "importar");

  await page.getByRole("button", { name: "Crear pool ZFS" }).first().click();
  for (const s of ["WD-8KJ2P0QN", "WD-8KJ7X3LM"]) await sheet(page).getByRole("button", { name: new RegExp(`serie ${s}`) }).click();
  await sheet(page).getByRole("button", { name: "Continuar" }).click();
  await expect(sheet(page).getByRole("radio", { name: /^Espejo/ })).toHaveAttribute("aria-checked", "true");
  await expect(sheet(page).locator(".ts-zfs-diagram")).toBeVisible();
  await snap(page, "asistente-forma");
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: /^Volúmenes/ }).first().click();
  await expect(page.getByText("Nativo de ZFS (AES-256-GCM)")).toBeVisible();
  await snap(page, "volumen");
  expect(errors).toEqual([]);
});

test("NAS real: espejo ZFS con S3 + S6 desde el asistente, volumen y destruir", async ({ page }) => {
  test.skip(!PASSWORD || !DESTRUCTIVE, "prueba destructiva: TANGO_E2E_ADMIN_PASSWORD y TANGO_E2E_DESTRUCTIVE=1");
  test.setTimeout(20 * 60_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/");
  await expect(page.getByLabel("Contraseña")).toBeVisible({ timeout: 30_000 });
  if (!(await page.getByText("Administrador", { exact: true }).isVisible())) await page.getByTitle("Administrador").click();
  await page.getByLabel("Contraseña").fill(PASSWORD);
  await page.keyboard.press("Enter");
  await expect(page.locator(".tg-menubar")).toBeVisible({ timeout: 30_000 });
  await openStorage(page);
  await page.getByRole("button", { name: /^Pools ZFS/ }).click();

  await test.step("asistente: espejo", async () => {
    await page.getByRole("button", { name: "Crear pool ZFS" }).first().click();
    for (const s of ["TANGO-S3", "TANGO-S6"]) await sheet(page).getByRole("button", { name: new RegExp(`serie ${s}`) }).click();
    await sheet(page).getByRole("button", { name: "Continuar" }).click();
    await expect(sheet(page).getByRole("radio", { name: /^Espejo/ })).toHaveAttribute("aria-checked", "true");
    await sheet(page).getByRole("button", { name: "Continuar" }).click();
    await sheet(page).getByLabel("Nombre del pool").fill("tzui");
    await sheet(page).getByRole("button", { name: "Continuar" }).click();
    const confirm = sheet(page).getByLabel("Escribí tzui para confirmar");
    await expect(confirm).toBeVisible();
    await snap(page, "real-revision");
    await confirm.fill("tzui");
    await sheet(page).getByRole("button", { name: "Borrar discos y crear el pool" }).click();
    await expect(sheet(page).getByRole("heading", { name: "El pool tzui está listo" })).toBeVisible({ timeout: 5 * 60_000 });
    await sheet(page).getByRole("button", { name: "Cerrar", exact: true }).click();
  });

  const card = page.locator(".ts-zfs-pool").filter({ hasText: "tzui" });
  await test.step("volumen en el pool", async () => {
    await card.getByRole("button", { name: "Crear volumen…" }).click();
    await sheet(page).getByLabel("Nombre del volumen").fill("zui");
    await sheet(page).getByRole("button", { name: "Crear volumen" }).click();
    await expect(sheet(page).getByRole("heading", { name: "zui está listo" })).toBeVisible({ timeout: 5 * 60_000 });
    await sheet(page).getByRole("button", { name: "Cerrar", exact: true }).click();
    await expect(card.locator(".ts-pool-vol").filter({ hasText: "zui" })).toBeVisible({ timeout: 30_000 });
    await snap(page, "real-pool");
  });

  await test.step("destruir", async () => {
    await card.getByRole("button", { name: "Destruir…" }).click();
    await sheet(page).getByLabel("Escribí tzui para confirmar").fill("tzui");
    await sheet(page).getByRole("button", { name: "Destruir el pool" }).click();
    await expect(page.locator(".ts-zfs-pool").filter({ hasText: "tzui" })).toHaveCount(0, { timeout: 5 * 60_000 });
  });
  expect(errors).toEqual([]);
});
