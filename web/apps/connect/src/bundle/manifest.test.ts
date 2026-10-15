import { describe, expect, it } from "vitest";
import vectors from "../../../../../protocol/vectors/tangoconnect.json";
import { sha256Hex, validateManifestShape, verifyManifest, type BundleManifestResult } from "./manifest";

const v = vectors.bundle;
const result: BundleManifestResult = { version: v.version, files: v.files, signed: v.signed, signature: v.signatureB64, keyId: v.keyId };

describe("manifiesto del escritorio (vector de internal/connect.Sign)", () => {
  it("verifica la firma Ed25519 de tangod con WebCrypto y usa el texto firmado", async () => {
    const m = await verifyManifest(result, v.publicKeyB64);
    expect(m).not.toBeNull();
    expect(m!.files.map((f) => f.path)).toEqual(["assets/index-abc.js", "index.html"]);
    expect(validateManifestShape(m!)).toBeNull();
  });
  it("rechaza texto alterado, firma vacía (build sin firmar) o clave equivocada", async () => {
    expect(await verifyManifest({ ...result, signed: result.signed.replace('"size":14', '"size":15') }, v.publicKeyB64)).toBeNull();
    expect(await verifyManifest({ ...result, signature: "" }, v.publicKeyB64)).toBeNull();
    expect(await verifyManifest(result, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")).toBeNull();
    expect(await verifyManifest(result, "")).toBeNull();
  });
  it("el sha256 coincide con el contenido", async () => {
    expect(await sha256Hex(new TextEncoder().encode("<!doctype html>"))).toBe(v.files[1].sha256);
  });
  it("valida la forma", () => {
    const base = { v: 1, version: "x", files: v.files };
    expect(validateManifestShape(base)).toBeNull();
    expect(validateManifestShape({ ...base, v: 2 })).toMatch(/versión/);
    expect(validateManifestShape({ ...base, files: [...v.files, { path: "/etc/passwd", sha256: "a".repeat(64), size: 1 }] })).toMatch(/ruta/);
    expect(validateManifestShape({ ...base, files: [...v.files, { path: "a/../b", sha256: "a".repeat(64), size: 1 }] })).toMatch(/ruta/);
    expect(validateManifestShape({ ...base, files: [{ path: "x.js", sha256: "a".repeat(64), size: 1 }] })).toMatch(/index.html/);
  });
});
