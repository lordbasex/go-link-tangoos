import { describe, expect, it } from "vitest";
import { resolveRange } from "./dcstream";

describe("resolveRange (Range del Service Worker → [start, end))", () => {
  it("sin Range: todo el archivo", () => expect(resolveRange(1000, null)).toEqual([0, 1000]));
  it("bytes=0- (lo que pide un <video>)", () => expect(resolveRange(1000, { start: 0, end: null })).toEqual([0, 1000]));
  it("bytes=100-199", () => expect(resolveRange(1000, { start: 100, end: 199 })).toEqual([100, 200]));
  it("el final se recorta al tamaño", () => expect(resolveRange(1000, { start: 900, end: 5000 })).toEqual([900, 1000]));
  it("sufijo bytes=-100", () => expect(resolveRange(1000, { start: null, end: 100 })).toEqual([900, 1000]));
  it("fuera del archivo → 416", () => {
    expect(resolveRange(1000, { start: 1000, end: null })).toBeNull();
    expect(resolveRange(1000, { start: 500, end: 100 })).toBeNull();
  });
  it("archivo vacío", () => expect(resolveRange(0, { start: 0, end: null })).toEqual([0, 0]));
});
