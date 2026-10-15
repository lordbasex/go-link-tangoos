// Rutas del portal: / · /demo · /nas · /nas/vincular · /nas/<device_id> · /s/<invite>
import { lazy, Suspense } from "react";
import { usePath } from "./router";
import { Landing } from "./pages/Landing";

const NasList = lazy(() => import("./pages/NasList").then((m) => ({ default: m.NasList })));
const LinkNas = lazy(() => import("./pages/NasList").then((m) => ({ default: m.LinkNas })));
const Remote = lazy(() => import("./pages/Remote").then((m) => ({ default: m.Remote })));
const Share = lazy(() => import("./pages/Share").then((m) => ({ default: m.Share })));
const Demo = lazy(() => import("./pages/Demo").then((m) => ({ default: m.Demo })));

export function route(path: string): { page: "landing" | "demo" | "nas" | "link" | "remote" | "share" | "notfound"; id?: string } {
  const p = path.split("?")[0].replace(/\/+$/, "") || "/";
  if (p === "/") return { page: "landing" };
  if (p === "/demo") return { page: "demo" };
  if (p === "/nas") return { page: "nas" };
  if (p === "/nas/vincular") return { page: "link" };
  let m = /^\/nas\/([0-9a-fA-F-]{36})$/.exec(p);
  if (m) return { page: "remote", id: m[1] };
  m = /^\/s\/([0-9a-fA-F-]{36}\.[0-9a-f]{16})$/.exec(p);
  if (m) return { page: "share", id: m[1] };
  return { page: "notfound" };
}

export function App() {
  const r = route(usePath());
  return (
    <Suspense fallback={<div className="ios" />}>
      {r.page === "landing" && <Landing />}
      {r.page === "demo" && <Demo />}
      {r.page === "nas" && <NasList />}
      {r.page === "link" && <LinkNas />}
      {r.page === "remote" && <Remote key={r.id} deviceId={r.id!} />}
      {r.page === "share" && <Share key={r.id} invite={r.id!} />}
      {r.page === "notfound" && <Landing />}
    </Suspense>
  );
}
