// Configuración del portal (docs/16-tangoconnect.md).
export const APP = "tangoos"; // namespace en signalhub (ALLOWED_APPS)

export const SIGNAL_URL: string = import.meta.env.VITE_SIGNAL_URL || "wss://signal.go-link.org/ws?v=1";

/** Clave pública Ed25519 (base64) de releases: firma el manifiesto del escritorio que entrega el NAS. */
export const BUNDLE_PUBKEY: string = import.meta.env.VITE_BUNDLE_PUBKEY || "";

export const PORTAL_ORIGIN = "https://tangoos.go-link.org";

export const shareURL = (invite: string) => `${PORTAL_ORIGIN}/s/${encodeURIComponent(invite)}`;
