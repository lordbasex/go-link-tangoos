// Huella del certificado de un NAS (la misma que muestra TangoOS en Ajustes: SHA-256 del DER).
// Los NAS usan un certificado propio: la primera conexión muestra la huella y la app la fija.
import { createHash } from "node:crypto";
import tls from "node:tls";

export function fingerprintOfDER(der: Buffer): string {
  const hex = createHash("sha256").update(der).digest("hex").toUpperCase();
  return "sha-256 " + (hex.match(/../g) ?? []).join(":");
}

/** PEM → DER (Electron entrega el certificado en PEM). */
export function derOfPEM(pem: string): Buffer {
  const b64 = pem.replace(/-----(BEGIN|END) CERTIFICATE-----/g, "").replace(/\s+/g, "");
  return Buffer.from(b64, "base64");
}

export interface Probe {
  host: string;
  port: number;
  fingerprint: string;
  subject: string;
}

/** Se conecta por TLS sin validar (todavía no hay nada que validar) y devuelve la huella. */
export function probe(host: string, port = 443, timeoutMs = 8000): Promise<Probe> {
  return new Promise((resolve, reject) => {
    const sock = tls.connect({ host, port, servername: /^[\d.]+$|:/.test(host) ? undefined : host, rejectUnauthorized: false, timeout: timeoutMs }, () => {
      const cert = sock.getPeerCertificate();
      sock.end();
      if (!cert?.raw) return reject(new Error("el NAS no presentó un certificado"));
      resolve({ host, port, fingerprint: fingerprintOfDER(cert.raw), subject: String(cert.subject?.CN ?? "") });
    });
    sock.on("timeout", () => {
      sock.destroy();
      reject(new Error("el NAS no respondió a tiempo"));
    });
    sock.on("error", (e) => reject(e));
  });
}

/** host:puerto escrito por la persona → partes validadas (sin esquema ni ruta). */
export function parseAddress(input: string): { host: string; port: number } {
  let s = input.trim().replace(/^https?:\/\//i, "").replace(/\/.*$/, "");
  if (!s || s.length > 255 || /[\s@?#]/.test(s)) throw new Error("dirección inválida");
  let port = 443;
  const v6 = /^\[([0-9a-f:]+)\](?::(\d+))?$/i.exec(s);
  if (v6) {
    if (v6[2]) port = Number(v6[2]);
    s = v6[1];
  } else {
    const m = /^([^:]+):(\d+)$/.exec(s);
    if (m) {
      s = m[1];
      port = Number(m[2]);
    }
  }
  if (!(port > 0 && port < 65536)) throw new Error("puerto inválido");
  if (!/^[a-z0-9.-]+$/i.test(s) && !/^[0-9a-f:]+$/i.test(s)) throw new Error("dirección inválida");
  return { host: s.toLowerCase(), port };
}
