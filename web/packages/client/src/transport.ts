import type { Evt, Method, Params, Result, Topic, User } from "./protocol";
import type { Transfers } from "./transfers";
import type { Terminals } from "./terminal";

export type TransportState = "connecting" | "connected" | "reconnecting" | "closed";

/**
 * Lo único que las apps conocen del servidor. Implementaciones:
 * - MockTransport: datos demo (este prototipo)
 * - RtcTransport: DataChannels rpc/events/metrics (docs/03, protocol/README.md)
 * - HybridTransport: RtcTransport para lo que tangod implementa, mock para el resto
 * - NativeBridgeTransport: app nativa, WebRTC en Go (docs/06)
 */
export interface Transport {
  readonly kind: "mock" | "rtc" | "hybrid" | "native";
  call<M extends Method>(method: M, params: Params<M>, opts?: { signal?: AbortSignal }): Promise<Result<M>>;
  subscribe<T extends Topic>(topic: T, fn: (e: Evt<T>) => void): () => void;
  state(): TransportState;
  onState(fn: (s: TransportState) => void): () => void;
  /** true si `method` lo atiende el servidor real (no el mock). */
  isReal(method: Method): boolean;
  /** Se dispara cuando cambia el conjunto de métodos reales (llegó un `hello`). */
  onCapabilities(fn: () => void): () => void;
  /** El servidor rechazó la sesión guardada (token vencido/revocado): volver al login. */
  onAuthLost(fn: () => void): () => void;
  /** Subidas y bajadas de archivos (protocol §10). Opcional: no todo transporte las soporta. */
  readonly transfers?: Transfers;
  /** Terminales de administración por el canal `term` (protocol §12). Opcional. */
  readonly terminals?: Terminals;
}

/** Usuario autenticado de la sesión actual (lo setea el cliente tras login/resume). */
export type SessionUser = Pick<User, "name" | "full" | "color">;
