import { useState } from "react";
import { Fan, Thermometer, Timer } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useMetrics, fmtCpuTemp, fmtRpm } from "@tango/client";
import { Button, Group, Row, Segmented, Toggle } from "@tango/ui";
import { Value, demoApply } from "./common";
import { UpsCard } from "./Ups";

function Icon({ icon: I, color }: { icon: LucideIcon; color: string }) {
  return (
    <span className="tset-row-icon" style={{ background: color }}>
      <I size={15} />
    </span>
  );
}

export function Hardware() {
  const m = useMetrics();
  const last = m[m.length - 1];
  const [fan, setFan] = useState<"quiet" | "balanced" | "cool">("balanced");
  const [sched, setSched] = useState(false);
  const [hdd, setHdd] = useState(true);

  return (
    <>
      <Group title="Refrigeración">
        <Row icon={<Icon icon={Thermometer} color="#F97316" />} label="Temperatura del procesador">
          <Value>{fmtCpuTemp(last?.tempCpu)}</Value>
        </Row>
        <Row icon={<Icon icon={Fan} color="#0ea5e9" />} label="Ventilador" hint={`${fmtRpm(last?.fanRpm)} ahora`}>
          <Segmented
            label="Modo del ventilador"
            value={fan}
            onChange={(v) => (setFan(v), demoApply("El modo del ventilador"))}
            options={[
              { value: "quiet", label: "Silencioso" },
              { value: "balanced", label: "Equilibrado" },
              { value: "cool", label: "Frío" },
            ]}
          />
        </Row>
      </Group>

      <Group title="Discos">
        <Row label="Detener discos inactivos" hint="Los discos rígidos se detienen después de 20 minutos sin uso">
          <Toggle checked={hdd} onChange={(v) => (setHdd(v), demoApply("La hibernación de discos"))} label="Detener discos inactivos" />
        </Row>
      </Group>

      <UpsCard />
      <Group title="Energía">
        <Row icon={<Icon icon={Timer} color="#8B5CF6" />} label="Encendido y apagado programados" hint={sched ? "Apaga a las 01:00 y enciende a las 07:30, todos los días" : "Siempre encendido"}>
          <Toggle checked={sched} onChange={setSched} label="Programar" />
        </Row>
        <Row label="Al volver la luz" hint="Qué hace el NAS después de un corte">
          <Value>Encenderse solo</Value>
        </Row>
      </Group>
      <div className="tset-actions">
        <Button variant="primary" onClick={() => demoApply("La configuración de energía")}>
          Aplicar
        </Button>
      </div>
    </>
  );
}
