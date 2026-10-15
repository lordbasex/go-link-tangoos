import { describe, expect, it } from "vitest";
import { MockTransport } from "./mock/MockTransport";
import { zfsCheck, zfsSplit, zfsTolerance, zfsUsable } from "./protocol-zfs";

const G = 1024 ** 3;

describe("reglas ZFS (iguales a internal/zfs)", () => {
  it("capacidad, tolerancia y mínimos", () => {
    expect(zfsUsable("mirror", [4 * G, 2 * G])).toBe(2 * G);
    expect(zfsUsable("raidz1", [2 * G, 2 * G, 4 * G])).toBe(4 * G);
    expect(zfsUsable("raidz2", Array(6).fill(G))).toBe(4 * G);
    expect(zfsUsable("stripe", [G, 2 * G])).toBe(3 * G);
    expect(zfsTolerance("mirror", 3)).toBe(2);
    expect(zfsTolerance("raidz2", 6)).toBe(2);
    expect(zfsCheck("raidz1", 2)).not.toBeNull();
    expect(zfsCheck("raidz3", 5)).toBeNull();
    const s = zfsSplit("raidz1", [2 * G, 2 * G, 4 * G]);
    expect(s[2].unused).toBe(2 * G);
    expect(s.reduce((a, x) => a + x.data, 0)).toBeCloseTo(4 * G);
  });
});

describe("mock ZFS", () => {
  it("escenario zfs: pool RAIDZ1 con caché, volúmenes y zvol", async () => {
    const t = new MockTransport("zfs");
    const st = await t.call("storage.zfs.status", undefined);
    expect(st.enabled).toBe(true);
    expect(st.pools[0].layout).toBe("raidz1");
    expect(st.pools[0].cache.length).toBe(1);
    const vols = await t.call("storage.volumes.list", undefined);
    expect(vols.filter((v) => v.kind === "zfs").length).toBe(2);
  });

  it("sin ZFS: habilitar, crear pool con plan y confirmación", async () => {
    const t = new MockTransport("hybrid");
    expect((await t.call("storage.zfs.status", undefined)).enabled).toBe(false);
    await expect(t.call("storage.zfs.plan", { name: "x", layout: "mirror", disks: [] })).rejects.toMatchObject({ code: "ZFS_UNAVAILABLE" });
    const { job } = await t.call("storage.zfs.enable", undefined);
    expect((await t.call("storage.zfs.status", undefined)).enabling).toBe(job);
  });
});
