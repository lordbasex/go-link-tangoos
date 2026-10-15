import "./storage.css";
import { useEffect, useState } from "react";
import { Camera, Database, HardDrive, LayoutDashboard, Network, Plus } from "lucide-react";
import { useLive, useTransport, useIsReal } from "@tango/client";
import type { Disk, Volume } from "@tango/client";
import { Button, DemoBadge, SidebarItem } from "@tango/ui";
import { WindowToolbar, toast, useWindowMenus } from "@tango/shell";
import type { AppProps } from "@tango/shell";
import { Overview } from "./Overview";
import { Disks, Snapshots, Volumes } from "./Sections";
import { CreateVolume } from "./CreateVolume";
import { ExpandVolume, ReplaceDisk } from "./Maintenance";
import type { Section } from "./util";
import { Iscsi } from "./Iscsi";

const SIDE = 210;

const titles: Record<Section, { title: string; sub: string }> = {
  overview: { title: "Resumen", sub: "Estado del almacenamiento" },
  volumes: { title: "Volúmenes", sub: "Arrays mdadm con Btrfs" },
  disks: { title: "Discos", sub: "Identidad, SMART y temperatura" },
  snapshots: { title: "Instantáneas", sub: "Puntos de restauración por carpeta" },
  iscsi: { title: "iSCSI", sub: "Discos de red para máquinas virtuales y servidores" },
};

export default function StorageApp({ win }: AppProps) {
  const realData = useIsReal("storage.disks.list");
  const t = useTransport();
  const disks = useLive("storage.disks.list", undefined, ["storage.changed"]).data ?? [];
  const vols = useLive("storage.volumes.list", undefined, ["storage.changed"]).data ?? [];
  const [section, setSection] = useState<Section>("overview");
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [replacing, setReplacing] = useState<Volume | null>(null);
  const [expanding, setExpanding] = useState<Volume | null>(null);

  useEffect(() => {
    if (win.args.action === "create") setCreating(true);
    if (win.args.section === "iscsi") setSection("iscsi");
  }, [win.args]);

  const v1 = vols[0];
  useWindowMenus(
    win,
    [
      {
        label: "Volumen",
        items: [
          { label: "Crear volumen…", action: () => setCreating(true) },
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
    [v1?.id, v1?.state, section],
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
          <Button onClick={() => setCreating(true)}>
            <Plus size={14} /> Crear volumen
          </Button>
        </WindowToolbar>
        <div className="tg-app-content">
          <div className="tg-app-pad">
            {section === "overview" && <Overview disks={disks} vols={vols} onDisk={openDisk} onCreate={() => setCreating(true)} onReplace={setReplacing} />}
            {section === "volumes" && <Volumes disks={disks} vols={vols} onCreate={() => setCreating(true)} onReplace={setReplacing} onExpand={setExpanding} />}
            {section === "disks" && <Disks disks={disks} vols={vols} selected={selected} onSelect={setSelected} />}
            {section === "snapshots" && <Snapshots />}
            {section === "iscsi" && <Iscsi vols={vols} />}
          </div>
        </div>
      </div>
      <div className="ts-sheet-host">
        <CreateVolume open={creating} onClose={() => setCreating(false)} disks={disks} vols={vols} />
        <ReplaceDisk vol={replacing} disks={disks} onClose={() => setReplacing(null)} />
        <ExpandVolume vol={expanding} disks={disks} onClose={() => setExpanding(null)} />
      </div>
    </div>
  );
}
