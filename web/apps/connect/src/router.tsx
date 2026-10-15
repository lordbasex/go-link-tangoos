// Router mínimo con la History API (el portal tiene 5 rutas; no hace falta react-router).
import { useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent } from "react";

const listeners = new Set<() => void>();
const notify = () => listeners.forEach((f) => f());
if (typeof window !== "undefined") window.addEventListener("popstate", notify);

export function navigate(to: string, opts: { replace?: boolean } = {}) {
  if (opts.replace) history.replaceState(null, "", to);
  else history.pushState(null, "", to);
  window.scrollTo(0, 0);
  notify();
}

export function usePath(): string {
  return useSyncExternalStore(
    (f) => {
      listeners.add(f);
      return () => listeners.delete(f);
    },
    () => location.pathname + location.search,
    () => "/",
  );
}

/** Enlace interno: navega sin recargar (con Ctrl/⌘ abre pestaña nueva como siempre). */
export function Link({ to, onClick, ...rest }: AnchorHTMLAttributes<HTMLAnchorElement> & { to: string }) {
  const click = (e: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(e);
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const url = new URL(to, location.href);
    if (url.origin !== location.origin) return;
    if (url.pathname === location.pathname && url.hash) return; // ancla en la misma página
    e.preventDefault();
    navigate(url.pathname + url.search + url.hash);
    if (url.hash) document.getElementById(url.hash.slice(1))?.scrollIntoView();
  };
  return <a href={to} onClick={click} {...rest} />;
}
