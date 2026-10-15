import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// `?backend=rtc` en dev: la señalización va por este proxy a tangod (por defecto, la VM de desarrollo).
// El tráfico WebRTC (UDP 8443) va directo del navegador al NAS.
const backend = process.env.TANGO_BACKEND ?? "https://172.17.0.44";

export default defineConfig({
  // Rutas relativas: el mismo build lo sirve tangod en "/" y el portal TangoConnect lo monta desde
  // /nas-bundle/<deviceId>/ (Cache Storage + Service Worker, docs/16).
  base: "./",
  plugins: [react()],
  build: { target: "es2022", cssCodeSplit: true, chunkSizeWarningLimit: 400 },
  server: {
    port: 5173,
    proxy: {
      "/app-icons": { target: backend, secure: false, changeOrigin: true },
      "/signal": {
        target: backend,
        ws: true,
        secure: false,
        changeOrigin: true,
        // tangod valida que Origin coincida con Host: en dev reescribimos el Origin del navegador.
        configure: (proxy) => proxy.on("proxyReqWs", (req) => req.setHeader("origin", backend)),
      },
    },
  },
});
