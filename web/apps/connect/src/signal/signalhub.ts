// Cliente mínimo de signalhub (protocolo v1, github.com/lordbasex/signalhub). Mismas reglas que el
// cliente de go-link: un sobre JSON por mensaje, respuestas en orden (cola FIFO), errores con textos fijos.
// A diferencia de go-link, cada SignalhubLink es UNA conexión de un solo uso: si se cae, el que la usa
// (SignalhubSocket) abre otra, y el RtcTransport de TangoOS se encarga del backoff.

export type MessageType =
  | "hello"
  | "claim"
  | "reach"
  | "join"
  | "paired"
  | "joined"
  | "signal"
  | "peer_left"
  | "error";

export interface ICEServer {
  urls: string[];
  username?: string;
  credential?: string;
}

export interface Envelope {
  type: MessageType | string;
  app?: string;
  device_id?: string;
  code?: string;
  invite?: string;
  session_id?: string;
  peer_id?: string;
  remote?: string;
  room_id?: string;
  to?: string;
  from?: string;
  ice_servers?: unknown;
  payload?: unknown;
  error?: string;
}

/** Error de signalhub (texto fijo del servidor) o local (timeout, desconexión). */
export class SignalError extends Error {
  constructor(
    message: string,
    readonly fromServer: boolean,
  ) {
    super(message);
    this.name = "SignalError";
  }
}

/** Agrega ?v=1 si la URL no fija versión. */
export function endpoint(url: string): string {
  const u = new URL(url);
  if (!u.searchParams.has("v")) u.searchParams.set("v", "1");
  return u.toString();
}

/** STUN/TURN del hello, validados: como mucho 10, URLs stun:/turn:/turns:, credenciales string. */
export function parseICEServers(v: unknown): ICEServer[] {
  if (!Array.isArray(v)) return [];
  return v.slice(0, 10).flatMap((s): ICEServer[] => {
    if (typeof s !== "object" || s === null) return [];
    const o = s as Record<string, unknown>;
    const list = typeof o.urls === "string" ? [o.urls] : Array.isArray(o.urls) ? o.urls : [];
    const urls = list.filter((u): u is string => typeof u === "string" && /^(stun|turns?):/i.test(u) && u.length <= 256).slice(0, 10);
    if (!urls.length) return [];
    const out: ICEServer = { urls };
    if (typeof o.username === "string") out.username = o.username.slice(0, 256);
    if (typeof o.credential === "string") out.credential = o.credential.slice(0, 256);
    return [out];
  });
}

/** Traducción de los errores fijos de signalhub a castellano para la UI. */
export function humanSignalError(e: unknown): string {
  const msg = e instanceof Error ? e.message : String(e);
  const map: Record<string, string> = {
    "invalid or expired code": "El código no existe o venció. Generá uno nuevo en Ajustes → TangoConnect.",
    "device not connected": "Tu NAS no está conectado a TangoConnect ahora. Revisá que esté encendido y con internet.",
    "invalid or expired invite": "Este enlace ya no existe: venció, lo revocaron o el NAS está apagado.",
    "rate limit exceeded": "Demasiados intentos seguidos. Esperá un minuto y probá de nuevo.",
    "app not allowed": "El servidor de señalización todavía no acepta TangoOS (falta «tangoos» en ALLOWED_APPS).",
    "signaling server unreachable": "No pudimos llegar al servidor de señalización. Revisá tu conexión.",
    "request timed out": "El servidor de señalización no respondió a tiempo.",
    disconnected: "Se cortó la conexión con el servidor de señalización.",
  };
  return map[msg] ?? msg;
}

interface Pending {
  expect: string[];
  resolve: (e: Envelope) => void;
  reject: (e: SignalError) => void;
  timer: ReturnType<typeof setTimeout>;
}

export interface LinkOptions {
  url: string;
  timeoutMs?: number;
  WebSocketImpl?: typeof WebSocket;
}

/** Una conexión a signalhub. */
export class SignalhubLink {
  peerId = "";
  iceServers: ICEServer[] = [];
  private ws: WebSocket | null = null;
  private pending: Pending[] = [];
  private listeners = new Set<(e: Envelope) => void>();
  private closeListeners = new Set<() => void>();
  private helloWaiters: { resolve: () => void; reject: (e: SignalError) => void }[] = [];
  private closed = false;
  private readonly timeoutMs: number;

  constructor(private readonly opts: LinkOptions) {
    this.timeoutMs = opts.timeoutMs ?? 10_000;
  }

  /** Abre el WebSocket y espera el hello. */
  open(): Promise<void> {
    const WS = this.opts.WebSocketImpl ?? globalThis.WebSocket;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.close();
        reject(new SignalError("signaling server unreachable", false));
      }, this.timeoutMs);
      this.helloWaiters.push({
        resolve: () => {
          clearTimeout(timer);
          resolve();
        },
        reject: (e) => {
          clearTimeout(timer);
          reject(e);
        },
      });
      let ws: WebSocket;
      try {
        ws = new WS(endpoint(this.opts.url));
      } catch {
        clearTimeout(timer);
        reject(new SignalError("signaling server unreachable", false));
        return;
      }
      this.ws = ws;
      ws.onmessage = (ev) => this.handle(ev.data);
      ws.onclose = () => this.down();
      ws.onerror = () => {
        /* onclose llega después */
      };
    });
  }

  send(env: Envelope): boolean {
    if (!this.ws || this.ws.readyState !== 1) return false;
    this.ws.send(JSON.stringify(env));
    return true;
  }

  /** Envía y resuelve con la próxima respuesta de un tipo de `expect` (un "error" rechaza). */
  request(env: Envelope, expect: string[]): Promise<Envelope> {
    return new Promise((resolve, reject) => {
      const entry: Pending = {
        expect,
        resolve,
        reject,
        timer: setTimeout(() => {
          this.pending = this.pending.filter((p) => p !== entry);
          reject(new SignalError("request timed out", false));
        }, this.timeoutMs),
      };
      this.pending.push(entry);
      if (!this.send(env)) {
        clearTimeout(entry.timer);
        this.pending = this.pending.filter((p) => p !== entry);
        reject(new SignalError("disconnected", false));
      }
    });
  }

  onMessage(fn: (e: Envelope) => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  onClose(fn: () => void): () => void {
    this.closeListeners.add(fn);
    return () => this.closeListeners.delete(fn);
  }

  close() {
    if (this.closed) return;
    try {
      this.ws?.close();
    } catch {
      /* ya cerrado */
    }
    this.down();
  }

  private handle(data: unknown) {
    if (typeof data !== "string") return;
    let env: Envelope;
    try {
      const v: unknown = JSON.parse(data);
      if (typeof v !== "object" || v === null || Array.isArray(v)) return;
      env = v as Envelope;
    } catch {
      return;
    }
    if (env.type === "hello") {
      this.peerId = typeof env.peer_id === "string" ? env.peer_id.slice(0, 64) : "";
      this.iceServers = parseICEServers(env.ice_servers);
      this.helloWaiters.splice(0).forEach((w) => w.resolve());
      return;
    }
    const head = this.pending[0];
    if (head && (env.type === "error" || head.expect.includes(env.type))) {
      this.pending.shift();
      clearTimeout(head.timer);
      if (env.type === "error") head.reject(new SignalError(env.error ?? "unknown error", true));
      else head.resolve(env);
    }
    for (const fn of [...this.listeners]) fn(env);
  }

  private down() {
    if (this.closed) return;
    this.closed = true;
    this.ws = null;
    this.helloWaiters.splice(0).forEach((w) => w.reject(new SignalError("signaling server unreachable", false)));
    for (const p of this.pending.splice(0)) {
      clearTimeout(p.timer);
      p.reject(new SignalError("disconnected", false));
    }
    for (const fn of [...this.closeListeners]) fn();
  }
}
