import { useEffect, useState } from "react";
import { fmtBytes, fmtDuration, useLive, fmtMem } from "@tango/client";
import { Button, Group, Row, Status } from "@tango/ui";
import { TangoLogo } from "@tango/theme-milonga";
import { Value, demoApply } from "./common";

export function General() {
  const info = useLive("system.info", undefined).data;
  const vols = useLive("storage.volumes.list", undefined, ["storage.changed"]).data ?? [];
  const [up, setUp] = useState(0);
  useEffect(() => {
    if (!info) return;
    setUp(info.uptimeSec);
    const t = setInterval(() => setUp((u) => u + 60), 60_000);
    return () => clearInterval(t);
  }, [info]);

  if (!info) return null;
  const total = vols.reduce((a, v) => a + v.size, 0);
  const used = vols.reduce((a, v) => a + v.used, 0);

  return (
    <>
      <div className="tset-about">
        <div className="tset-about-logo">
          <TangoLogo size={54} color="#fff" />
        </div>
        <h1>TangoOS</h1>
        <div className="tset-about-ver">Versión {info.version}</div>
        <div className="tset-about-model">{info.model}</div>
      </div>

      <Group title="Acerca de este NAS">
        <Row label="Nombre">
          <Value>{info.hostname}.local</Value>
        </Row>
        <Row label="Procesador">
          <Value>
            {info.cpu} · {info.cores} núcleos
          </Value>
        </Row>
        <Row label="Memoria">
          <Value>{fmtMem(info.memTotal)} DDR5</Value>
        </Row>
        <Row label="Almacenamiento">
          <Value>{total ? `${fmtBytes(total - used)} libres de ${fmtBytes(total)}` : "Sin volúmenes"}</Value>
        </Row>
        <Row label="Número de serie">
          <Value>{info.serial}</Value>
        </Row>
        <Row label="Encendido desde hace">
          <Value>{fmtDuration(up)}</Value>
        </Row>
      </Group>

      <Group title="Software">
        <Row label="TangoOS" hint="Interfaz, gestión y tiempo real: tangod + tango-agent">
          <Value>{info.version}</Value>
        </Row>
        <Row label="Sistema base" hint="Recibe los parches de seguridad de Debian">
          <Value>{info.debian}</Value>
        </Row>
        <Row label="Kernel">
          <Value>{info.kernel}</Value>
        </Row>
        <Row label="Licencia">
          <Value>MIT · código abierto</Value>
        </Row>
      </Group>

      <Group title="Estado">
        <Row label="Servicios" hint="tangod, tango-agent, Samba, Avahi">
          <Status tone="ok">Funcionando</Status>
        </Row>
        <Row label="Nombre del NAS" hint="Cómo aparece en la red y en las apps">
          <Button onClick={() => demoApply("El nuevo nombre")}>Cambiar nombre…</Button>
        </Row>
      </Group>
      <p className="tset-foot">TangoOS es un proyecto independiente, no afiliado a Apple Inc. ni a Synology Inc.</p>
    </>
  );
}
