import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { readdirSync, rmSync } from "node:fs";
import { join } from "node:path";

// Los PNG originales de las capturas (public/img/*.png, ~1 MB cada uno) no se publican: el sitio usa
// las versiones WebP de public/img/opt (scripts/optimize-connect-img.sh).
const dropOriginalShots = () => ({
  name: "tangoos-drop-original-shots",
  closeBundle() {
    const dir = join(import.meta.dirname, "dist", "img");
    try {
      for (const f of readdirSync(dir)) if (f.endsWith(".png")) rmSync(join(dir, f));
    } catch {
      /* sin carpeta img */
    }
  },
});

// Portal TangoOS Connect (https://tangoos.go-link.org). Estático: lo publica scripts/deploy-connect-web.sh
// o lo sirve cmd/connect-web. Variables de build (docs/16):
//   VITE_SIGNAL_URL     signalhub (por defecto wss://signal.go-link.org/ws?v=1)
//   VITE_BUNDLE_PUBKEY  clave pública Ed25519 (base64) que firma el escritorio que entrega el NAS
export default defineConfig({
  plugins: [react(), dropOriginalShots()],
  build: { target: "es2022", sourcemap: false, chunkSizeWarningLimit: 600 },
  server: { port: 5174 },
  preview: { port: 5174 },
});
