import { useState } from "react";
import { Globe, Laptop, Monitor, Network, Terminal, Tv } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { fmtAgo, useLive } from "@tango/client";
import type { Session } from "@tango/client";
import { toast } from "@tango/shell";
import { Badge, Button, Group, Row, Status } from "@tango/ui";
import { Value } from "./common";
import { Certificate } from "./Certificate";
import { TwoFactor } from "./TwoFactor";
import { AppPasswords } from "./AppPasswords";

const clientInfo: Record<Session["client"], { label: string; icon: LucideIcon }> = {
  web: { label: "Navegador", icon: Globe },
  "desk-win": { label: "App de Windows", icon: Monitor },
  "desk-linux": { label: "App de Linux", icon: Laptop },
  smb: { label: "SMB", icon: Network },
  ssh: { label: "SSH", icon: Terminal },
  remote: { label: "Remoto (TangoConnect)", icon: Globe },
  guest: { label: "Enlace público", icon: Globe },
};

export function Security() {
  const sessions = useLive("sessions.list", undefined).data ?? [];
  const [closed, setClosed] = useState<string[]>([]);

  return (
    <>
      <Group title="Sesiones activas">
        {sessions
          .filter((s) => !closed.includes(s.id))
          .map((s) => {
            const c = clientInfo[s.client];
            const I = s.user === "tv-living" ? Tv : c.icon;
            return (
              <Row
                key={s.id}
                icon={
                  <span className="tset-row-icon" style={{ background: "#5d6270" }}>
                    <I size={15} />
                  </span>
                }
                label={
                  <span className="tset-uname">
                    {s.user} · {c.label}
                    {s.current && <Badge tone="info">Esta sesión</Badge>}
                  </span>
                }
                hint={`${s.ip} · desde ${fmtAgo(s.since)}`}
              >
                {!s.current && (
                  <Button
                    onClick={() => {
                      setClosed((x) => [...x, s.id]);
                      toast({ level: "ok", title: "Sesión cerrada", body: `Demo: ${s.user} en ${s.ip} tendría que volver a entrar.`, app: "tango.settings" });
                    }}
                  >
                    Cerrar sesión
                  </Button>
                )}
              </Row>
            );
          })}
      </Group>

      <Group title="Acceso">
        <TwoFactor />
        <AppPasswords />
        <Row label="Bloqueo por intentos fallidos" hint="5 intentos en 10 minutos bloquean esa IP por 30 minutos">
          <Status tone="ok">Activo</Status>
        </Row>
        <Row label="Cierre por inactividad">
          <Value>30 minutos</Value>
        </Row>
      </Group>

      <Group title="Firewall">
        <Row label="Acceso permitido" hint="Puertos 443, 8443 (WebRTC), 3478 (STUN), 445 (SMB), 2049 (NFS)">
          <Value>Solo la red local 192.168.1.0/24</Value>
        </Row>
        <Row label="SSH">
          <Value>Desactivado</Value>
        </Row>
      </Group>

      <Certificate />
    </>
  );
}
