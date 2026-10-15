// El root de React del portal. Cuando el escritorio que entrega el NAS toma la página, el portal
// se desmonta y le deja #root vacío (volver al portal recarga la página).
import type { Root } from "react-dom/client";

let root: Root | null = null;

export function setPortalRoot(r: Root) {
  root = r;
}

export function releasePortal() {
  root?.unmount();
  root = null;
  const el = document.getElementById("root");
  if (el) el.replaceChildren();
}
