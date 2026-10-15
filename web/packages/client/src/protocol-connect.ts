// TangoConnect (protocol/README.md §15): acceso remoto por signalhub, navegadores
// vinculados, escritorio servido por el NAS (canal bundle) y enlaces públicos. Se suma a
// Methods por module augmentation, como el resto de las fases.

export interface ConnectHello {
  server: string;
  nas_id: string;
  name: string;
  deviceId: string;
  methods: string[];
  topics: string[];
  /** true si esta conexión llegó por signalhub. */
  remote: boolean;
  state?: "paired" | "unlinked" | "linked" | "guest";
}

export interface ConnectStatus {
  enabled: boolean;
  connected: boolean;
  signal: string;
  portal: string;
  deviceId: string;
  fingerprint: string;
  relayOnly: boolean;
  hasTurn: boolean;
  remotes: number;
  lastError?: string;
}

export interface ConnectLink {
  id: string;
  label: string;
  createdAt: string;
  lastSeen?: string;
  ip?: string;
}

export interface ConnectLinkResult {
  linkId: string;
  token: string;
  deviceId: string;
  fingerprint: string;
}

export interface BundleEntry {
  path: string;
  sha256: string;
  size: number;
}

export interface BundleManifest {
  version: string;
  files: BundleEntry[];
  /** Texto exacto que se firmó: la firma se verifica sobre esto. */
  signed: string;
  /** Ed25519 en base64; "" = bundle de desarrollo sin firmar. */
  signature: string;
  keyId?: string;
}

export interface ShareLinkInfo {
  id: string;
  /** `<portal>/s/<deviceId>.<id>#<secreto>`: el secreto va en el fragmento. */
  url: string;
  invite: string;
  code: string;
  owner: string;
  path: string;
  name: string;
  kind: string;
  size: number;
  createdAt: string;
  expiresAt?: string;
  hasPassword: boolean;
  maxDownloads?: number;
  downloads: number;
  expired: boolean;
}

export interface GuestInfo {
  name: string;
  kind: string;
  size: number;
  nas: string;
  expiresAt?: string;
  needsPassword: boolean;
  unlocked: boolean;
  downloadsLeft?: number;
}

declare module "./protocol" {
  interface Methods {
    "connect.hello": { params: void; result: ConnectHello };
    "connect.status": { params: void; result: ConnectStatus };
    "connect.enable": { params: { enabled: boolean; signalURL?: string; portalURL?: string }; result: ConnectStatus };
    "connect.pair.start": { params: void; result: { code: string; expiresAt: string } };
    "connect.links.list": { params: void; result: ConnectLink[] };
    "connect.links.revoke": { params: { id: string }; result: Record<string, never> };
    "connect.link": { params: { label: string }; result: ConnectLinkResult };
    "connect.challenge": { params: { linkId: string; nonce: string }; result: { proof: string } };
    "connect.resume": { params: { linkId: string; token: string }; result: Record<string, never> };
    "bundle.manifest": { params: void; result: BundleManifest };
    "bundle.get": { params: { path: string }; result: { id: number; size: number; sha256: string; channel: string; chunk: number } };
    "shares.links.create": {
      params: { path: string; expiresAt?: string; password?: string; maxDownloads?: number; allowUpload?: boolean };
      result: ShareLinkInfo;
    };
    "shares.links.list": { params: { all?: boolean } | void; result: ShareLinkInfo[] };
    "shares.links.revoke": { params: { id: string }; result: Record<string, never> };
    "guest.open": { params: { link: string; secret: string }; result: GuestInfo };
    "guest.info": { params: void; result: GuestInfo };
    "guest.unlock": { params: { password: string }; result: Record<string, never> };
    "guest.list": { params: { path: string }; result: FileEntry[] };
    "guest.download.begin": {
      params: { path: string; offset?: number; length?: number };
      result: { xfer: number; channel: string; size: number; chunk: number; offset: number; length: number };
    };
  }
  interface Topics {
    "connect.changed": Record<string, never>;
  }
}

/** Separa un enlace público `…/s/<deviceId>.<linkId>#<secreto>`. */
export function parseShareURL(url: string): { deviceId: string; linkId: string; secret: string } | null {
  const m = /\/s\/([0-9a-f-]{36})\.([0-9a-f]{16})#([A-Za-z0-9_-]{16,64})$/.exec(url.trim());
  return m ? { deviceId: m[1], linkId: m[2], secret: m[3] } : null;
}

const enc = new TextEncoder();
const b64url = (b: ArrayBuffer) =>
  btoa(String.fromCharCode(...new Uint8Array(b)))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");

/**
 * Prueba de vínculo que el NAS tiene que mostrar antes de que el portal revele su token
 * (protocol/README.md §15, vector en protocol/vectors/connect-link-proof.json):
 * base64url(HMAC-SHA256(key = SHA-256(token), "tangoconnect link proof\n" + nonce)), sin padding.
 */
export async function connectLinkProof(token: string, nonce: string): Promise<string> {
  const key = await crypto.subtle.digest("SHA-256", enc.encode(token));
  const k = await crypto.subtle.importKey("raw", key, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return b64url(await crypto.subtle.sign("HMAC", k, enc.encode("tangoconnect link proof\n" + nonce)));
}
