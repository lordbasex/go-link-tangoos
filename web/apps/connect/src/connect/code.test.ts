import { describe, expect, it } from "vitest";
import { applyDigits, deleteDigit, formatCode, isCompleteCode, normalizeCode } from "./code";

describe("código de vinculación", () => {
  it("normaliza y formatea como signalhub (3-3-3)", () => {
    expect(normalizeCode("113-134 323x9")).toBe("113134323");
    expect(formatCode("113134323")).toBe("113 134 323");
    expect(formatCode("1131")).toBe("113 1");
    expect(isCompleteCode("113 134 323")).toBe(true);
    expect(isCompleteCode("11313432")).toBe(false);
  });
  it("escribir en una casilla avanza y pegar reparte", () => {
    expect(applyDigits("", 0, "1")).toEqual({ value: "1", focus: 1 });
    expect(applyDigits("1", 1, "13134323")).toEqual({ value: "113134323", focus: 8 });
    expect(applyDigits("12", 0, "9")).toEqual({ value: "92", focus: 1 });
    expect(applyDigits("12", 1, "a")).toEqual({ value: "12", focus: 1 });
  });
  it("borrar en casilla vacía retrocede", () => {
    expect(deleteDigit("123", 3)).toEqual({ value: "12", focus: 2 });
    expect(deleteDigit("123", 1)).toEqual({ value: "1 3", focus: 1 });
    expect(isCompleteCode("1 3".replace(/\s/g, ""))).toBe(false);
  });
});
