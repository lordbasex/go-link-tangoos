// Service Worker de TangoOS (protocol/README.md §13): sirve /dc/stream/… y /dc/download/…
// pidiéndole los bytes a la página, que los baja por el DataChannel (files.download.begin con
// offset/length). Así un <video> puede adelantar (Range → 206) y una descarga grande va directo
// al gestor de descargas del navegador sin armar un Blob en memoria. No cachea nada.
/* eslint-env serviceworker */
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

const RE = /^\/dc\/(stream|download)\/([A-Za-z0-9_-]{16,64})(?:\/[^/]*)?$/;

self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  if (url.origin !== self.location.origin) return;
  const m = RE.exec(url.pathname);
  if (!m) return;
  event.respondWith(serve(event, m[1], m[2]));
});

/** Parsea "bytes=a-b" (un solo rango). null = sin Range o no soportado. */
function parseRange(h) {
  const m = /^bytes=(\d*)-(\d*)$/.exec(h || "");
  if (!m || (m[1] === "" && m[2] === "")) return null;
  return { start: m[1] === "" ? null : Number(m[1]), end: m[2] === "" ? null : Number(m[2]) };
}

async function pageFor(event) {
  const ids = [event.clientId, event.resultingClientId].filter(Boolean);
  for (const id of ids) {
    const c = await self.clients.get(id);
    if (c && c.type === "window") return [c];
  }
  return self.clients.matchAll({ type: "window" });
}

function ask(client, msg) {
  return new Promise((resolve) => {
    const ch = new MessageChannel();
    const t = setTimeout(() => resolve({ port: null, head: null }), 15000);
    ch.port1.onmessage = (e) => {
      clearTimeout(t);
      ch.port1.onmessage = null;
      resolve({ port: ch.port1, head: e.data });
    };
    client.postMessage(msg, [ch.port2]);
  });
}

async function serve(event, kind, token) {
  const range = kind === "stream" ? parseRange(event.request.headers.get("range")) : null;
  let port = null;
  let head = null;
  for (const c of await pageFor(event)) {
    ({ port, head } = await ask(c, { type: "tango.dc", token, range }));
    if (head && head.ok) break;
    if (port) port.close();
    port = null;
  }
  if (!port || !head || !head.ok) return new Response("Enlace vencido o inválido", { status: head && head.status ? head.status : 404 });

  const { size, start, end, mime, name } = head;
  const stream = new ReadableStream({
    pull(ctl) {
      return new Promise((resolve) => {
        port.onmessage = (e) => {
          const d = e.data;
          if (d.chunk) ctl.enqueue(new Uint8Array(d.chunk));
          if (d.done) {
            ctl.close();
            port.close();
          }
          if (d.error) {
            ctl.error(new Error(d.error));
            port.close();
          }
          resolve();
        };
        port.postMessage({ pull: true });
      });
    },
    cancel() {
      port.postMessage({ cancel: true });
      port.close();
    },
  }, { highWaterMark: 0 });

  const h = new Headers({ "Content-Type": mime || "application/octet-stream", "Accept-Ranges": "bytes", "Cache-Control": "no-store", "Content-Length": String(end - start) });
  if (kind === "download") h.set("Content-Disposition", `attachment; filename*=UTF-8''${encodeURIComponent(name || "archivo")}`);
  if (range) {
    h.set("Content-Range", `bytes ${start}-${Math.max(start, end - 1)}/${size}`);
    return new Response(stream, { status: 206, headers: h });
  }
  return new Response(stream, { status: 200, headers: h });
}
