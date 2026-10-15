// Modo híbrido (protocol/README.md §5): lo que tangod implementa va por WebRTC,
// el resto lo responde el MockTransport y la UI lo sigue marcando DEMO.
import type { Transport, TransportState } from "../transport";
import type { Evt, Method, Params, Result, Topic } from "../protocol";
import type { RtcTransport } from "./RtcTransport";
import { RpcError } from "../protocol";
import type { Transfers } from "../transfers";
import type { Terminals } from "../terminal";

export class HybridTransport implements Transport {
  readonly kind = "hybrid" as const;
  /** Se resuelve con el primer `hello` o, si el NAS no aparece, a los `helloTimeoutMs` (queda solo el mock). */
  readonly ready: Promise<void>;

  constructor(
    readonly rtc: RtcTransport,
    readonly mock: Transport,
    helloTimeoutMs = 4_000,
  ) {
    this.ready = new Promise((resolve) => {
      if (rtc.hello) return resolve();
      const timer = setTimeout(done, helloTimeoutMs);
      const off = rtc.onCapabilities(done);
      function done() {
        clearTimeout(timer);
        off();
        resolve();
      }
    });
  }

  /** Transferencias reales si el NAS implementa files.*.begin; si no, las simula el mock. */
  readonly transfers: Transfers = {
    upload: async (...a) => (await this.pick("files.upload.begin")).upload(...a),
    download: async (...a) => (await this.pick("files.download.begin")).download(...a),
  };

  /** Terminal real si el NAS implementa term.open; si no, la terminal DEMO del mock. */
  readonly terminals: Terminals = {
    open: async (cols, rows) => {
      await this.ready;
      const t = this.rtc.isReal("term.open") ? this.rtc.terminals : this.mock.terminals;
      if (!t) throw new RpcError("NOT_IMPLEMENTED", "Este transporte no soporta terminales");
      return t.open(cols, rows);
    },
  };

  private async pick(method: Method): Promise<Transfers> {
    await this.ready;
    const t = this.rtc.isReal(method) ? this.rtc.transfers : this.mock.transfers;
    if (!t) throw new RpcError("NOT_IMPLEMENTED", "Este transporte no soporta transferencias de archivos");
    return t;
  }

  async call<M extends Method>(method: M, params: Params<M>, opts?: { signal?: AbortSignal }): Promise<Result<M>> {
    await this.ready;
    return this.rtc.isReal(method) ? this.rtc.call(method, params, opts) : this.mock.call(method, params, opts);
  }

  subscribe<T extends Topic>(topic: T, fn: (e: Evt<T>) => void): () => void {
    // El filtro es dinámico: si el hello llega después (o cambia), se corta solo la fuente que sobra.
    const offR = this.rtc.subscribe(topic, (e) => this.rtc.hasTopic(topic) && fn(e));
    const offM = this.mock.subscribe(topic, (e) => !this.rtc.hasTopic(topic) && fn(e));
    return () => {
      offR();
      offM();
    };
  }

  state(): TransportState {
    return this.rtc.state();
  }

  onState(fn: (s: TransportState) => void) {
    return this.rtc.onState(fn);
  }

  isReal(method: Method) {
    return this.rtc.isReal(method);
  }

  onCapabilities(fn: () => void) {
    return this.rtc.onCapabilities(fn);
  }

  onAuthLost(fn: () => void) {
    return this.rtc.onAuthLost(fn);
  }
}
