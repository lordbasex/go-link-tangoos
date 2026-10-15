// Transporte real: señalización por WebSocket + una RTCPeerConnection con los canales
// rpc / events / metrics (protocol/README.md). El NAS siempre ofrece; el navegador responde.
import { verifyHost } from "../hostproof";
import type { Transport, TransportState } from "../transport";
import { RpcError } from "../protocol";
import type { Evt, Method, MetricsFrame, Params, Result, Topic } from "../protocol";
import { getToken, setToken } from "../session";
import { RpcClient } from "./rpc";
import { RtcTransfers } from "./files";
import { RtcTerminals } from "./term";
import { BundleReceiver, sha256Hex } from "./bundle";

export interface Hello {
  type: "hello";
  v: number;
  server: string;
  nas_id: string;
  ice_servers: RTCIceServer[];
  methods: string[];
  topics: string[];
}

type SignalPayload =
  | { kind: "offer"; sdp: string }
  | { kind: "answer"; sdp: string }
  | { kind: "candidate"; candidate: RTCIceCandidateInit | null }
  | { kind: "hello_client"; client: string };

export interface RtcTransportOptions {
  /** URL de señalización. Por defecto `wss://<location.host>/signal?v=1`. */
  url?: string;
  /** "web" | "desk-win" | "desk-linux" */
  client?: string;
  timeoutMs?: number;
  minBackoffMs?: number;
  maxBackoffMs?: number;
  /** Cuánto esperar a que una PC `disconnected` se recupere sola antes de reconectar. */
  disconnectGraceMs?: number;
  autoConnect?: boolean;
  WebSocketImpl?: typeof WebSocket;
  RTCPeerConnectionImpl?: typeof RTCPeerConnection;
  /**
   * Conexión remota por signalhub (TangoConnect, protocol §15): los métodos llegan por
   * `connect.hello` en vez del hello del WebSocket, y `beforeAuth` corre (vínculo del
   * navegador: connect.challenge + connect.resume) antes de reanudar la sesión.
   */
  remote?: {
    beforeAuth?: (call: (method: string, params: unknown) => Promise<unknown>) => Promise<void>;
    onError?: (e: unknown) => void;
  };
  /** Dónde vive el token de sesión (por defecto sessionStorage `tango.session`). */
  tokenStore?: { get(): string | null; set(token: string | null): void };
}

/**
 * Señalización remota (signalhub) vista por el transporte: el portal la implementa con
 * claim/reach y la entrega ya unida a la sesión del NAS.
 */
export interface SignalProvider {
  /** ICE servers (STUN/TURN) del hello de signalhub. */
  iceServers: RTCIceServer[];
  /** Manda un payload de señalización (answer/candidate) al NAS. */
  send(payload: unknown): void;
  onPayload(fn: (payload: unknown) => void): () => void;
  onClose(fn: () => void): () => void;
  close(): void;
}

/**
 * Transporte remoto sobre signalhub. `join` se llama en cada (re)conexión y devuelve la
 * señalización ya unida a la sesión del NAS (claim la primera vez, reach después).
 */
export function rtcFromSignal(join: () => Promise<SignalProvider>, opts: Omit<RtcTransportOptions, "url" | "WebSocketImpl"> = {}): RtcTransport {
  // Un "WebSocket" que habla con signalhub: emite un hello sintético con los ICE servers
  // y traduce los mensajes {type:"signal", payload} a payloads de signalhub.
  class SignalSocket {
    readyState = 0;
    onmessage: ((e: { data: string }) => void) | null = null;
    onclose: (() => void) | null = null;
    private provider: SignalProvider | null = null;
    private closed = false;
    constructor() {
      join().then(
        (p) => {
          if (this.closed) return p.close();
          this.provider = p;
          this.readyState = 1;
          p.onPayload((payload) => this.onmessage?.({ data: JSON.stringify({ type: "signal", payload }) }));
          p.onClose(() => this.close());
          this.onmessage?.({ data: JSON.stringify({ type: "hello", v: 1, server: "", nas_id: "", ice_servers: p.iceServers, methods: [], topics: [] }) });
        },
        () => this.close(),
      );
    }
    send(raw: string) {
      const m = JSON.parse(raw) as { type: string; payload?: { kind?: string } };
      if (m.type === "signal" && m.payload && m.payload.kind !== "hello_client") this.provider?.send(m.payload);
    }
    close() {
      if (this.closed) return;
      this.closed = true;
      this.readyState = 3;
      this.provider?.close();
      this.onclose?.();
    }
  }
  return new RtcTransport({ ...opts, url: "signalhub:", WebSocketImpl: SignalSocket as unknown as typeof WebSocket, remote: opts.remote ?? {} });
}

export function defaultSignalURL(): string {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  return `${proto}://${location.host}/signal?v=1`;
}

export class RtcTransport implements Transport {
  readonly kind = "rtc" as const;
  readonly rpc: RpcClient;
  readonly transfers: RtcTransfers;
  readonly terminals: RtcTerminals;
  hello: Hello | null = null;

  private readonly o: Required<Omit<RtcTransportOptions, "WebSocketImpl" | "RTCPeerConnectionImpl" | "url" | "remote" | "tokenStore">> & {
    url: string;
    remote?: RtcTransportOptions["remote"];
    tokens: { get(): string | null; set(token: string | null): void };
    WS: typeof WebSocket;
    PC: typeof RTCPeerConnection;
  };
  private st: TransportState = "connecting";
  private stopped = true;
  private ws: WebSocket | null = null;
  private pc: RTCPeerConnection | null = null;
  private gen = 0; // invalida callbacks de conexiones viejas
  private queued: RTCIceCandidateInit[] = [];
  private attempt = 0;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private graceTimer: ReturnType<typeof setTimeout> | null = null;
  private methods = new Set<string>();
  private topics = new Set<string>();
  private subs = new Map<string, Set<(e: Evt<any>) => void>>();
  private stateSubs = new Set<(s: TransportState) => void>();
  private capSubs = new Set<() => void>();
  private authSubs = new Set<() => void>();
  private lastSeq = 0;
  private metricsSeq = 0;
  private fileChannels = new Map<string, RTCDataChannel>();
  private termChannel: RTCDataChannel | null = null;
  private bundleRx = new BundleReceiver();

  constructor(opts: RtcTransportOptions = {}) {
    this.o = {
      url: opts.url ?? defaultSignalURL(),
      client: opts.client ?? "web",
      timeoutMs: opts.timeoutMs ?? 15_000,
      minBackoffMs: opts.minBackoffMs ?? 500,
      maxBackoffMs: opts.maxBackoffMs ?? 15_000,
      disconnectGraceMs: opts.disconnectGraceMs ?? 3_000,
      autoConnect: opts.autoConnect ?? true,
      WS: opts.WebSocketImpl ?? globalThis.WebSocket,
      PC: opts.RTCPeerConnectionImpl ?? globalThis.RTCPeerConnection,
      remote: opts.remote,
      tokens: opts.tokenStore ?? { get: getToken, set: setToken },
    };
    this.rpc = new RpcClient(this.o.timeoutMs);
    this.transfers = new RtcTransfers({
      call: (m, p, o) => this.call(m, p, o),
      channel: (label) => this.fileChannels.get(label),
      subscribe: (topic, fn) => this.subscribe(topic, fn),
    });
    this.terminals = new RtcTerminals({
      call: (m, p) => this.call(m, p),
      channel: () => this.termChannel ?? undefined,
    });
    if (this.o.autoConnect) this.connect();
  }

  // ---- Transport ------------------------------------------------------------------

  async call<M extends Method>(method: M, params: Params<M>, opts: { signal?: AbortSignal } = {}): Promise<Result<M>> {
    if (this.st !== "connected" || !this.rpc.ready) await this.waitConnected(opts.signal);
    return (await this.rpc.call(method, params, opts)) as Result<M>;
  }

  subscribe<T extends Topic>(topic: T, fn: (e: Evt<T>) => void): () => void {
    let set = this.subs.get(topic);
    if (!set) this.subs.set(topic, (set = new Set()));
    set.add(fn as (e: Evt<any>) => void);
    return () => set!.delete(fn as (e: Evt<any>) => void);
  }

  state() {
    return this.st;
  }

  onState(fn: (s: TransportState) => void) {
    this.stateSubs.add(fn);
    return () => this.stateSubs.delete(fn);
  }

  isReal(method: Method) {
    return this.methods.has(method);
  }

  hasTopic(topic: string) {
    return this.topics.has(topic);
  }

  onCapabilities(fn: () => void) {
    this.capSubs.add(fn);
    return () => this.capSubs.delete(fn);
  }

  onAuthLost(fn: () => void) {
    this.authSubs.add(fn);
    return () => this.authSubs.delete(fn);
  }

  /**
   * Baja un archivo del escritorio servido por el NAS (canal bundle) y verifica su
   * sha256. Solo en conexiones remotas vinculadas (protocol §15).
   */
  async fetchBundleFile(path: string): Promise<Uint8Array<ArrayBuffer>> {
    const r = (await this.call("bundle.get", { path })) as { id: number; size: number; sha256: string };
    const data = await this.bundleRx.expect(r.id, r.size);
    if ((await sha256Hex(data)) !== r.sha256) throw new RpcError("BUNDLE_CORRUPT", `El archivo ${path} llegó corrupto`);
    return data;
  }

  // ---- Ciclo de vida ----------------------------------------------------------------

  /** Conecta y reconecta hasta close(). */
  connect() {
    if (!this.stopped) return;
    this.stopped = false;
    this.open();
  }

  close() {
    this.stopped = true;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.retryTimer = null;
    this.teardown();
    this.setState("closed");
  }

  private setState(s: TransportState) {
    if (this.st === s) return;
    this.st = s;
    this.stateSubs.forEach((f) => f(s));
  }

  private open() {
    const gen = ++this.gen;
    this.setState(this.attempt === 0 && !this.hello ? "connecting" : "reconnecting");
    let ws: WebSocket;
    try {
      ws = new this.o.WS(this.o.url);
    } catch {
      return this.scheduleReconnect();
    }
    this.ws = ws;
    ws.onmessage = (e) => gen === this.gen && this.onSignal(String(e.data));
    ws.onclose = () => {
      if (gen !== this.gen) return;
      this.ws = null;
      // Con la PC arriba el WS puede cerrarse sin problema (protocol §1).
      if (!this.rpc.ready) this.reconnect();
    };
  }

  private send(payload: SignalPayload) {
    if (this.ws?.readyState !== 1) return false;
    this.ws.send(JSON.stringify({ type: "signal", payload }));
    return true;
  }

  private onSignal(raw: string) {
    let msg: { type: string; payload?: SignalPayload; code?: string; message?: string } & Partial<Hello>;
    try {
      msg = JSON.parse(raw);
    } catch {
      return;
    }
    if (msg.type === "hello") {
      this.hello = msg as Hello;
      this.methods = new Set(this.hello.methods ?? []);
      this.topics = new Set(this.hello.topics ?? []);
      this.capSubs.forEach((f) => f());
      this.send({ kind: "hello_client", client: this.o.client });
    } else if (msg.type === "signal" && msg.payload) {
      void this.onPayload(msg.payload).catch((e) => {
        console.warn("[tango] señalización", e);
        this.reconnect();
      });
    } else if (msg.type === "error") {
      console.warn(`[tango] señalización: ${msg.code}: ${msg.message}`);
    }
  }

  private async onPayload(p: SignalPayload) {
    const gen = this.gen;
    if (p.kind === "offer") {
      const pc = this.pc ?? this.createPC();
      await pc.setRemoteDescription({ type: "offer", sdp: p.sdp });
      for (const c of this.queued.splice(0)) await pc.addIceCandidate(c).catch(() => {});
      const answer = await pc.createAnswer();
      if (gen !== this.gen) return;
      await pc.setLocalDescription(answer);
      this.send({ kind: "answer", sdp: answer.sdp ?? "" });
    } else if (p.kind === "candidate") {
      if (!p.candidate) return; // fin de candidatos: nada que hacer
      if (!this.pc?.remoteDescription) this.queued.push(p.candidate);
      else await this.pc.addIceCandidate(p.candidate).catch(() => {});
    }
  }

  private createPC(): RTCPeerConnection {
    const gen = this.gen;
    const pc = new this.o.PC({ iceServers: this.hello?.ice_servers ?? [] });
    this.pc = pc;
    pc.onicecandidate = (e) => gen === this.gen && this.send({ kind: "candidate", candidate: e.candidate ? e.candidate.toJSON() : null });
    pc.ondatachannel = (e) => gen === this.gen && this.bindChannel(e.channel, gen);
    pc.onconnectionstatechange = () => {
      if (gen !== this.gen) return;
      const s = pc.connectionState;
      if (s === "connected") {
        if (this.graceTimer) clearTimeout(this.graceTimer);
        this.graceTimer = null;
        if (this.rpc.ready) this.setState("connected");
      } else if (s === "disconnected") {
        // Puede volver sola (cambio de Wi-Fi); si no, reconexión completa.
        this.setState("reconnecting");
        this.graceTimer ??= setTimeout(() => this.reconnect(), this.o.disconnectGraceMs);
      } else if (s === "failed") {
        this.reconnect();
      }
    };
    return pc;
  }

  private bindChannel(ch: RTCDataChannel, gen: number) {
    if (ch.label === "rpc") {
      ch.onmessage = (e) => this.rpc.handle(String(e.data));
      // Una sola vez por canal: si llega ya abierto (ondatachannel) y además dispara `open`, el paso de
      // vínculo remoto (connect.link / guest.open) correría dos veces y el NAS rechazaría el segundo.
      let opened = false;
      const open = () => {
        if (opened || gen !== this.gen) return;
        opened = true;
        void this.onRpcOpen(ch, gen);
      };
      ch.onopen = open;
      ch.onclose = () => gen === this.gen && this.reconnect();
      if (ch.readyState === "open") open();
    } else if (ch.label === "events") {
      ch.onmessage = (e) => {
        let m: { type?: string; topic?: string; seq?: number; data?: unknown };
        try {
          m = JSON.parse(String(e.data));
        } catch {
          return;
        }
        if (m.type !== "evt" || !m.topic) return;
        const seq = m.seq ?? 0;
        if (seq && seq <= this.lastSeq) return; // duplicado
        if (seq && this.lastSeq && seq > this.lastSeq + 1) console.debug(`[tango] eventos perdidos: ${this.lastSeq + 1}…${seq - 1}`);
        if (seq) this.lastSeq = seq;
        this.dispatch(m.topic, m.data, seq);
      };
    } else if (ch.label.startsWith("files.")) {
      // Canales binarios de transferencias (protocol §10).
      ch.binaryType = "arraybuffer";
      this.fileChannels.set(ch.label, ch);
      ch.onmessage = (e) => {
        if (gen === this.gen && e.data instanceof ArrayBuffer) this.transfers.onFrame(e.data);
      };
      ch.onclose = () => {
        if (this.fileChannels.get(ch.label) === ch) this.fileChannels.delete(ch.label);
      };
    } else if (ch.label === "term") {
      // Terminales: frames binarios [u32 id][datos] (protocol §12).
      ch.binaryType = "arraybuffer";
      this.termChannel = ch;
      ch.onmessage = (e) => {
        if (gen === this.gen && e.data instanceof ArrayBuffer) this.terminals.onFrame(e.data);
      };
      ch.onclose = () => {
        if (this.termChannel === ch) this.termChannel = null;
      };
    } else if (ch.label === "bundle") {
      // Escritorio servido por el NAS (protocol §15).
      ch.binaryType = "arraybuffer";
      ch.onmessage = (e) => {
        if (gen === this.gen && e.data instanceof ArrayBuffer) this.bundleRx.onFrame(e.data);
      };
    } else if (ch.label === "metrics") {
      ch.onmessage = (e) => {
        try {
          this.dispatch("metrics", JSON.parse(String(e.data)) as MetricsFrame, ++this.metricsSeq);
        } catch {
          /* frame corrupto: se descarta, llega otro en 250 ms */
        }
      };
    }
  }

  private async onRpcOpen(ch: RTCDataChannel, gen: number) {
    this.rpc.bind(ch);
    this.attempt = 0;
    this.lastSeq = 0; // la seq es por conexión (docs/03 §6)
    if (this.o.remote) {
      // Remota: no hubo hello de WebSocket. Métodos y topics por connect.hello, y el
      // vínculo del navegador antes de reanudar la sesión.
      try {
        const h = (await this.rpc.call("connect.hello", null)) as { methods?: string[]; topics?: string[] };
        this.methods = new Set(h.methods ?? []);
        this.topics = new Set(h.topics ?? []);
        this.capSubs.forEach((f) => f());
        await this.o.remote.beforeAuth?.((m, p) => this.rpc.call(m, p));
      } catch (e) {
        if (gen !== this.gen) return;
        this.o.remote.onError?.(e);
        this.close();
        return;
      }
    }
    // Reconexión: la sesión del servidor es nueva, se reanuda con el token guardado.
    const token = this.o.tokens.get();
    if (token) {
      try {
        await verifyHost((m, p) => this.rpc.call(m, p) as Promise<{ proof: string }>, token, this.methods.has("auth.challenge"));
        await this.rpc.call("auth.resume", { token });
      } catch (e) {
        if (gen !== this.gen) return;
        if (e instanceof RpcError && e.code !== "DISCONNECTED" && e.code !== "TIMEOUT") {
          this.o.tokens.set(null);
          this.authSubs.forEach((f) => f());
        }
      }
    }
    if (gen === this.gen) this.setState("connected");
  }

  private dispatch(topic: string, data: unknown, seq: number) {
    const e = { topic, seq, data } as Evt<any>;
    this.subs.get(topic)?.forEach((f) => f(e));
  }

  private waitConnected(signal?: AbortSignal): Promise<void> {
    if (this.st === "connected" && this.rpc.ready) return Promise.resolve();
    return new Promise((resolve, reject) => {
      const done = (err?: Error) => {
        clearTimeout(timer);
        off();
        signal?.removeEventListener("abort", onAbort);
        if (err) reject(err);
        else resolve();
      };
      const timer = setTimeout(() => done(new RpcError("DISCONNECTED", "Sin conexión con el NAS")), this.o.timeoutMs);
      const off = this.onState((s) => {
        if (s === "connected") done();
        else if (s === "closed") done(new RpcError("DISCONNECTED", "Conexión cerrada"));
      });
      const onAbort = () => done(new RpcError("CANCELLED", "Cancelado"));
      signal?.addEventListener("abort", onAbort, { once: true });
    });
  }

  private reconnect() {
    if (this.stopped) return;
    this.teardown();
    this.scheduleReconnect();
  }

  private scheduleReconnect() {
    if (this.stopped || this.retryTimer) return;
    this.setState("reconnecting");
    const base = Math.min(this.o.maxBackoffMs, this.o.minBackoffMs * 2 ** this.attempt);
    this.attempt++;
    const delay = base * (0.75 + Math.random() * 0.5);
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      this.open();
    }, delay);
  }

  private teardown() {
    this.gen++;
    if (this.graceTimer) clearTimeout(this.graceTimer);
    this.graceTimer = null;
    this.rpc.bind(null);
    this.rpc.failAll();
    this.fileChannels.clear();
    this.transfers.failAll();
    this.termChannel = null;
    this.terminals.failAll();
    this.bundleRx.failAll();
    this.queued = [];
    const pc = this.pc;
    this.pc = null;
    try {
      pc?.close();
    } catch {
      /* ya cerrada */
    }
    const ws = this.ws;
    this.ws = null;
    try {
      ws?.close();
    } catch {
      /* ya cerrado */
    }
  }
}
