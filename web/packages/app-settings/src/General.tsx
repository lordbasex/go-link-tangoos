import { useEffect, useState } from "react";
import { fmtBytes, fmtDuration, useLive, fmtMem, t, tn } from "@tango/client";
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
    const iv = setInterval(() => setUp((u) => u + 60), 60_000);
    return () => clearInterval(iv);
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
        <div className="tset-about-ver">{t("Versión {v}", { v: info.version })}</div>
        <div className="tset-about-model">{info.model}</div>
      </div>

      <Group title={t("Acerca de este NAS")}>
        <Row label={t("Nombre")}>
          <Value>{info.hostname}.local</Value>
        </Row>
        <Row label={t("Procesador")}>
          <Value>
            {info.cpu} · {tn(info.cores, "{n} núcleo", "{n} núcleos")}
          </Value>
        </Row>
        <Row label={t("Memoria")}>
          <Value>{fmtMem(info.memTotal)} DDR5</Value>
        </Row>
        <Row label={t("Almacenamiento")}>
          <Value>{total ? t("{free} libres de {total}", { free: fmtBytes(total - used), total: fmtBytes(total) }) : t("Sin volúmenes")}</Value>
        </Row>
        <Row label={t("Número de serie")}>
          <Value>{info.serial}</Value>
        </Row>
        <Row label={t("Encendido desde hace")}>
          <Value>{fmtDuration(up)}</Value>
        </Row>
      </Group>

      <Group title={t("Software")}>
        <Row label="TangoOS" hint={t("Interfaz, gestión y tiempo real: tangod + tango-agent")}>
          <Value>{info.version}</Value>
        </Row>
        <Row label={t("Sistema base")} hint={t("Recibe los parches de seguridad de Debian")}>
          <Value>{info.debian}</Value>
        </Row>
        <Row label="Kernel">
          <Value>{info.kernel}</Value>
        </Row>
        <Row label={t("Licencia")}>
          <Value>{t("MIT · código abierto")}</Value>
        </Row>
      </Group>

      <Group title={t("Estado")}>
        <Row label={t("Servicios")} hint="tangod, tango-agent, Samba, Avahi">
          <Status tone="ok">{t("Funcionando")}</Status>
        </Row>
        <Row label={t("Nombre del NAS")} hint={t("Cómo aparece en la red y en las apps")}>
          <Button onClick={() => demoApply(t("El nuevo nombre"))}>{t("Cambiar nombre…")}</Button>
        </Row>
      </Group>
      <p className="tset-foot">{t("TangoOS es un proyecto independiente, no afiliado a Apple Inc. ni a ningún fabricante de NAS.")}</p>
    </>
  );
}
