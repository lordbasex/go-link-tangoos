import { useState } from "react";
import { Fan, Thermometer, Timer } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useMetrics, fmtCpuTemp, fmtRpm, t } from "@tango/client";
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
      <Group title={t("Refrigeración")}>
        <Row icon={<Icon icon={Thermometer} color="#F97316" />} label={t("Temperatura del procesador")}>
          <Value>{fmtCpuTemp(last?.tempCpu)}</Value>
        </Row>
        <Row icon={<Icon icon={Fan} color="#0ea5e9" />} label={t("Ventilador")} hint={t("{rpm} ahora", { rpm: fmtRpm(last?.fanRpm) })}>
          <Segmented
            label={t("Modo del ventilador")}
            value={fan}
            onChange={(v) => (setFan(v), demoApply(t("El modo del ventilador")))}
            options={[
              { value: "quiet", label: t("Silencioso") },
              { value: "balanced", label: t("Equilibrado") },
              { value: "cool", label: t("Frío") },
            ]}
          />
        </Row>
      </Group>

      <Group title={t("Discos")}>
        <Row label={t("Detener discos inactivos")} hint={t("Los discos rígidos se detienen después de 20 minutos sin uso")}>
          <Toggle checked={hdd} onChange={(v) => (setHdd(v), demoApply(t("La hibernación de discos")))} label={t("Detener discos inactivos")} />
        </Row>
      </Group>

      <UpsCard />
      <Group title={t("Energía")}>
        <Row icon={<Icon icon={Timer} color="#8B5CF6" />} label={t("Encendido y apagado programados")} hint={sched ? t("Apaga a las 01:00 y enciende a las 07:30, todos los días") : t("Siempre encendido")}>
          <Toggle checked={sched} onChange={setSched} label={t("Programar")} />
        </Row>
        <Row label={t("Al volver la luz")} hint={t("Qué hace el NAS después de un corte")}>
          <Value>{t("Encenderse solo")}</Value>
        </Row>
      </Group>
      <div className="tset-actions">
        <Button variant="primary" onClick={() => demoApply(t("La configuración de energía"))}>
          {t("Aplicar")}
        </Button>
      </div>
    </>
  );
}
