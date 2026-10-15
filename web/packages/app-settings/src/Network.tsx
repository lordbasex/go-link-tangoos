import { useState } from "react";
import { EthernetPort } from "lucide-react";
import { useLive, useMetrics, fmtRate } from "@tango/client";
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
      <Group title="Interfaces">
        <Row
          icon={
            <span className="tset-row-icon" style={{ background: "#2F7CF6" }}>
              <EthernetPort size={15} />
            </span>
          }
          label="enp1s0 · Ethernet"
          hint={`${info?.ip ?? "—"} · 2,5 GbE full duplex · MAC 7c:2b:e1:13:4a:9f`}
        >
          <Status tone="ok">Conectada</Status>
        </Row>
        <Row
          icon={
            <span className="tset-row-icon" style={{ background: "#8E8E96" }}>
              <EthernetPort size={15} />
            </span>
          }
          label="enp2s0 · Ethernet"
          hint="Sin cable · MAC 7c:2b:e1:13:4a:a0"
        >
          <span className="tg-muted">Desconectada</span>
        </Row>
        <Row label="Tráfico ahora">
          <Value>
            ↓ {fmtRate(last?.netRx ?? 0)} · ↑ {fmtRate(last?.netTx ?? 0)}
          </Value>
        </Row>
      </Group>

      <Group title="enp1s0">
        <Row label="Configurar IPv4">
          <Segmented
            label="Configurar IPv4"
            value={mode}
            onChange={setMode}
            options={[
              { value: "dhcp", label: "Automático (DHCP)" },
              { value: "static", label: "Manual" },
            ]}
          />
        </Row>
        <Row label="Dirección IP">
          <TextInput defaultValue={info?.ip ?? ""} disabled={mode === "dhcp"} style={{ width: 160 }} aria-label="Dirección IP" />
        </Row>
        <Row label="Máscara de subred">
          <TextInput defaultValue="255.255.255.0" disabled={mode === "dhcp"} style={{ width: 160 }} aria-label="Máscara" />
        </Row>
        <Row label="Puerta de enlace">
          <TextInput defaultValue="192.168.1.1" disabled={mode === "dhcp"} style={{ width: 160 }} aria-label="Puerta de enlace" />
        </Row>
        <Row label="DNS">
          <TextInput defaultValue="192.168.1.1, 1.1.1.1" disabled={mode === "dhcp"} style={{ width: 160 }} aria-label="DNS" />
        </Row>
      </Group>

      <Group title="Nombre en la red">
        <Row label="Nombre del NAS" hint={`Se anuncia como ${host || "tango"}.local en macOS, Windows y Linux`}>
          <TextInput value={host} onChange={(e) => setHost(e.target.value.replace(/[^a-z0-9-]/gi, "").toLowerCase())} style={{ width: 160 }} aria-label="Nombre del NAS" />
        </Row>
      </Group>
      <div className="tset-actions">
        <Button variant="primary" onClick={() => demoApply("La configuración de red")}>
          Aplicar
        </Button>
      </div>
    </>
  );
}
