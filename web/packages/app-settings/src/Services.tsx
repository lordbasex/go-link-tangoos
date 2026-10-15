import { useState } from "react";
import { FolderTree, Globe, HardDrive, Monitor, Radar, Terminal } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { RpcError, useIsReal, useLive, useTransport } from "@tango/client";
import type { ServiceId, ServiceInfo } from "@tango/client";
import { openApp, toast } from "@tango/shell";
import { Button, DemoBadge, Group, Row, Status, Toggle } from "@tango/ui";
import { demoApply } from "./common";
import { useJobLog } from "./job";
import { TimeMachineHelp } from "./TimeMachine";

function Icon({ icon: I, color }: { icon: LucideIcon; color: string }) {
  return (
    <span className="tset-row-icon" style={{ background: color }}>
      <I size={15} />
    </span>
  );
}

const META: Record<ServiceId, { label: string; icon: LucideIcon; color: string; hint: string }> = {
  smb: { label: "SMB (Windows, macOS, Linux)", icon: Monitor, color: "#2F7CF6", hint: "Samba · cifrado SMB 3.1.1" },
  nfs: { label: "NFS", icon: FolderTree, color: "#F97316", hint: "NFS del kernel · para Linux, reproductores y VMs. Elegí qué equipos acceden en cada carpeta compartida." },
  sftp: { label: "SFTP", icon: Terminal, color: "#3a3d45", hint: "Cada usuario ve solo sus carpetas compartidas; sin acceso a una terminal" },
  webdav: { label: "WebDAV", icon: Globe, color: "#22C55E", hint: "Para montar carpetas por HTTPS desde cualquier sistema" },
  iscsi: { label: "iSCSI", icon: HardDrive, color: "#8B5CF6", hint: "Discos de red para máquinas virtuales y servidores (LIO)" },
};

export function Services() {
  const t = useTransport();
  const real = useIsReal("services.list");
  const { data: services, reload } = useLive("services.list", undefined, ["services.changed"]);
  const [pending, setPending] = useState<Partial<Record<ServiceId, boolean>>>({});
  const [disc, setDisc] = useState({ mdns: true, wsd: true });
  const [tmHelp, setTmHelp] = useState(false);
  const log = useJobLog(() => {
    setPending({});
    reload();
  });

  const toggle = async (s: ServiceInfo, enabled: boolean) => {
    setPending((p) => ({ ...p, [s.id]: enabled }));
    try {
      const { job } = await t.call("services.set", { id: s.id, enabled });
      log.start(job);
    } catch (e) {
      setPending((p) => ({ ...p, [s.id]: undefined }));
      toast({ level: "crit", title: `No se pudo ${enabled ? "activar" : "desactivar"} ${s.id.toUpperCase()}`, body: e instanceof RpcError ? e.message : String(e), app: "tango.settings" });
    }
  };

  return (
    <>
      <Group
        title={
          <>
            Compartir archivos {!real && <DemoBadge />}
          </>
        }
      >
        {(services ?? []).filter((s) => s.id in META).map((s) => {
          const m = META[s.id];
          const want = pending[s.id];
          const busy = want !== undefined;
          return (
            <Row key={s.id} icon={<Icon icon={m.icon} color={m.color} />} label={m.label} hint={`${s.detail || m.hint}${s.port ? ` · puerto ${s.port}` : ""}`}>
              {busy ? <Status tone="info">{want ? "Activando…" : "Deteniendo…"}</Status> : s.enabled ? s.running ? <Status tone="ok">Activo</Status> : <Status tone="crit">Detenido con error</Status> : null}
              {s.id === "smb" && s.enabled && (
                <Button variant="plain" onClick={() => setTmHelp(true)}>
                  Time Machine…
                </Button>
              )}
              {s.id === "iscsi" && s.enabled && (
                <Button variant="plain" onClick={() => openApp("tango.storage", { section: "iscsi" })}>
                  Destinos…
                </Button>
              )}
              <Toggle checked={want ?? s.enabled} disabled={busy} onChange={(v) => void toggle(s, v)} label={m.label} />
            </Row>
          );
        })}
      </Group>

      <Group title="Descubrimiento en la red">
        <Row icon={<Icon icon={Radar} color="#2F7CF6" />} label="Bonjour / mDNS" hint="Aparece como tango.local en macOS y Linux">
          <Toggle checked={disc.mdns} onChange={(v) => (setDisc((d) => ({ ...d, mdns: v })), demoApply("mDNS"))} label="mDNS" />
        </Row>
        <Row icon={<Icon icon={Radar} color="#0ea5e9" />} label="WS-Discovery" hint="Aparece en «Red» del Explorador de Windows">
          <Toggle checked={disc.wsd} onChange={(v) => (setDisc((d) => ({ ...d, wsd: v })), demoApply("WS-Discovery"))} label="WS-Discovery" />
        </Row>
      </Group>
      <TimeMachineHelp open={tmHelp} onClose={() => setTmHelp(false)} />
      <p className="tset-foot">La interfaz y la gestión las hace solo tangod. Samba, nfsd y LIO existen porque implementan protocolos que usan tus otras computadoras.</p>
    </>
  );
}
