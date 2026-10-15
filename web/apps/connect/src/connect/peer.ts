// RTCPeerConnection observada: el RtcTransport la maneja igual que siempre; el portal solo mira
// la huella DTLS que presentó el NAS y engancha los canales files.* para las descargas de enlaces
// públicos (guest.download.begin).

/** Huella DTLS del NAS según su SDP ("AB:CD:…"), o null. */
export function remoteFingerprint(pc: RTCPeerConnection | null): string | null {
  const sdp = pc?.remoteDescription?.sdp ?? "";
  const m = /a=fingerprint:\S+\s+([0-9A-F:]+)/i.exec(sdp);
  return m ? m[1].toUpperCase() : null;
}

export interface PeerHooks {
  onPeer?: (pc: RTCPeerConnection) => void;
  onChannel?: (ch: RTCDataChannel) => void;
}

/** Clase para `RTCPeerConnectionImpl`. Se crea al llamarla (en Node no hay WebRTC). */
export function observedPeerConnection(h: PeerHooks, Base: typeof RTCPeerConnection = globalThis.RTCPeerConnection): typeof RTCPeerConnection {
  class Observed extends Base {
    constructor(cfg?: RTCConfiguration) {
      super(cfg);
      h.onPeer?.(this);
      this.addEventListener("datachannel", (e: RTCDataChannelEvent) => h.onChannel?.(e.channel));
    }
  }
  return Observed;
}
