// Proceso principal y preloads de Electron: CommonJS para Node (los preloads con sandbox no admiten ESM).
import { builtinModules } from "node:module";
import { defineConfig } from "vite";

export default defineConfig({
  build: {
    outDir: "dist/main",
    emptyOutDir: true,
    target: "node22",
    ssr: true,
    minify: false,
    sourcemap: false,
    rollupOptions: {
      input: { main: "src/main/main.ts", "preload-launcher": "src/preload/launcher.ts", "preload-nas": "src/preload/nas.ts" },
      external: ["electron", ...builtinModules, ...builtinModules.map((m) => `node:${m}`)],
      output: { format: "cjs", entryFileNames: "[name].js" },
    },
  },
  ssr: { noExternal: ["bonjour-service"] },
});
