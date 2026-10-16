// RPC sobre el DataChannel `rpc` (protocol/README.md §3).
import { RpcError } from "../protocol";
import { t } from "../i18n";

/** Lo mínimo de RTCDataChannel que usa el cliente (permite tests con un canal falso). */
export interface ChannelLike {
  readonly readyState: string;
  send(data: string): void;
}

interface Pending {
  resolve: (v: unknown) => void;
  reject: (e: Error) => void;
  timer: ReturnType<typeof setTimeout>;
  off?: () => void;
}

type Wire =
  | { type: "res"; id: number; result: unknown }
  | { type: "err"; id: number; error: { code: string; message: string } };

export const DEFAULT_TIMEOUT_MS = 15_000;

export class RpcClient {
  private nextId = 1;
  private pending = new Map<number, Pending>();
  private ch: ChannelLike | null = null;

  constructor(private readonly timeoutMs = DEFAULT_TIMEOUT_MS) {}

  /** Asocia el canal abierto. Los ids siguen creciendo entre reconexiones. */
  bind(ch: ChannelLike | null) {
    this.ch = ch;
  }

  get ready(): boolean {
    return this.ch?.readyState === "open";
  }

  call(method: string, params: unknown, opts: { signal?: AbortSignal; timeoutMs?: number } = {}): Promise<unknown> {
    const ch = this.ch;
    if (!ch || ch.readyState !== "open") return Promise.reject(new RpcError("DISCONNECTED", "Sin conexión con el NAS"));
    if (opts.signal?.aborted) return Promise.reject(new RpcError("CANCELLED", "Cancelado"));
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      const p: Pending = {
        resolve,
        reject,
        timer: setTimeout(() => this.settle(id, new RpcError("TIMEOUT", t("{method}: el NAS no respondió a tiempo", { method }))), opts.timeoutMs ?? this.timeoutMs),
      };
      if (opts.signal) {
        const onAbort = () => {
          // Avisamos al servidor (docs/03 §5) y rechazamos ya, sin esperar respuesta.
          try {
            ch.send(JSON.stringify({ type: "cancel", id }));
          } catch {
            /* canal cerrado: nada que cancelar */
          }
          this.settle(id, new RpcError("CANCELLED", "Cancelado"));
        };
        opts.signal.addEventListener("abort", onAbort, { once: true });
        p.off = () => opts.signal!.removeEventListener("abort", onAbort);
      }
      this.pending.set(id, p);
      try {
        ch.send(JSON.stringify({ type: "req", id, method, params: params ?? null }));
      } catch (e) {
        this.settle(id, new RpcError("DISCONNECTED", (e as Error).message || "No se pudo enviar"));
      }
    });
  }

  /** Mensaje entrante del canal `rpc`. Devuelve false si no era una respuesta válida. */
  handle(raw: string): boolean {
    let msg: Wire;
    try {
      msg = JSON.parse(raw) as Wire;
    } catch {
      return false;
    }
    if (typeof msg?.id !== "number") return false;
    if (msg.type === "res") return this.settle(msg.id, undefined, msg.result);
    if (msg.type === "err") return this.settle(msg.id, new RpcError(msg.error?.code ?? "INTERNAL", msg.error?.message ?? "Error del NAS"));
    return false;
  }

  /** Rechaza todo lo pendiente (el canal se cayó). */
  failAll(code = "DISCONNECTED", message = "Se perdió la conexión con el NAS") {
    for (const id of [...this.pending.keys()]) this.settle(id, new RpcError(code, message));
  }

  get inFlight(): number {
    return this.pending.size;
  }

  private settle(id: number, err?: Error, result?: unknown): boolean {
    const p = this.pending.get(id);
    if (!p) return false;
    this.pending.delete(id);
    clearTimeout(p.timer);
    p.off?.();
    if (err) p.reject(err);
    else p.resolve(result);
    return true;
  }
}
