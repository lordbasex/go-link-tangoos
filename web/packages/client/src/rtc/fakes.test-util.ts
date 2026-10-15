// Dobles de prueba para WebSocket, RTCPeerConnection y RTCDataChannel (solo tests).

export class FakeDC {
  readyState = "connecting";
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  constructor(readonly label: string) {}
  send(d: string) {
    this.sent.push(d);
  }
  open() {
    this.readyState = "open";
    this.onopen?.();
  }
  recv(msg: unknown) {
    this.onmessage?.({ data: typeof msg === "string" ? msg : JSON.stringify(msg) });
  }
  lastSent() {
    return JSON.parse(this.sent[this.sent.length - 1]);
  }
}

export class FakeWS {
  static all: FakeWS[] = [];
  static get last() {
    return FakeWS.all[FakeWS.all.length - 1];
  }
  readyState = 0;
  sent: unknown[] = [];
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(readonly url: string) {
    FakeWS.all.push(this);
  }
  open() {
    this.readyState = 1;
  }
  send(d: string) {
    this.sent.push(JSON.parse(d));
  }
  recv(msg: unknown) {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }
  close() {
    this.readyState = 3;
    this.onclose?.();
  }
}

export class FakePC {
  static all: FakePC[] = [];
  static get last() {
    return FakePC.all[FakePC.all.length - 1];
  }
  remoteDescription: { type: string; sdp: string } | null = null;
  local: unknown = null;
  added: unknown[] = [];
  connectionState = "new";
  closed = false;
  onicecandidate: ((e: { candidate: null | { toJSON(): unknown } }) => void) | null = null;
  ondatachannel: ((e: { channel: FakeDC }) => void) | null = null;
  onconnectionstatechange: (() => void) | null = null;
  constructor(readonly config: unknown) {
    FakePC.all.push(this);
  }
  async setRemoteDescription(d: { type: string; sdp: string }) {
    this.remoteDescription = d;
  }
  async createAnswer() {
    return { type: "answer", sdp: "ANSWER-SDP" };
  }
  async setLocalDescription(d: unknown) {
    this.local = d;
  }
  async addIceCandidate(c: unknown) {
    this.added.push(c);
  }
  close() {
    this.closed = true;
  }
  channel(label: string) {
    const dc = new FakeDC(label);
    this.ondatachannel?.({ channel: dc });
    return dc;
  }
  setState(s: string) {
    this.connectionState = s;
    this.onconnectionstatechange?.();
  }
}

// setImmediate: sigue funcionando con los timers falsos de vitest (que solo reemplazan setTimeout).
export const flush = () => new Promise((r) => setImmediate(r));
