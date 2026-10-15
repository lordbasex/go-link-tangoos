import { describe, expect, it } from "vitest";
import { hybridUsable, poolLayout, tiersUnused, tiersUsable } from "./protocol-pools";
import { poolsHandlers } from "./mock/pools";
import { disks as fixtureDisks, volumes as fixtureVolumes } from "./mock/data";
import { RpcError } from "./protocol";
import type { Disk, Job, Volume } from "./protocol";

const G = 1024 ** 3;
const MiB = 1024 ** 2;
// Disco de n GiB útiles exactos (hybridUsable le saca 2 MiB), igual que el test de Go.
const disk = (id: string, n: number) => ({ id, size: n * G + 2 * MiB });

describe("RAID híbrido: tramos (mismas reglas que internal/diskops/hybrid.go)", () => {
  const cases: { name: string; profile: "hybrid1" | "hybrid2"; sizes: number[]; tiers: [string, number, number][]; usable: number; unused: number }[] = [
    { name: "2+2+4", profile: "hybrid1", sizes: [2, 2, 4], tiers: [["raid5", 3, 4], ["unused", 1, 0]], usable: 4, unused: 2 },
    { name: "2+4+4", profile: "hybrid1", sizes: [2, 4, 4], tiers: [["raid5", 3, 4], ["raid1", 2, 2]], usable: 6, unused: 0 },
    { name: "2+2+4+4", profile: "hybrid1", sizes: [2, 2, 4, 4], tiers: [["raid5", 4, 6], ["raid1", 2, 2]], usable: 8, unused: 0 },
    { name: "uno más grande que la suma", profile: "hybrid1", sizes: [1, 10], tiers: [["raid1", 2, 1], ["unused", 1, 0]], usable: 1, unused: 9 },
    { name: "2+4+8+8", profile: "hybrid1", sizes: [2, 4, 8, 8], tiers: [["raid5", 4, 6], ["raid5", 3, 4], ["raid1", 2, 4]], usable: 14, unused: 0 },
    { name: "híbrido-2 2+2+4+4+4", profile: "hybrid2", sizes: [2, 2, 4, 4, 4], tiers: [["raid6", 5, 6], ["unused", 3, 0]], usable: 6, unused: 6 },
    { name: "híbrido-2 2+4+4+4+4", profile: "hybrid2", sizes: [2, 4, 4, 4, 4], tiers: [["raid6", 5, 6], ["raid6", 4, 4]], usable: 10, unused: 0 },
  ];
  for (const c of cases) {
    it(c.name, () => {
      const t = poolLayout(c.profile, c.sizes.map((s, i) => disk(`d${i}`, s)));
      expect(t.map((x) => [x.level, x.members.length, x.usable / G])).toEqual(c.tiers);
      expect(tiersUsable(t) / G).toBe(c.usable);
      expect(tiersUnused(t) / G).toBe(c.unused);
    });
  }

  it("junta tamaños a menos de 1 % (discos 'de 4 TB' de marcas distintas)", () => {
    const t = poolLayout("hybrid1", [disk("a", 4), { id: "b", size: 4 * G * 1.004 }, disk("c", 4)]);
    expect(t).toHaveLength(1);
    expect(t[0].level).toBe("raid5");
  });

  it("RAID clásico: un tramo con el disco más chico", () => {
    const t = poolLayout("raid5", [disk("a", 2), disk("b", 2), disk("c", 4)]);
    expect(t).toHaveLength(1);
    expect(t[0].usable).toBe(2 * (2 * G + 2 * MiB));
    expect(hybridUsable(2 * MiB)).toBe(0);
  });
});

function mockCtx(scenario: "pools" | "hybrid" | "locked") {
  const ds: Disk[] = fixtureDisks(scenario);
  let vs: Volume[] = fixtureVolumes(scenario, ds);
  const done: (() => void)[] = [];
  const ctx = {
    scenario: () => scenario,
    disks: () => ds,
    vols: () => vs,
    setVols: (v: Volume[]) => (vs = v),
    emit: () => undefined,
    startJob: (type: string, title: string, detail: string, progress: number, _step: number, _rate?: string, onDone?: () => void) => {
      if (onDone) done.push(onDone);
      return { id: `j${done.length}`, type, title, detail, progress, state: "running", startedAt: "" } as Job;
    },
    notify: () => undefined,
  };
  return { h: poolsHandlers(ctx), ds, vols: () => vs, finish: () => done.splice(0).forEach((f) => f()) };
}

describe("demo de grupos (mock)", () => {
  it("crea un grupo híbrido, un volumen cifrado, lo bloquea y lo desbloquea", () => {
    const { h, ds, vols, finish } = mockCtx("hybrid");
    const ids = ds.filter((d) => d.bay).map((d) => d.id);
    const plan = h["storage.pools.plan"]!({ name: "pool1", profile: "hybrid1", disks: ids });
    expect(plan.summary.tiers.map((t) => t.level)).toEqual(["raid5", "raid5", "unused"]);
    expect(() => h["storage.pools.create"]!({ plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: "otro" })).toThrow(RpcError);
    h["storage.pools.create"]!({ plan_id: plan.plan_id, confirm_token: plan.confirm_token, typed_name: "pool1" });
    finish();
    const [pool] = h["storage.pools.list"]!(undefined);
    expect(pool.state).toBe("healthy");
    expect(ds.filter((d) => d.pool === pool.id)).toHaveLength(4);

    h["storage.poolVolumes.create"]!({ pool: pool.id, name: "privado", size: 0, fs: "btrfs", encryption: { passphrase: "una frase larga de prueba", keyDevice: null } });
    finish();
    const v = vols().find((x) => x.name === "privado")!;
    expect(v.encrypted && v.kind === "pool" && v.size === pool.size).toBe(true);
    expect(h["storage.pools.list"]!(undefined)[0].free).toBe(0);

    h["storage.volumes.lock"]!({ volume: v.id });
    expect(vols().find((x) => x.id === v.id)!.state).toBe("locked");
    expect(() => h["storage.volumes.unlock"]!({ volume: v.id, passphrase: "error" })).toThrow(/no abre/);
    h["storage.volumes.unlock"]!({ volume: v.id, passphrase: "una frase larga de prueba" });
    expect(vols().find((x) => x.id === v.id)!.locked).toBe(false);
    // Un grupo con volúmenes no se borra.
    expect(() => h["storage.pools.deletePlan"]!({ pool: pool.id })).toThrow(/eliminalos primero/);
  });

  it("caché: lectura/escritura necesita 2 SSD y un HDD no sirve", () => {
    const { h, ds } = mockCtx("pools");
    const hdd = ds.find((d) => d.use === "free" && d.kind === "hdd");
    expect(() => h["storage.cache.plan"]!({ volume: "vol2", disks: [ds[1].id], mode: "readwrite" })).toThrow(/2 SSD/);
    if (hdd) expect(() => h["storage.cache.plan"]!({ volume: "vol2", disks: [hdd.id], mode: "read" })).toThrow();
    expect(() => h["storage.cache.plan"]!({ volume: "vol1", disks: [ds[1].id], mode: "read" })).toThrow(/ya tiene caché/);
  });

  it("escenario locked: volumen bloqueado con llave USB", () => {
    const { h, vols } = mockCtx("locked");
    const v = vols().find((x) => x.locked)!;
    expect(v.keyDevice).toBe(h["storage.keyDevices.list"]!(undefined)[0].uuid);
    h["storage.volumes.unlock"]!({ volume: v.id, useKeyDevice: true });
    expect(vols().find((x) => x.id === v.id)!.mount).toBe("/privado");
  });
});
