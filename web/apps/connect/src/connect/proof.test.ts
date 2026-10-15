import { describe, expect, it } from "vitest";
import vectors from "../../../../../protocol/vectors/tangoconnect.json";
import { linkProof, normalizeFingerprint, sameText, tokenKey } from "./proof";

const hex = (b: ArrayBuffer) => Array.from(new Uint8Array(b), (x) => x.toString(16).padStart(2, "0")).join("");

describe("prueba de vínculo (vector de internal/connect)", () => {
  it("la clave y la prueba coinciden con LinkProof de tangod", async () => {
    const v = vectors.linkProof;
    const key = await tokenKey(v.deviceToken);
    expect(hex(key)).toBe(v.tokenHashHex);
    expect(await linkProof(key, v.nonce)).toBe(v.proof);
  });
  it("otro token da otra prueba", async () => {
    const p = await linkProof(await tokenKey("otro"), vectors.linkProof.nonce);
    expect(sameText(p, vectors.linkProof.proof)).toBe(false);
  });
  it("normaliza huellas DTLS de SDP y de tangod", () => {
    expect(normalizeFingerprint("sha-256 ab:cd:01")).toBe("ABCD01");
    expect(normalizeFingerprint("AB:CD:01")).toBe("ABCD01");
    expect(normalizeFingerprint(null)).toBe("");
  });
});
