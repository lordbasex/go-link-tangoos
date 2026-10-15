// Terminales sobre el canal binario `term` (protocol §12).
import { BaseTermSession, decodeTermFrame } from "../terminal";
import type { Terminals, TermSession } from "../terminal";
import type { Method, Params, Result } from "../protocol";

interface Deps {
  call: <M extends Method>(m: M, p: Params<M>) => Promise<Result<M>>;
  channel: () => RTCDataChannel | undefined;
}

export class RtcTerminals implements Terminals {
  private sessions = new Map<number, BaseTermSession>();

  constructor(private readonly d: Deps) {}

  async open(cols: number, rows: number): Promise<TermSession> {
    const { id } = await this.d.call("term.open", { cols, rows });
    const s = new BaseTermSession(id, {
      send: (frame) => {
        const ch = this.d.channel();
        if (ch?.readyState === "open") ch.send(frame);
      },
      resize: (c, r) => void this.d.call("term.resize", { id, cols: c, rows: r }).catch(() => undefined),
      close: () => {
        this.sessions.delete(id);
        void this.d.call("term.close", { id }).catch(() => undefined);
      },
    });
    this.sessions.set(id, s);
    s.onExit(() => this.sessions.delete(id));
    return s;
  }

  /** Frame entrante del canal `term`. Un frame de 4 bytes sin datos = el NAS cerró esa terminal. */
  onFrame(buf: ArrayBuffer) {
    const f = decodeTermFrame(buf);
    if (!f) return;
    const s = this.sessions.get(f.id);
    if (!s) return;
    if (f.data.byteLength === 0) s.exit("exit");
    else s.push(f.data);
  }

  failAll() {
    for (const s of this.sessions.values()) s.exit("disconnected");
    this.sessions.clear();
  }
}
