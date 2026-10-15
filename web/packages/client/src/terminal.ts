// Terminal de administración (protocol §12, docs/03 §4): canal binario `term`, cada frame lleva
// un prefijo u32 big-endian con el id de la terminal. Abrir/redimensionar/cerrar van por `rpc`.

export interface TermSession {
  readonly id: number;
  write(data: string | Uint8Array): void;
  resize(cols: number, rows: number): void;
  onData(fn: (chunk: Uint8Array) => void): () => void;
  /** Se dispara cuando el NAS cierra la terminal (exit del shell o desconexión). */
  onExit(fn: (reason: string) => void): () => void;
  close(): void;
}

export interface Terminals {
  open(cols: number, rows: number): Promise<TermSession>;
}

const enc = new TextEncoder();

export function encodeTermFrame(id: number, data: string | Uint8Array): ArrayBuffer {
  const body = typeof data === "string" ? enc.encode(data) : data;
  const buf = new ArrayBuffer(4 + body.byteLength);
  new DataView(buf).setUint32(0, id >>> 0, false);
  new Uint8Array(buf, 4).set(body);
  return buf;
}

export function decodeTermFrame(buf: ArrayBuffer): { id: number; data: Uint8Array } | null {
  if (buf.byteLength < 4) return null;
  return { id: new DataView(buf).getUint32(0, false), data: new Uint8Array(buf, 4) };
}

/** Sesión genérica: el transporte concreto provee `send`, `rpcResize` y `rpcClose`. */
export class BaseTermSession implements TermSession {
  private dataSubs = new Set<(c: Uint8Array) => void>();
  private exitSubs = new Set<(r: string) => void>();
  private closed = false;

  constructor(
    readonly id: number,
    private readonly io: { send: (frame: ArrayBuffer) => void; resize: (cols: number, rows: number) => void; close: () => void },
  ) {}

  write(data: string | Uint8Array) {
    if (!this.closed) this.io.send(encodeTermFrame(this.id, data));
  }

  resize(cols: number, rows: number) {
    if (!this.closed) this.io.resize(cols, rows);
  }

  onData(fn: (c: Uint8Array) => void) {
    this.dataSubs.add(fn);
    return () => this.dataSubs.delete(fn);
  }

  onExit(fn: (r: string) => void) {
    this.exitSubs.add(fn);
    return () => this.exitSubs.delete(fn);
  }

  /** Lo llama el transporte cuando llega un frame para esta terminal. */
  push(data: Uint8Array) {
    this.dataSubs.forEach((f) => f(data));
  }

  /** Lo llama el transporte (o close()) al terminar. */
  exit(reason: string) {
    if (this.closed) return;
    this.closed = true;
    this.exitSubs.forEach((f) => f(reason));
  }

  close() {
    if (this.closed) return;
    this.io.close();
    this.exit("closed");
  }
}
