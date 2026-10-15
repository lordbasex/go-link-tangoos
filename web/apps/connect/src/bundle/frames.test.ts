import { describe, expect, it } from "vitest";
import { encodeFrame } from "@tango/client";
import { FrameCollector } from "./frames";

describe("recolector de frames", () => {
  it("arma una transferencia aunque los frames lleguen antes que el registro", async () => {
    const c = new FrameCollector(1000);
    c.onFrame(encodeFrame(7, 3, new Uint8Array([4, 5])));
    const p = c.expect(7, 5);
    c.onFrame(encodeFrame(7, 0, new Uint8Array([1, 2, 3])));
    expect(Array.from(await p)).toEqual([1, 2, 3, 4, 5]);
  });
  it("tamaño 0 termina enseguida y un frame fuera de rango falla", async () => {
    const c = new FrameCollector(1000);
    expect((await c.expect(1, 0)).byteLength).toBe(0);
    const p = c.expect(2, 2);
    c.onFrame(encodeFrame(2, 1, new Uint8Array([1, 2])));
    await expect(p).rejects.toThrow();
  });
});
