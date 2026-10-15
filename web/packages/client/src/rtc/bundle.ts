// Canal `bundle` (protocol/README.md §15): el NAS sirve los archivos de su escritorio
// como frames [u32 id BE][u64 offset BE][datos]. El cargador del portal pide cada
// archivo con bundle.get y verifica su sha256 contra el manifiesto firmado.

type Pending = { size: number; got: number; buf: Uint8Array<ArrayBuffer>; resolve: (b: Uint8Array<ArrayBuffer>) => void; reject: (e: Error) => void };

export class BundleReceiver {
  private pending = new Map<number, Pending>();
  private early = new Map<number, ArrayBuffer[]>();

  onFrame(buf: ArrayBuffer) {
    if (buf.byteLength < 12) return;
    const v = new DataView(buf);
    const id = v.getUint32(0);
    const p = this.pending.get(id);
    if (!p) {
      const l = this.early.get(id) ?? [];
      l.push(buf);
      this.early.set(id, l);
      return;
    }
    this.write(id, p, buf);
  }

  private write(id: number, p: Pending, buf: ArrayBuffer) {
    const v = new DataView(buf);
    const off = Number(v.getBigUint64(4));
    const data = new Uint8Array(buf, 12);
    if (off + data.length > p.size) return p.reject(new Error("bundle: frame fuera de rango"));
    p.buf.set(data, off);
    p.got += data.length;
    if (p.got >= p.size) {
      this.pending.delete(id);
      p.resolve(p.buf);
    }
  }

  /** Espera los `size` bytes del archivo `id` (ya pedido con bundle.get). */
  expect(id: number, size: number): Promise<Uint8Array<ArrayBuffer>> {
    return new Promise((resolve, reject) => {
      const p: Pending = { size, got: 0, buf: new Uint8Array(size), resolve, reject };
      if (size === 0) return resolve(p.buf);
      this.pending.set(id, p);
      for (const b of this.early.get(id) ?? []) this.write(id, p, b);
      this.early.delete(id);
    });
  }

  failAll(e = new Error("se cortó la conexión")) {
    for (const p of this.pending.values()) p.reject(e);
    this.pending.clear();
    this.early.clear();
  }
}

export async function sha256Hex(b: Uint8Array<ArrayBuffer>): Promise<string> {
  const d = await crypto.subtle.digest("SHA-256", b);
  return Array.from(new Uint8Array(d), (x) => x.toString(16).padStart(2, "0")).join("");
}
