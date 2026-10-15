import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

// Clúster, segunda etapa (docs/17): unión con código y huella, TLS, bricks dedicados,
// operaciones de bricks, cuotas, instantáneas y NFS. Dos recorridos:
//   - demo (?scenario=cluster) contra CLUSTER_DEMO_URL (por defecto el vite de desarrollo);
//   - clúster real, solo lectura, contra CLUSTER_NODE con TANGO_E2E_ADMIN_PASSWORD (el clúster
//     tiene que estar armado: 3 nodos, TLS y un volumen con bricks dedicados).
// Capturas: tests/e2e-ui/artifacts/cluster2-*.png
const ART = join(import.meta.dirname, "../../tests/e2e-ui/artifacts");
mkdirSync(ART, { recursive: true });
const DEMO = process.env.CLUSTER_DEMO_URL ?? "http://localhost:5181";
const NODE = process.env.CLUSTER_NODE ?? "";
const PASSWORD = process.env.TANGO_E2E_ADMIN_PASSWORD ?? "";
let n = 0;

async function snap(page: Page, name: string) {
  n++;
  await page.waitForTimeout(400);
  await page.screenshot({ path: join(ART, `cluster2-${String(n).padStart(2, "0")}-${name}.png`) });
}

async function openCluster(page: Page) {
  // Clúster no está fijo en el Dock: se abre desde el Launcher (Alt+Espacio).
  await page.keyboard.press("Alt+Space");
  await page.getByRole("button", { name: "Clúster", exact: true }).first().click();
  await expect(page.locator(".tg-window[data-focused]")).toBeVisible();
  await page.locator(".tg-window[data-focused]").evaluate((el) => {
    el.style.left = "40px";
    el.style.top = "36px";
    el.style.width = "1320px";
    el.style.height = "900px";
  });
}

const sheet = (page: Page) => page.locator(".tg-sheet");
const win = (page: Page) => page.locator(".tg-window[data-focused]");
const side = (page: Page, label: string) => page.locator(".tg-window[data-focused] .tg-sidebar").getByText(label, { exact: true });

test.describe.configure({ mode: "serial" });

test("demo: TLS, bricks dedicados, unión con huella, bricks, cuotas, instantáneas y NFS", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(`${DEMO}/?skip&mock&scenario=cluster`);
  await openCluster(page);
  await expect(page.getByText("Con TLS")).toBeVisible();
  await snap(page, "demo-resumen");

  await side(page, "Nodos").click();
  await expect(page.getByText("TLS prendido")).toBeVisible();
  await snap(page, "demo-nodos-tls");

  // Invitar: código y huella
  await win(page).getByRole("button", { name: "Invitar un nodo" }).first().click();
  await sheet(page).getByRole("textbox").fill("192.168.1.30");
  await sheet(page).getByRole("button", { name: "Invitar" }).click();
  await expect(sheet(page).getByLabel("Código de unión")).toHaveText("K7QWM-4RZTP");
  await snap(page, "demo-invitar-codigo");
  await sheet(page).getByRole("button", { name: "Listo" }).click();

  // Agregar: código → huella para comparar
  await win(page).getByRole("button", { name: "Agregar un nodo" }).first().click();
  await sheet(page).getByPlaceholder("192.168.1.21").fill("tango-d");
  await sheet(page).getByPlaceholder("ABCDE-FGHJK").fill("K7QWM-4RZTP");
  await sheet(page).getByRole("button", { name: "Continuar" }).click();
  await expect(sheet(page).getByText(/Huella de tango-d/)).toBeVisible();
  await snap(page, "demo-agregar-huella");
  await sheet(page).getByRole("button", { name: /Coinciden/ }).click();
  await expect(page.locator(".tcl-table b").getByText("tango-d", { exact: true })).toBeVisible();

  // TLS: la parada controlada
  await win(page).getByRole("button", { name: "Apagar…" }).click();
  await expect(sheet(page).getByText("parada controlada")).toBeVisible();
  await snap(page, "demo-tls-parada");
  await sheet(page).getByRole("button", { name: "Cancelar" }).click();

  // Volumen con bricks dedicados e instantáneas
  await side(page, "gv_tel_config").click();
  await expect(page.getByText("Volumen dedicado").first()).toBeVisible();
  await snap(page, "demo-volumen-dedicado");
  await win(page).getByRole("button", { name: "Instantáneas" }).click();
  await expect(sheet(page).getByText("antes-de-migrar")).toBeVisible();
  await snap(page, "demo-instantaneas");
  await sheet(page).getByRole("button", { name: "Cerrar" }).click();

  // Bricks: agregar un conjunto (selector) y rebalancear con avance
  await win(page).getByRole("button", { name: /^Bricks/ }).click();
  await expect(sheet(page).getByText(/Elegí 3 nodos/)).toBeVisible();
  for (const host of ["tango", "tango-b", "tango-c"]) await sheet(page).getByRole("switch", { name: host, exact: true }).click();
  await snap(page, "demo-bricks-agregar");
  await sheet(page).getByRole("button", { name: "Agregar 3 bricks" }).click();
  await expect(side(page, "gv_tel_config")).toBeVisible();
  await page.waitForTimeout(3500);
  await win(page).getByRole("button", { name: /^Bricks/ }).click();
  await sheet(page).getByRole("group", { name: "Operación" }).getByRole("button", { name: "Rebalancear" }).click();
  await sheet(page).getByRole("button", { name: "Rebalancear", exact: true }).last().click();
  await expect(sheet(page).getByText(/archivos movidos/)).toBeVisible();
  await expect(sheet(page).getByText(/^Terminado/)).toBeVisible({ timeout: 20_000 });
  await snap(page, "demo-rebalance");
  await sheet(page).getByRole("button", { name: "Cerrar" }).click();

  // Cuotas y NFS en el volumen de locuciones
  await side(page, "gv_tel_locuciones").click();
  await win(page).getByRole("button", { name: "Cuotas" }).click();
  await expect(sheet(page).getByText("/Grabaciones")).toBeVisible();
  await snap(page, "demo-cuotas");
  await sheet(page).getByRole("button", { name: "Cerrar" }).click();
  await win(page).getByRole("button", { name: "NFS" }).click();
  await expect(sheet(page).getByText("192.168.1.0/24")).toBeVisible();
  await snap(page, "demo-nfs");
  await sheet(page).getByRole("button", { name: "Cancelar" }).click();

  // Crear: un brick por nodo, carpeta o volumen dedicado
  await win(page).getByRole("button", { name: /Crear volumen/ }).first().click();
  await sheet(page).getByPlaceholder("gv_documentos").fill("gv_nuevo");
  await expect(sheet(page).getByText("/srv/gluster/gv_nuevo/brick · XFS").first()).toBeVisible();
  await snap(page, "demo-crear-dedicado");
  expect(errors).toEqual([]);
});

test("clúster real: resumen, nodos con TLS y un volumen con bricks dedicados", async ({ page }) => {
  test.skip(!NODE || !PASSWORD, "CLUSTER_NODE y TANGO_E2E_ADMIN_PASSWORD (clúster armado)");
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(`https://${NODE}/`);
  await expect(page.getByLabel("Contraseña")).toBeVisible({ timeout: 30_000 });
  if (!(await page.getByText("Administrador", { exact: true }).isVisible())) await page.getByTitle("Administrador").click();
  await page.getByLabel("Contraseña").fill(PASSWORD);
  await page.keyboard.press("Enter");
  await expect(page.locator(".tg-menubar")).toBeVisible({ timeout: 30_000 });
  await openCluster(page);
  await expect(page.getByText("Con TLS")).toBeVisible({ timeout: 30_000 });
  await snap(page, "real-resumen");
  await side(page, "Nodos").click();
  await expect(page.getByText("TLS prendido")).toBeVisible();
  await snap(page, "real-nodos-tls");
  await side(page, process.env.CLUSTER_VOLUME ?? "gv_lv").click();
  await expect(page.getByText("Volumen dedicado").first()).toBeVisible();
  await snap(page, "real-volumen");
  await win(page).getByRole("button", { name: "Ver como gluster volume info" }).click();
  await expect(page.getByLabel("Salida de gluster volume info")).toContainText("auth.ssl-allow");
  await snap(page, "real-volume-info");
  await win(page).getByRole("button", { name: "Instantáneas" }).click();
  await snap(page, "real-instantaneas");
  await sheet(page).getByRole("button", { name: "Cerrar" }).click();
  await win(page).getByRole("button", { name: "Cuotas" }).click();
  await snap(page, "real-cuotas");
  expect(errors).toEqual([]);
});
