import { describe, expect, it } from "vitest";
import { clusterHandlers } from "./mock/cluster";
import { clusterInfoText, clusterVolumeTone } from "./protocol-cluster";
import type { ClusterVolume } from "./protocol-cluster";
import { RpcError } from "./protocol";
import type { Job, Volume } from "./protocol";

function ctx(scenario: "cluster" | "healthy") {
  const jobs: (Job & { onDone?: () => void })[] = [];
  const h = clusterHandlers({
    scenario: () => scenario,
    emit: () => {},
    startJob: (type, title, detail, progress, step, rate, onDone) => {
      const j = { id: `j${jobs.length}`, type, title, detail, progress, state: "running", startedAt: "", rate, onDone } as Job & { onDone?: () => void };
      jobs.push(j);
      return j;
    },
    notify: () => {},
    volumes: () => [{ id: "v1", name: "volume1", mount: "/volume1", size: 100, used: 10 } as Volume],
  });
  const call = <T,>(m: string, p?: unknown): T => (h as Record<string, (p: unknown) => T>)[m](p);
  const finish = () => jobs.splice(0).forEach((j) => j.onDone?.());
  return { call, finish };
}

describe("clúster GlusterFS (mock y helpers)", () => {
  it("clusterInfoText es igual a `gluster volume info` (réplica 3 y árbitro)", () => {
    const v: ClusterVolume = {
      name: "gv0", id: "9a379168-db13-4b29-b1d3-047442d6ab24", status: "Started", type: "Replicate", replica: 3, arbiter: 0, sets: 1,
      bricks: ["tg-gluster1", "tg-gluster2", "tg-gluster3"].map((h) => ({ host: h, path: "/srv/fx/gv0/b", hostUuid: "", online: true, healPending: 0, splitBrain: 0 })),
      options: { "cluster.quorum-type": "auto", "cluster.server-quorum-ratio": "51%" }, optOrder: ["cluster.quorum-type", "cluster.server-quorum-ratio"],
      sizeTotal: 0, sizeFree: 0, healPending: 0, splitBrain: 0, online: 3,
    };
    expect(clusterInfoText(v)).toBe(
      [
        "Volume Name: gv0", "Type: Replicate", "Volume ID: 9a379168-db13-4b29-b1d3-047442d6ab24", "Status: Started", "Snapshot Count: 0",
        "Number of Bricks: 1 x 3 = 3", "Transport-type: tcp", "Bricks:",
        "Brick1: tg-gluster1:/srv/fx/gv0/b", "Brick2: tg-gluster2:/srv/fx/gv0/b", "Brick3: tg-gluster3:/srv/fx/gv0/b",
        "Options Reconfigured:", "cluster.quorum-type: auto", "cluster.server-quorum-ratio: 51%",
      ].join("\n"),
    );
    expect(clusterInfoText({ ...v, arbiter: 1, bricks: v.bricks.map((b, i) => ({ ...b, arbiter: i === 2 })) })).toContain("Number of Bricks: 1 x (2 + 1) = 3");
    expect(clusterVolumeTone({ ...v, splitBrain: 1 })).toBe("crit");
    expect(clusterVolumeTone({ ...v, online: 2 })).toBe("warn");
    expect(clusterVolumeTone({ ...v, status: "Stopped" })).toBe("info");
    expect(clusterVolumeTone(v)).toBe("ok");
  });

  it("demo: 3 nodos, el preset de producción y los avisos", () => {
    const { call } = ctx("cluster");
    const st = call<{ nodes: unknown[]; volumes: ClusterVolume[]; presets: { id: string; options: Record<string, string> }[] }>("cluster.status");
    expect(st.nodes).toHaveLength(3);
    expect(st.volumes.map(clusterVolumeTone)).toEqual(["ok", "warn", "crit"]);
    const prod = st.presets.find((p) => p.id === "production-replica3")!;
    expect(Object.keys(prod.options)).toHaveLength(17);
    expect(prod.options["cluster.server-quorum-ratio"]).toBe("51%");
  });

  it("sin instalar → instalar → unir → crear → montar → borrar", () => {
    const { call, finish } = ctx("healthy");
    expect(call<{ installed: boolean }>("cluster.status").installed).toBe(false);
    expect(() => call("cluster.peer.prepare", { host: "nas-b", code: "ABCDE-FGHJK" })).toThrow(RpcError);
    call("cluster.install");
    finish();
    expect(call<{ installed: boolean; nodes: unknown[] }>("cluster.status").nodes).toHaveLength(1);
    expect(() => call("cluster.peer.prepare", { host: "rechazado", code: "ABCDE-FGHJK" })).toThrow(/invitalo/);
    expect(() => call("cluster.peer.prepare", { host: "nas-b", code: "corto" })).toThrow(/código/);
    for (const host of ["nas-b", "nas-c"]) {
      const p = call<{ id: string; fingerprint: string }>("cluster.peer.prepare", { host, code: "ABCDE-FGHJK" });
      expect(p.fingerprint.split(" ")).toHaveLength(8);
      call("cluster.peer.confirm", { id: p.id });
    }
    expect(() => call("cluster.peer.confirm", { id: "j1" })).toThrow(/venció/);
    call("cluster.volume.create", { name: "gv_docs", replica: 3, preset: "production-replica3", start: true, bricks: ["tango", "nas-b", "nas-c"].map((host) => ({ host, kind: "dir", tangoVolume: "volume1" })) });
    finish();
    const st = call<{ volumes: ClusterVolume[] }>("cluster.status");
    expect(st.volumes[0].bricks[1].path).toBe("/volume1/@gluster/gv_docs");
    expect(st.volumes[0].bricks[1].kind).toBe("dir");
    // Sin bricks dedicados thin no hay instantáneas.
    expect(() => call("cluster.volume.snapshot", { name: "gv_docs", op: "create", snap: "s1" })).toThrow(/thin/);
    // Cuotas
    call("cluster.volume.quota", { name: "gv_docs", op: "enable" });
    const q = call<{ quotas: { path: string }[]; job?: string }>("cluster.volume.quota", { name: "gv_docs", op: "limit", path: "/Fotos", limit: 10 * 1024 ** 3 });
    expect(q.quotas.map((x) => x.path)).toEqual(["/Fotos"]);
    expect(q.job).toBeTruthy(); // lo que ya hay en la carpeta se cuenta con un job
    finish();
    const used = call<{ quotas: { used: number }[] }>("cluster.volume.quota", { name: "gv_docs", op: "list" }).quotas[0].used;
    expect(used).toBeGreaterThan(0);
    // Solo carpetas de primer nivel
    expect(() => call("cluster.volume.quota", { name: "gv_docs", op: "limit", path: "/Fotos/2026", limit: 1024 ** 3 })).toThrow(/primer nivel/);
    // Agregar un conjunto y rebalancear hasta terminar
    call("cluster.volume.bricks.add", { name: "gv_docs", bricks: ["tango", "nas-b", "nas-c"].map((host) => ({ host, kind: "lv", pool: "data", size: 1024 ** 3, thin: true })) });
    finish();
    expect(call<{ volumes: ClusterVolume[] }>("cluster.status").volumes[0].type).toBe("Distributed-Replicate");
    call("cluster.volume.rebalance", { name: "gv_docs", action: "start" });
    let done = false;
    for (let i = 0; i < 6 && !done; i++) done = !!call<{ task: { done: boolean } | null }>("cluster.volume.rebalance", { name: "gv_docs", action: "status" }).task?.done;
    expect(done).toBe(true);
    expect(st.volumes[0].options["network.ping-timeout"]).toBe("10");
    call("cluster.volume.mount", { name: "gv_docs", enabled: true, share: "Docs" });
    finish();
    expect(() => call("cluster.volume.action", { name: "gv_docs", action: "stop" })).toThrow(/desmontalo/);
    call("cluster.volume.mount", { name: "gv_docs", enabled: false });
    finish();
    expect(() => call("cluster.volume.action", { name: "gv_docs", action: "delete" })).toThrow(/stopped/);
    call("cluster.volume.action", { name: "gv_docs", action: "stop" });
    call("cluster.volume.action", { name: "gv_docs", action: "delete" });
    const end = call<{ volumes: ClusterVolume[]; orphans: unknown[] }>("cluster.status");
    expect(end.volumes).toHaveLength(0);
    expect(end.orphans).toHaveLength(2); // la carpeta y el volumen dedicado de este nodo
  });
});
