import { defineConfig, devices } from "@playwright/test";

// E2E de UI contra el producto real (docs/14). La contraseña de admin llega por
// TANGO_E2E_ADMIN_PASSWORD (el Makefile la lee del NAS por SSH; nunca se imprime).
export default defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.ts/,
  timeout: 15 * 60_000,
  expect: { timeout: 30_000 },
  workers: 1,
  retries: 0,
  reporter: [["list"], ["json", { outputFile: "../../tests/e2e-ui/results.json" }]],
  outputDir: "../../tests/e2e-ui/test-results",
  use: {
    baseURL: `https://${process.env.TANGO_HOST ?? "172.17.0.44"}`,
    ignoreHTTPSErrors: true, // certificado autofirmado del laboratorio
    viewport: { width: 1600, height: 1000 },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1600, height: 1000 } } }],
});
