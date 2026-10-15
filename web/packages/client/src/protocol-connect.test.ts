import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { connectLinkProof, parseShareURL } from "./protocol-connect";

describe("TangoConnect", () => {
  it("la prueba de vínculo coincide con el vector compartido con Go", async () => {
    const f = JSON.parse(readFileSync(resolve(__dirname, "../../../../protocol/vectors/connect-link-proof.json"), "utf8"));
    for (const v of f.vectors) expect(await connectLinkProof(v.token, v.nonce)).toBe(v.proof);
  });

  it("separa los enlaces públicos y rechaza los mal formados", () => {
    const u = "https://tangoos.go-link.org/s/e153109f-0614-414d-8129-b34fc1a8793f.0123456789abcdef#AbCdEf_-0123456789xyzABC";
    expect(parseShareURL(u)).toEqual({ deviceId: "e153109f-0614-414d-8129-b34fc1a8793f", linkId: "0123456789abcdef", secret: "AbCdEf_-0123456789xyzABC" });
    expect(parseShareURL("https://x/s/abc#def")).toBeNull();
    expect(parseShareURL(u.replace("#", "?s="))).toBeNull();
  });
});
