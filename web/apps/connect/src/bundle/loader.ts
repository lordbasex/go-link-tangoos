// Cargador del escritorio que entrega el NAS (docs/16 §4).
// 1. bundle.manifest → verifica forma + firma Ed25519 (clave embebida en el portal).
// 2. bundle.get {path} por cada archivo (RtcTransport.fetchBundleFile: frames del canal `bundle`)
//    → verifica el sha256 contra el manifiesto FIRMADO (no contra lo que dice bundle.get).
// 3. Guarda cada archivo en Cache Storage como /nas-bundle/<deviceId>/<path>; el Service Worker del
//    portal (/bundle-sw.js, scope /nas-bundle/) los sirve con su Content-Type.
// 4. Inyecta los <link rel=stylesheet> y el <script type=module> del index.html del NAS, con rutas
//    relativas a /nas-bundle/<deviceId>/ (el build del escritorio usa base "./").
// El escritorio del NAS toma el transporte ya abierto de window.__TANGO_TRANSPORT__.
import type { RtcTransport, Transport } from "@tango/client";
import { contentType, sha256Hex, validateManifestShape, verifyManifest, type BundleManifestResult } from "./manifest";

export type LoadStage = { kind: "manifest" } | { kind: "files"; done: number; total: number; bytes: number; totalBytes: number } | { kind: "mount" };

export class BundleError extends Error {
  constructor(
    readonly code: "NO_BUNDLE" | "BAD_MANIFEST" | "BAD_SIGNATURE" | "BAD_HASH" | "NO_SW" | "TRANSFER",
    message: string,
  ) {
    super(message);
  }
}

export const bundlePrefix = (deviceId: string) => `/nas-bundle/${deviceId}/`;

type Caller = (method: string, params: unknown) => Promise<unknown>;

export function callerOf(t: Transport): Caller {
  return (m, p) => (t.call as unknown as (m: string, p: unknown) => Promise<unknown>)(m, p);
}

/**
 * El SW tiene que CONTROLAR esta página: un Service Worker solo intercepta los pedidos de las páginas que
 * controla, no los pedidos a URLs dentro de su scope hechos desde páginas de afuera. Por eso el scope es "/"
 * (el SW deja pasar todo lo que no sea /nas-bundle/) y se espera el `controllerchange` de clients.claim().
 */
async function ensureServiceWorker(): Promise<void> {
  if (!("serviceWorker" in navigator)) throw new BundleError("NO_SW", "Este navegador no admite Service Workers (hace falta para abrir el escritorio).");
  await navigator.serviceWorker.register("/bundle-sw.js", { scope: "/" });
  if (navigator.serviceWorker.controller) return;
  await new Promise<void>((resolve, reject) => {
    const t = setTimeout(() => reject(new BundleError("NO_SW", "El Service Worker del portal no tomó la página.")), 10_000);
    navigator.serviceWorker.addEventListener(
      "controllerchange",
      () => {
        clearTimeout(t);
        resolve();
      },
      { once: true },
    );
  });
}

export async function loadNasDesktop(opts: {
  deviceId: string;
  transport: RtcTransport;
  publicKey: string;
  onStage: (s: LoadStage) => void;
}): Promise<{ version: string; mount: (root: HTMLElement) => Promise<void> }> {
  const call = callerOf(opts.transport);
  opts.onStage({ kind: "manifest" });
  if (!opts.transport.isReal?.("bundle.manifest" as never)) throw new BundleError("NO_BUNDLE", "Este NAS todavía no entrega su escritorio por TangoConnect.");
  const r = (await call("bundle.manifest", null)) as BundleManifestResult;
  if (!r?.signature) throw new BundleError("BAD_SIGNATURE", "El escritorio de este NAS no está firmado (build de desarrollo): no lo abrimos.");
  const m = await verifyManifest(r, opts.publicKey);
  if (!m) throw new BundleError("BAD_SIGNATURE", "La firma del escritorio no es válida: no lo abrimos.");
  const shape = validateManifestShape(m);
  if (shape) throw new BundleError("BAD_MANIFEST", `Manifiesto inválido: ${shape}`);

  await ensureServiceWorker();
  const cacheName = `tangoos-bundle-${opts.deviceId}-${m.version}`;
  const cache = await caches.open(cacheName);
  const prefix = bundlePrefix(opts.deviceId);
  const totalBytes = m.files.reduce((a, f) => a + f.size, 0);
  let bytes = 0;
  let done = 0;
  for (const f of m.files) {
    const url = prefix + f.path;
    const cached = await cache.match(url);
    let body: Uint8Array;
    if (cached && cached.headers.get("x-sha256") === f.sha256) {
      body = new Uint8Array(await cached.arrayBuffer());
    } else {
      try {
        body = await opts.transport.fetchBundleFile(f.path);
      } catch (e) {
        throw new BundleError("TRANSFER", `No se pudo bajar ${f.path}: ${(e as Error).message}`);
      }
      if ((await sha256Hex(body)) !== f.sha256) throw new BundleError("BAD_HASH", `${f.path} no coincide con el manifiesto firmado.`);
      await cache.put(url, new Response(body as BodyInit, { headers: { "content-type": contentType(f.path), "x-sha256": f.sha256 } }));
    }
    bytes += body.byteLength;
    done++;
    opts.onStage({ kind: "files", done, total: m.files.length, bytes, totalBytes });
  }
  // Limpia versiones viejas de este NAS.
  for (const k of await caches.keys()) if (k.startsWith(`tangoos-bundle-${opts.deviceId}-`) && k !== cacheName) await caches.delete(k);

  const indexHtml = await (await cache.match(prefix + "index.html"))!.text();
  return {
    version: m.version,
    mount: async (root) => {
      opts.onStage({ kind: "mount" });
      (window as unknown as { __TANGO_TRANSPORT__?: Transport }).__TANGO_TRANSPORT__ = opts.transport;
      const doc = new DOMParser().parseFromString(indexHtml, "text/html");
      const resolve = (href: string) => new URL(href.replace(/^\//, "./"), location.origin + prefix).pathname;
      for (const l of doc.querySelectorAll<HTMLLinkElement>('link[rel="stylesheet"]')) {
        const el = document.createElement("link");
        el.rel = "stylesheet";
        el.href = resolve(l.getAttribute("href") ?? "");
        document.head.appendChild(el);
      }
      const script = doc.querySelector<HTMLScriptElement>('script[type="module"][src]');
      if (!script) throw new BundleError("BAD_MANIFEST", "El index.html del NAS no tiene script de entrada.");
      root.id = "root";
      await import(/* @vite-ignore */ resolve(script.getAttribute("src")!));
    },
  };
}
