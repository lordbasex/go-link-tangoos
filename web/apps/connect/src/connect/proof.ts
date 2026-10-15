// Prueba de vínculo "el NAS prueba primero" (protocol/README.md §15, internal/connect/proof.go):
//   key   = SHA-256(utf8(device_token))   (lo único que guarda el NAS)
//   proof = base64url(HMAC-SHA256(key, "tangoconnect link proof\n" + nonce)), sin padding
// El portal pide connect.challenge {linkId, nonce}; solo un NAS que conoce el vínculo puede
// responder, y recién entonces el portal le entrega el token (connect.resume) por DTLS.
const enc = new TextEncoder();
const hex = (b: Uint8Array) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");

export const LINK_PROOF_LABEL = "tangoconnect link proof\n";

export function base64url(b: ArrayBuffer | Uint8Array): string {
  const u = b instanceof Uint8Array ? b : new Uint8Array(b);
  let s = "";
  for (const x of u) s += String.fromCharCode(x);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** SHA-256 del token (la clave del HMAC). */
export async function tokenKey(token: string): Promise<ArrayBuffer> {
  return crypto.subtle.digest("SHA-256", enc.encode(token));
}

export async function linkProof(key: ArrayBuffer, nonce: string): Promise<string> {
  const k = await crypto.subtle.importKey("raw", key, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return base64url(await crypto.subtle.sign("HMAC", k, enc.encode(LINK_PROOF_LABEL + nonce)));
}

export function newNonce(): string {
  return hex(crypto.getRandomValues(new Uint8Array(24)));
}

/** Comparación en tiempo constante. */
export function sameText(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let d = 0;
  for (let i = 0; i < a.length; i++) d |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return d === 0;
}

/** Huella DTLS normalizada: solo los bytes en hex mayúscula ("AB:CD…" o "sha-256 ab:cd…" → "ABCD…"). */
export function normalizeFingerprint(fp: string | null | undefined): string {
  return (fp ?? "").replace(/^sha-256\s+/i, "").replace(/[^0-9a-f]/gi, "").toUpperCase();
}
