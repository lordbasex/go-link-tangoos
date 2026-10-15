// Resume "el host prueba primero" (protocol/README.md §12, docs/07 §2): antes de revelar
// el token guardado, el cliente le pide al NAS que demuestre que lo conoce. Un NAS falso
// en la red no puede calcular la prueba y el token nunca le llega.
//
//   h1      = SHA-256(token)                      (lo que el NAS guarda)
//   tokenId = hex(SHA-256(h1))[:32]              (identifica el token sin revelarlo)
//   proof   = hex(HMAC-SHA256(h1, "tango resume\n" + nonce))
import { RpcError } from "./protocol";

const enc = new TextEncoder();
const hex = (b: ArrayBuffer) => Array.from(new Uint8Array(b), (x) => x.toString(16).padStart(2, "0")).join("");

export async function tokenIdOf(token: string): Promise<{ id: string; h1: ArrayBuffer }> {
  const h1 = await crypto.subtle.digest("SHA-256", enc.encode(token));
  const id = hex(await crypto.subtle.digest("SHA-256", h1)).slice(0, 32);
  return { id, h1 };
}

export async function expectedProof(h1: ArrayBuffer, nonce: string): Promise<string> {
  const key = await crypto.subtle.importKey("raw", h1, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return hex(await crypto.subtle.sign("HMAC", key, enc.encode("tango resume\n" + nonce)));
}

type Call = (method: "auth.challenge", params: { tokenId: string; nonce: string }) => Promise<{ proof: string }>;

/**
 * Verifica al NAS. Devuelve sin hacer nada si el servidor no implementa el desafío
 * (versiones anteriores) o si no hay WebCrypto (contexto no seguro): en ese caso el
 * resume clásico sigue funcionando.
 */
export async function verifyHost(call: Call, token: string, supported: boolean): Promise<void> {
  if (!supported || typeof crypto === "undefined" || !crypto.subtle) return;
  const { id, h1 } = await tokenIdOf(token);
  const nonce = hex(crypto.getRandomValues(new Uint8Array(24)).buffer);
  const { proof } = await call("auth.challenge", { tokenId: id, nonce });
  if (proof !== (await expectedProof(h1, nonce))) {
    throw new RpcError("HOST_NOT_VERIFIED", "El NAS no pudo probar que es el mismo de siempre: no se envió tu sesión.");
  }
}
