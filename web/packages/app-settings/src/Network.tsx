import { useState } from "react";
import { EthernetPort } from "lucide-react";
import { useLive, useMetrics, fmtRate, t } from "@tango/client";
import { Button, Group, Row, Segmented, Status, TextInput } from "@tango/ui";
import { Value, demoApply } from "./common";

export function NetworkSection() {
  const info = useLive("system.info", undefined).data;
  const m = useMetrics();
  const last = m[m.length - 1];
  const [mode, setMode] = useState<"dhcp" | "static">("dhcp");
  const [host, setHost] = useState("tango");

  return (
    <>
      <Group title={t("Interfaces")}>
        <Row
          icon={
            <span className="tset-row-icon" style={{ background: "#2F7CF6" }}>
              <EthernetPort size={15} />
            </span>
          }
          label="enp1s0 · Ethernet"
          hint={t("{ip} · 2,5 GbE full duplex · MAC {mac}", { ip: info?.ip ?? "—", mac: "7c:2b:e1:13:4a:9f" })}
        >
          <Status tone="ok">{t("Conectada")}</Status>
        </Row>
        <Row
          icon={
            <span className="tset-row-icon" style={{ background: "#8E8E96" }}>
              <EthernetPort size={15} />
            </span>
          }
          label="enp2s0 · Ethernet"
          hint={t("Sin cable · MAC {mac}", { mac: "7c:2b:e1:13:4a:a0" })}
        >
          <span className="tg-muted">{t("Desconectada")}</span>
        </Row>
        <Row label={t("Tráfico ahora")}>
          <Value>
            ↓ {fmtRate(last?.netRx ?? 0)} · ↑ {fmtRate(last?.netTx ?? 0)}
          </Value>
        </Row>
      </Group>

      <Group title="enp1s0">
        <Row label={t("Configurar IPv4")}>
          <Segmented
            label={t("Configurar IPv4")}
            value={mode}
            onChange={setMode}
            options={[
              { value: "dhcp", label: t("Automático (DHCP)") },
              { value: "static", label: t("Manual") },
            ]}
          />
        </Row>
        <Row label={t("Dirección IP")}>
          <TextInput defaultValue={info?.ip ?? ""} disabled={mode === "dhcp"} style={{ width: 160 }} aria-label={t("Dirección IP")} />
        </Row>
        <Row label={t("Máscara de subred")}>
          <TextInput defaultValue="255.255.255.0" disabled={mode === "dhcp"} style={{ width: 160 }} aria-label={t("Máscara")} />
        </Row>
        <Row label={t("Puerta de enlace")}>
          <TextInput defaultValue="192.168.1.1" disabled={mode === "dhcp"} style={{ width: 160 }} aria-label={t("Puerta de enlace")} />
        </Row>
        <Row label="DNS">
          <TextInput defaultValue="192.168.1.1, 1.1.1.1" disabled={mode === "dhcp"} style={{ width: 160 }} aria-label="DNS" />
        </Row>
      </Group>

      <Group title={t("Nombre en la red")}>
        <Row label={t("Nombre del NAS")} hint={t("Se anuncia como {host}.local en macOS, Windows y Linux", { host: host || "tango" })}>
          <TextInput value={host} onChange={(e) => setHost(e.target.value.replace(/[^a-z0-9-]/gi, "").toLowerCase())} style={{ width: 160 }} aria-label={t("Nombre del NAS")} />
        </Row>
      </Group>
      <div className="tset-actions">
        <Button variant="primary" onClick={() => demoApply(t("La configuración de red"))}>
          {t("Aplicar")}
        </Button>
      </div>
    </>
  );
}
