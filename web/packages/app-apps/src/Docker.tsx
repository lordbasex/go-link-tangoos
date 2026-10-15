import { Box, Container, HardDrive, Lock, Network } from "lucide-react";
import type { DockerStatus } from "@tango/client";
import { Button, Group, Row, Status } from "@tango/ui";
import { JobLog } from "./Detail";
import { isActive } from "./jobs";
import type { useTrackedJobs } from "./jobs";

type Jobs = ReturnType<typeof useTrackedJobs>;
type Run = (key: string, p: Promise<{ job: string }>, failTitle: string) => void;

const KEY = "__docker";

export function DockerHero({ jobs, run, install }: { jobs: Jobs; run: Run; install: () => Promise<{ job: string }> }) {
  const tj = jobs.get(KEY);
  const busy = isActive(tj);
  return (
    <div className="tg-app-pad tapp-hero">
      <div className="tapp-hero-art" aria-hidden="true">
        <Container size={46} strokeWidth={1.4} />
      </div>
      <h1 className="tg-h1">Instalá Docker para usar apps</h1>
      <p className="tapp-hero-lead">Las apps del catálogo corren aisladas en contenedores: cada una tiene su propio espacio, no toca el sistema y guarda sus datos en tu volumen.</p>
      <ul className="tapp-hero-points">
        <li>
          <Box size={16} /> Se instala desde los paquetes oficiales de Debian, en menos de un minuto.
        </li>
        <li>
          <Lock size={16} /> Con aislamiento de usuarios: un contenedor no corre como root del NAS.
        </li>
        <li>
          <HardDrive size={16} /> Los datos de cada app entran en las instantáneas del volumen.
        </li>
      </ul>
      {tj ? (
        <div className="tapp-hero-job">
          <JobLog tj={tj} />
        </div>
      ) : null}
      {!busy && (
        <Button variant="primary" size="lg" onClick={() => run(KEY, install(), "No se pudo instalar Docker")}>
          {tj ? "Reintentar" : "Instalar Docker"}
        </Button>
      )}
    </div>
  );
}

export function DockerSettings({ status, jobs, run, install }: { status: DockerStatus | undefined; jobs: Jobs; run: Run; install: () => Promise<{ job: string }> }) {
  const tj = jobs.get(KEY);
  return (
    <div className="tg-app-pad tapp-docker">
      <Group title="Docker Engine">
        <Row label="Estado">
          {!status ? (
            <span className="tg-muted">Consultando…</span>
          ) : status.installed ? (
            <Status tone={status.running ? "ok" : "warn"}>{status.running ? "En ejecución" : "Instalado, detenido"}</Status>
          ) : (
            <Status tone="info">No instalado</Status>
          )}
        </Row>
        <Row label="Versión">
          <span className="tg-muted">{status?.version ?? "—"}</span>
        </Row>
        <Row label="Aislamiento" hint="userns-remap: el root de un contenedor no es el root del NAS.">
          <Lock size={14} className="tg-muted" />
        </Row>
        <Row label="Redes" hint="Cada app tiene su red; solo las que lo necesitan usan la red del NAS.">
          <Network size={14} className="tg-muted" />
        </Row>
        {status && !status.installed && !isActive(tj) && (
          <Row label="Instalar Docker" hint="docker.io y docker-compose de Debian 13.">
            <Button variant="primary" onClick={() => run(KEY, install(), "No se pudo instalar Docker")}>
              Instalar
            </Button>
          </Row>
        )}
      </Group>
      {tj && (
        <div style={{ marginTop: 16 }}>
          <JobLog tj={tj} />
        </div>
      )}
    </div>
  );
}
