// Service Worker del portal TangoOS Connect (scope "/": tiene que controlar la página para interceptar los
// pedidos del escritorio; solo responde /nas-bundle/, el resto pasa a la red sin tocarlo).
// Sirve el escritorio que entregó el NAS, ya verificado (firma Ed25519 + sha256 por archivo) y
// guardado en Cache Storage por el cargador (src/bundle/loader.ts). Nunca va a la red: si un
// archivo no está en caché, responde 404.
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);
  if (url.origin !== self.location.origin || !url.pathname.startsWith("/nas-bundle/")) return;
  e.respondWith(
    (async () => {
      const id = url.pathname.split("/")[2] || "";
      for (const name of await caches.keys()) {
        if (!name.startsWith(`tangoos-bundle-${id}-`)) continue;
        const cache = await caches.open(name);
        const hit = await cache.match(url.pathname);
        if (hit) {
          const h = new Headers(hit.headers);
          h.delete("x-sha256");
          h.set("cache-control", "no-store");
          h.set("x-content-type-options", "nosniff");
          return new Response(hit.body, { status: 200, headers: h });
        }
      }
      return new Response("No está en el escritorio verificado de este NAS.", { status: 404, headers: { "content-type": "text/plain; charset=utf-8" } });
    })(),
  );
});
