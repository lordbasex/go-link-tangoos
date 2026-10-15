import { describe, expect, it } from "vitest";
import { expectedProof, tokenIdOf, verifyHost } from "./hostproof";

// Vector compartido con Go (internal/auth: TokenID / ResumeProof).
const TOKEN = "tok_test_123";
const NONCE = "abcdef0123456789abcdef";

describe("host proves first", () => {
  it("matches the Go implementation", async () => {
    const { id, h1 } = await tokenIdOf(TOKEN);
    expect(id).toBe("7b997127c5c39bc3376f16a7157636f9");
    expect(await expectedProof(h1, NONCE)).toBe("4617cfa40bf1a033f4538680df78a1b2d73722ef707abaf569e42b05a153d9cd");
  });

  it("refuses a NAS that cannot prove it knows the token", async () => {
    const fake = async () => ({ proof: "00".repeat(32) });
    await expect(verifyHost(fake, TOKEN, true)).rejects.toMatchObject({ code: "HOST_NOT_VERIFIED" });
  });

  it("accepts the real NAS and never sends the token in the challenge", async () => {
    let sent: unknown;
    const real = async (_m: "auth.challenge", p: { tokenId: string; nonce: string }) => {
      sent = p;
      const { h1 } = await tokenIdOf(TOKEN);
      return { proof: await expectedProof(h1, p.nonce) };
    };
    await verifyHost(real, TOKEN, true);
    expect(JSON.stringify(sent)).not.toContain(TOKEN);
  });

  it("skips when the server does not support it", async () => {
    await verifyHost(async () => ({ proof: "x" }), TOKEN, false);
  });
});
