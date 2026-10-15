import "./storage.css";
import { useEffect, useState } from "react";
import { Camera, Database, HardDrive, Layers, LayoutDashboard, Network, Plus } from "lucide-react";
import { useLive, useTransport, useIsReal } from "@tango/client";
import type { Disk, Pool, Volume } from "@tango/client";
import { Button, DemoBadge, SidebarItem } from "@tango/ui";
import { WindowToolbar, toast, useWindowMenus } from "@tango/shell";
import type { AppProps } from "@tango/shell";
import { Overview } from "./Overview";
import { Disks, Snapshots, Volumes } from "./Sections";
import { CreateVolume } from "./CreateVolume";
import { ExpandVolume, ReplaceDisk } from "./Maintenance";
import type { Section } from "./util";
import { useAvailable } from "./util";
import { Iscsi } from "./Iscsi";
import { Pools } from "./Pools";
import { CreatePool } from "./CreatePool";
import { CreatePoolVolume } from "./PoolVolume";

const SIDE = 210;

const titles: Record<Section, { title: string; sub: string }> = {
  overview: { title: "Resumen", sub: "Estado del almacenamiento" },
  pools: { title: "Grupos de almacenamiento", sub: "Discos y su protección (RAID o híbrido)" },
  volumes: { title: "Volúmenes", sub: "Donde viven los datos: Btrfs, cifrado opcional" },
  disks: { title: "Discos", sub: "Identidad, SMART y temperatura" },
  snapshots: { title: "Instantáneas", sub: "Puntos de restauración por carpeta" },
  iscsi: { title: "iSCSI", sub: "Discos de red para máquinas virtuales y servidores" },
};

export default function StorageApp({ win }: AppProps) {
  const realData = useIsReal("storage.disks.list");
  const t = useTransport();
  const disks = useLive("storage.disks.list", undefined, ["storage.changed"]).data ?? [];
  const vols = useLive("storage.volumes.list", undefined, ["storage.changed"]).data ?? [];
  const poolsOn = useAvailable("storage.pools.list");
  const pools = useLive(poolsOn ? "storage.pools.list" : "storage.volumes.list", undefined, ["storage.changed"]).data ?? [];
  // Sin grupos en el servidor se pide otra lista (los hooks no pueden ser condicionales): se descarta.
  const poolList = poolsOn && (pools.length === 0 || "tiers" in pools[0]) ? (pools as Pool[]) : [];
  const [creatingPool, setCreatingPool] = useState(false);
  const [creatingIn, setCreatingIn] = useState<string | null | false>(false);
  // Crear un volumen: dentro de un grupo con espacio; si no hay, primero el grupo (ADR 0011).
  const createVolume = (pool?: string) => {
    if (!poolsOn) return setCreating(true);
    if (poolList.some((p) => p.free >= 256 * 1024 ** 2)) setCreatingIn(pool ?? null);
    else setCreatingPool(true);
  };
  const [section, setSection] = useState<Section>("overview");
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [replacing, setReplacing] = useState<Volume | null>(null);
  const [expanding, setExpanding] = useState<Volume | null>(null);

  useEffect(() => {
    if (win.args.action === "create") createVolume();
    if (win.args.section === "iscsi") setSection("iscsi");
  }, [win.args]);

  const v1 = vols[0];
  useWindowMenus(
    win,
    [
      {
        label: "Volumen",
        items: [
          { label: "Crear volumen…", action: () => createVolume() },
          ...(poolsOn ? [{ label: "Crear grupo de almacenamiento…", action: () => setCreatingPool(true) }, { label: "Volumen clásico (sin grupo)…", action: () => setCreating(true) }] : []),
          {
            label: v1 ? `Verificar ${v1.name}` : "Verificar volumen",
            disabled: !v1 || v1.state !== "healthy",
            action: () => v1 && t.call("storage.scrub.start", { volume: v1.id }).catch((e: Error) => toast({ level: "warn", title: "No se pudo verificar", body: e.message })),
          },
          { label: "Crear instantánea…", action: () => setSection("snapshots") },
          { separator: true },
          { label: "Resumen", checked: section === "overview", action: () => setSection("overview") },
          { label: "Discos", checked: section === "disks", action: () => setSection("disks") },
        ],
      },
    ],
    [v1?.id, v1?.state, section, poolsOn, poolList.length],
  );

  const openDisk = (d: Disk) => {
    setSelected(d.id);
    setSection("disks");
  };

  const failed = disks.some((d) => d.use === "failed");
  const { title, sub } = titles[section];

  return (
    <div className="tg-app ts-app" style={{ ["--side" as string]: `${SIDE}px` }}>
      <nav className="tg-app-side tg-sidebar" aria-label="Secciones">
        <h4>Almacenamiento</h4>
        <SidebarItem icon={LayoutDashboard} label="Resumen" current={section === "overview"} onClick={() => setSection("overview")} />
        {poolsOn && <SidebarItem icon={Layers} label="Grupos" current={section === "pools"} meta={poolList.length || undefined} onClick={() => setSection("pools")} />}
        <SidebarItem icon={Database} label="Volúmenes" current={section === "volumes"} meta={vols.length || undefined} onClick={() => setSection("volumes")} />
        <SidebarItem icon={HardDrive} label="Discos" current={section === "disks"} meta={failed ? <span className="ts-side-alert">1</span> : disks.length} onClick={() => setSection("disks")} />
        <SidebarItem icon={Camera} label="Instantáneas" current={section === "snapshots"} onClick={() => setSection("snapshots")} />
        <h4>Red de almacenamiento</h4>
        <SidebarItem icon={Network} label="iSCSI" current={section === "iscsi"} onClick={() => setSection("iscsi")} />
      </nav>
      <div className="tg-app-main">
        <WindowToolbar win={win} inset={SIDE}>
          <div className="ts-tb-titles">
            <span className="tg-tb-title">{title}</span>
            <span className="tg-tb-sub">{sub}</span>
          </div>
          <span className="tg-tb-spacer" />
          {!realData && <DemoBadge />}
          {poolsOn && (
            <Button onClick={() => setCreatingPool(true)}>
              <Layers size={14} /> Crear grupo
            </Button>
          )}
          <Button onClick={() => createVolume()}>
            <Plus size={14} /> Crear volumen
          </Button>
        </WindowToolbar>
        <div className="tg-app-content">
          <div className="tg-app-pad">
            {section === "overview" && <Overview disks={disks} vols={vols} onDisk={openDisk} onCreate={() => createVolume()} onReplace={setReplacing} />}
            {section === "pools" && <Pools pools={poolList} disks={disks} vols={vols} onCreatePool={() => setCreatingPool(true)} onCreateVolume={(p) => createVolume(p)} />}
            {section === "volumes" && <Volumes disks={disks} vols={vols} pools={poolList} onCreate={() => createVolume()} onReplace={setReplacing} onExpand={setExpanding} />}
            {section === "disks" && <Disks disks={disks} vols={vols} pools={poolList} selected={selected} onSelect={setSelected} />}
            {section === "snapshots" && <Snapshots />}
            {section === "iscsi" && <Iscsi vols={vols} />}
          </div>
        </div>
      </div>
      <div className="ts-sheet-host">
        <CreateVolume open={creating} onClose={() => setCreating(false)} disks={disks} vols={vols} />
        <CreatePool open={creatingPool} onClose={() => setCreatingPool(false)} disks={disks} vols={vols} pools={poolList} onCreated={() => setCreatingIn(null)} />
        <CreatePoolVolume open={creatingIn !== false} onClose={() => setCreatingIn(false)} pools={poolList} vols={vols} initialPool={creatingIn || null} />
        <ReplaceDisk vol={replacing} disks={disks} onClose={() => setReplacing(null)} />
        <ExpandVolume vol={expanding} disks={disks} onClose={() => setExpanding(null)} />
      </div>
    </div>
  );
}
