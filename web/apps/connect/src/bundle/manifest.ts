// Manifiesto del escritorio que entrega el NAS por el canal `bundle` (protocol/README.md §15,
// internal/connect/bundle.go). tangod devuelve en `signed` el texto JSON EXACTO que firmó el build
// (cmd/tango-bundlesign) con Ed25519; el portal verifica la firma sobre esos bytes y usa la lista de
// archivos de adentro del texto firmado (no la que viene suelta al lado).
// Vector compartido con Go: protocol/vectors/tangoconnect.json.

import { t } from "@tango/client";

export interface BundleFile {
  path: string;
  sha256: string;
  size: number;
}

/** Respuesta de bundle.manifest. */
export interface BundleManifestResult {
  version: string;
  files: BundleFile[];
  signed: string;
  signature: string;
  keyId?: string;
}

/** Lo que se firma (Manifest de internal/connect). */
export interface SignedManifest {
  v: number;
  version: string;
  files: BundleFile[];
}

const enc = new TextEncoder();

function b64(s: string): Uint8Array<ArrayBuffer> {
  const bin = atob(s);
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function hex(b: ArrayBuffer | Uint8Array): string {
  return Array.from(b instanceof Uint8Array ? b : new Uint8Array(b), (x) => x.toString(16).padStart(2, "0")).join("");
}

/** Reglas de forma: rutas relativas sin "..", sha256 hex, tamaños razonables, index.html presente. */
export function validateManifestShape(m: SignedManifest): string | null {
  if (!m || m.v !== 1) return t("versión de manifiesto desconocida");
  if (typeof m.version !== "string" || !m.version || m.version.length > 64) return t("versión inválida");
  if (!Array.isArray(m.files) || m.files.length === 0 || m.files.length > 2000) return t("lista de archivos inválida");
  if (!m.files.some((f) => f.path === "index.html")) return t("falta index.html");
  let total = 0;
  for (const f of m.files) {
    if (typeof f.path !== "string" || !/^[A-Za-z0-9._@\-/]+$/.test(f.path) || f.path.startsWith("/") || f.path.split("/").some((p) => p === ".." || p === "" || p === "."))
      return t("ruta inválida: {path}", { path: String(f.path).slice(0, 80) });
    if (!/^[0-9a-f]{64}$/.test(f.sha256)) return t("sha256 inválido en {path}", { path: f.path });
    if (!Number.isInteger(f.size) || f.size < 0 || f.size > 50 * 1024 * 1024) return t("tamaño inválido en {path}", { path: f.path });
    total += f.size;
  }
  if (total > 200 * 1024 * 1024) return t("el escritorio pesa demasiado");
  return null;
}

/**
 * Verifica la firma Ed25519 sobre `signed` con la clave pública (base64, 32 bytes) y devuelve el
 * manifiesto firmado ya parseado, o null si la firma no vale (o falta: build de desarrollo sin firmar).
 */
export async function verifyManifest(r: BundleManifestResult, publicKeyB64: string): Promise<SignedManifest | null> {
  if (!publicKeyB64 || !r.signature || typeof r.signed !== "string") return null;
  try {
    const key = await crypto.subtle.importKey("raw", b64(publicKeyB64), { name: "Ed25519" }, false, ["verify"]);
    const ok = await crypto.subtle.verify({ name: "Ed25519" }, key, b64(r.signature), enc.encode(r.signed) as Uint8Array<ArrayBuffer>);
    if (!ok) return null;
    return JSON.parse(r.signed) as SignedManifest;
  } catch {
    return null;
  }
}

export async function sha256Hex(data: ArrayBuffer | Uint8Array): Promise<string> {
  return hex(await crypto.subtle.digest("SHA-256", data as BufferSource));
}

export function contentType(path: string): string {
  const ext = path.slice(path.lastIndexOf(".") + 1).toLowerCase();
  const map: Record<string, string> = {
    html: "text/html; charset=utf-8",
    js: "text/javascript; charset=utf-8",
    mjs: "text/javascript; charset=utf-8",
    css: "text/css; charset=utf-8",
    json: "application/json",
    svg: "image/svg+xml",
    png: "image/png",
    webp: "image/webp",
    avif: "image/avif",
    woff2: "font/woff2",
    woff: "font/woff",
    ico: "image/x-icon",
    txt: "text/plain; charset=utf-8",
  };
  return map[ext] ?? "application/octet-stream";
}
