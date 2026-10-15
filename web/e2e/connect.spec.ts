import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { join } from "node:path";

// TangoConnect de punta a punta con un navegador real (docs/16, protocol/README.md §15):
// portal → código de vinculación → escritorio que entrega el NAS (firmado) → login → almacenamiento →
// enlace público de una foto → descarga en otro contexto (sha256) → reach con prueba del host → revocar.
//
//   TANGO_CONNECT_PORTAL  origen del portal (p. ej. http://127.0.0.1:5174), armado con la clave pública dev
//   TANGO_CTL             comando tangoctl como admin contra el NAS (script; nunca imprime la contraseña)
//   TANGO_VM_SSH          comando que corre algo por SSH en el NAS (para el sha256 del archivo original)
//   TANGO_E2E_ADMIN_PASSWORD
const PORTAL = process.env.TANGO_CONNECT_PORTAL ?? "";
const CTL = process.env.TANGO_CTL ?? "";
const SSH = process.env.TANGO_VM_SSH ?? "";
const PASSWORD = process.env.TANGO_E2E_ADMIN_PASSWORD ?? "";
const PHOTO = process.env.TANGO_CONNECT_PHOTO ?? "Atardecer en el Río.jpg";

const ART = join(import.meta.dirname, "../../tests/e2e-ui/artifacts");
mkdirSync(ART, { recursive: true });
for (const f of readdirSync(ART)) if (f.startsWith("connect-") && f.endsWith(".png")) rmSync(join(ART, f));
let shot = 0;
async function snap(page: Page, name: string) {
  shot++;
  await page.screenshot({ path: join(ART, `connect-${String(shot).padStart(2, "0")}-${name}.png`) });
}

function ctl(...args: string[]): unknown {
  const out = execFileSync(CTL, args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  const i = out.search(/[[{]/);
  return JSON.parse(out.slice(i));
}
const ssh = (cmd: string) => execFileSync(SSH, [cmd], { encoding: "utf8" }).trim();

test.describe.configure({ mode: "serial" });

test("TangoConnect: vincular, escritorio del NAS, enlace público", async ({ browser }) => {
  test.skip(!PORTAL || !CTL || !SSH || !PASSWORD, "faltan TANGO_CONNECT_PORTAL / TANGO_CTL / TANGO_VM_SSH / TANGO_E2E_ADMIN_PASSWORD");
  test.setTimeout(10 * 60_000);
  const ctx = await browser.newContext({ baseURL: PORTAL, viewport: { width: 1600, height: 1000 }, acceptDownloads: true });
  const page = await ctx.newPage();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  let deviceId = "";

  await test.step("vincular con el código del NAS", async () => {
    await page.goto("/nas/vincular");
    await snap(page, "vincular");
    const pair = ctl("rpc", "connect.pair.start") as { code: string };
    const digits = pair.code.replace(/\D/g, "");
    expect(digits).toHaveLength(9);
    await page.getByLabel("Dígito 1").click();
    await page.keyboard.type(digits);
    await page.locator('form button[type="submit"]').click();
    await page.waitForURL(/\/nas\/[0-9a-f-]{36}$/, { timeout: 60_000 });
    deviceId = new URL(page.url()).pathname.split("/").pop()!;
  });

  await test.step("el escritorio lo entrega el NAS (firmado), no el modo desarrollo", async () => {
    await expect(page.locator(".tc-steps")).toBeVisible({ timeout: 30_000 }).catch(() => {});
    await snap(page, "conectando");
    const pw = page.getByLabel("Contraseña");
    await expect(pw).toBeVisible({ timeout: 90_000 });
    await expect(page.getByText(/Modo desarrollo/)).toHaveCount(0);
    // Los módulos del escritorio vienen de /nas-bundle/<deviceId>/ (Cache Storage + Service Worker del portal).
    const fromBundle = await page.evaluate(
      (id) => performance.getEntriesByType("resource").some((e) => e.name.includes(`/nas-bundle/${id}/assets/`)),
      deviceId,
    );
    expect(fromBundle).toBe(true);
    await snap(page, "login-remoto");
    await pw.fill(PASSWORD);
    await page.keyboard.press("Enter");
    await expect(page.locator(".tg-menubar")).toBeVisible({ timeout: 60_000 });
    await page.waitForTimeout(1500);
    await snap(page, "escritorio-remoto");
  });

  await test.step("almacenamiento: rack de 12 bahías por TangoConnect", async () => {
    await page.locator('[data-dock-app="tango.storage"]').click();
    await expect(page.getByText("volume1").first()).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(".ts-bay")).toHaveCount(12);
    await snap(page, "almacenamiento-remoto");
    await page.locator(".tg-window[data-focused] .tg-tl-close").click();
  });

  let shareURL = "";
  await test.step("Archivos: crear enlace público de una foto de Fotos", async () => {
    await page.locator('[data-dock-app="tango.files"]').click();
    await expect(page.locator(".tg-window[data-focused]")).toBeVisible();
    await page.locator(".tg-side-item", { hasText: "Fotos" }).first().click();
    const item = page.getByText(PHOTO, { exact: true }).first();
    await expect(item).toBeVisible({ timeout: 30_000 });
    await item.click({ button: "right" });
    await page.getByRole("menuitem", { name: /Compartir enlace/ }).click();
    await snap(page, "compartir-hoja");
    const create = page.locator(".tg-sheet").getByRole("button", { name: /Crear enlace|Crear|Compartir/ }).last();
    await create.click();
    await page.waitForTimeout(1500);
    await snap(page, "compartir-creado");
    const links = ctl("rpc", "shares.links.list", '{"all":true}') as { id: string; url: string; path?: string; name?: string }[];
    const mine = links.filter((l) => (l.path ?? l.name ?? l.url).includes(PHOTO.split(".")[0]) || decodeURIComponent(l.url).includes(PHOTO));
    expect(mine.length).toBeGreaterThan(0);
    shareURL = mine[mine.length - 1].url;
  });

  await test.step("enlace público en un contexto nuevo: descarga y sha256", async () => {
    const u = new URL(shareURL);
    const guestURL = PORTAL + u.pathname + u.hash; // mismo portal (127.0.0.1) que la prueba
    const g = await browser.newContext({ viewport: { width: 1280, height: 860 }, acceptDownloads: true });
    const gp = await g.newPage();
    await gp.goto(guestURL);
    const btn = gp.getByRole("button", { name: "Descargar" }).first();
    await expect(btn).toBeVisible({ timeout: 90_000 });
    await snap(gp, "enlace-publico");
    const [dl] = await Promise.all([gp.waitForEvent("download", { timeout: 120_000 }), btn.click()]);
    const file = join(ART, "..", "connect-download.bin");
    await dl.saveAs(file);
    const got = createHash("sha256").update(readFileSync(file)).digest("hex");
    rmSync(file);
    const want = ssh(`sha256sum "$(find /volume*/@shares/Fotos -maxdepth 3 -name '${PHOTO}' | head -1)" | cut -d' ' -f1`);
    expect(got).toBe(want);
    await g.close();
  });

  await test.step("recargar: reach con prueba del host, sin volver a vincular", async () => {
    await page.goto(`/nas/${deviceId}`);
    await expect(page.locator(".tg-menubar, .tg-login-card").first()).toBeVisible({ timeout: 90_000 });
    await expect(page.getByText(/Modo desarrollo/)).toHaveCount(0);
    await snap(page, "reach");
  });

  await test.step("revocar el vínculo del navegador: queda bloqueado", async () => {
    const links = ctl("rpc", "connect.links.list") as { id: string }[];
    for (const l of links) ctl("rpc", "connect.links.revoke", JSON.stringify({ id: l.id }));
    await page.goto(`/nas/${deviceId}`);
    await expect(page.getByRole("alert")).toBeVisible({ timeout: 90_000 });
    await snap(page, "revocado");
  });

  expect(errors.filter((e) => !/ResizeObserver/.test(e))).toEqual([]);
  await ctx.close();
});
