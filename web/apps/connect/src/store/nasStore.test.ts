import { describe, expect, it } from "vitest";
import { NasStore, memoryBackend } from "./nasStore";

const nas = (id: string, lastSeen: string | null) => ({
  deviceId: id,
  name: `NAS ${id.slice(0, 4)}`,
  linkId: "0123456789abcdef",
  token: "tok",
  fingerprint: null,
  linkedAt: "2026-10-01T00:00:00.000Z",
  lastSeen,
});

describe("Mis NAS", () => {
  it("guarda, lista por último acceso, actualiza y olvida", async () => {
    const s = new NasStore(memoryBackend());
    await s.save(nas("11111111-1111-4111-8111-111111111111", "2026-10-10T00:00:00.000Z"));
    await s.save(nas("22222222-2222-4222-8222-222222222222", "2026-10-14T00:00:00.000Z"));
    expect((await s.list()).map((n) => n.deviceId[0])).toEqual(["2", "1"]);
    await s.touch("11111111-1111-4111-8111-111111111111", { lastSeen: "2026-10-15T00:00:00.000Z", name: "Casa" });
    const l = await s.list();
    expect(l[0].name).toBe("Casa");
    await s.remove("22222222-2222-4222-8222-222222222222");
    expect((await s.list()).length).toBe(1);
  });
  it("rechaza vínculos inválidos", async () => {
    const s = new NasStore(memoryBackend());
    await expect(s.save({ ...nas("no-es-uuid", null) })).rejects.toThrow();
    await expect(s.save({ ...nas("11111111-1111-4111-8111-111111111111", null), token: "" })).rejects.toThrow();
  });
});
