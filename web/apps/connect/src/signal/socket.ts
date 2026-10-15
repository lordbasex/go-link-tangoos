// Señalización remota para el RtcTransport de @tango/client (rtcFromSignal, docs/16 §2): cada
// (re)conexión abre un WebSocket nuevo a signalhub, hace claim (código de 9 dígitos) o reach (NAS
// ya vinculado, o enlace público) y entrega un SignalProvider unido a la sesión con el NAS.
// El `payload` de cada `signal` es el SignalPayload de tangod ({"kind":"offer"|"answer"|"candidate"})
// sin envoltura (internal/connect/service.go).
import type { SignalProvider } from "@tango/client";
import { APP } from "../config";
import { SignalError, SignalhubLink, type ICEServer } from "./signalhub";

export type Target =
  | { mode: "claim"; code: string }
  | { mode: "reach"; deviceId: string }
  /** Enlace público `/s/<deviceId>.<linkId>#<secreto>`: reach sin vínculo y después guest.open. */
  | { mode: "guest"; deviceId: string; link: string; secret: string };

export type Stage = "signal" | "waiting-nas" | "paired";

/** Une los ICE de signalhub con otros descartando STUN/TURN de redes privadas: desde internet no sirven. */
export function mergeIce(fromSignal: ICEServer[], others: unknown): RTCIceServer[] {
  const extra = Array.isArray(others) ? (others as RTCIceServer[]) : [];
  const usable = extra.filter((s) => {
    const urls = Array.isArray(s.urls) ? s.urls : [s.urls];
    return urls.every((u) => typeof u === "string" && !/^(stun|turns?):(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|127\.|[^:]*\.local)/i.test(u));
  });
  return [...fromSignal, ...usable];
}

export interface JoinHooks {
  signalUrl: string;
  onStage?: (s: Stage) => void;
  WebSocketImpl?: typeof WebSocket;
}

/** Abre la sesión con el NAS en signalhub. Rechaza con SignalError (código vencido, NAS desconectado…). */
export async function joinSignal(target: Target, h: JoinHooks): Promise<SignalProvider> {
  const link = new SignalhubLink({ url: h.signalUrl, WebSocketImpl: h.WebSocketImpl });
  h.onStage?.("signal");
  let remote = "";
  try {
    await link.open();
    h.onStage?.("waiting-nas");
    const env =
      target.mode === "claim"
        ? await link.request({ type: "claim", app: APP, code: target.code.replace(/\D/g, "") }, ["paired"])
        : await link.request({ type: "reach", app: APP, device_id: target.deviceId }, ["paired"]);
    remote = String(env.remote ?? "");
    if (!remote) throw new SignalError("bad envelope", false);
  } catch (e) {
    link.close();
    throw e instanceof SignalError ? e : new SignalError(String(e), false);
  }
  h.onStage?.("paired");
  const payloadSubs = new Set<(p: unknown) => void>();
  const closeSubs = new Set<() => void>();
  link.onMessage((env) => {
    if (env.type === "peer_left" && env.from === remote) return link.close();
    if (env.type !== "signal" || env.from !== remote || typeof env.payload !== "object" || env.payload === null) return;
    payloadSubs.forEach((f) => f(env.payload));
  });
  link.onClose(() => closeSubs.forEach((f) => f()));
  return {
    iceServers: mergeIce(link.iceServers, []),
    send: (payload) => void link.send({ type: "signal", to: remote, payload }),
    onPayload(fn) {
      payloadSubs.add(fn);
      return () => payloadSubs.delete(fn);
    },
    onClose(fn) {
      closeSubs.add(fn);
      return () => closeSubs.delete(fn);
    },
    close: () => link.close(),
  };
}
