// Pantalla del lanzador (página local, file://): rutas relativas.
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  root: "src/launcher",
  base: "./",
  plugins: [react()],
  build: { outDir: "../../dist/launcher", emptyOutDir: true, target: "es2022", rollupOptions: { input: { index: "src/launcher/index.html", error: "src/launcher/error.html" } } },
  publicDir: false,
});
