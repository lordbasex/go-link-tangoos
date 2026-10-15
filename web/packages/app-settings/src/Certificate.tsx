import { useState } from "react";
import { Download, KeyRound } from "lucide-react";
import { RpcError, fmtDate, saveBlob, useIsReal, useLive, useTransport } from "@tango/client";
import { toast } from "@tango/shell";
import { Button, DemoBadge, Group, Row, Segmented, Status } from "@tango/ui";
import { Value } from "./common";
import { JobLog, useJobLog } from "./job";

type OS = "mac" | "win" | "linux" | "mobile";

const STEPS: Record<OS, string[]> = {
  mac: ["Abrí tangoos-ca.crt: se agrega al llavero «Inicio de sesión».", "En Acceso a Llaveros, abrí «TangoOS CA», desplegá «Confiar» y elegí «Confiar siempre».", "Cerrá y volvé a abrir el navegador."],
  win: ["Abrí tangoos-ca.crt y elegí «Instalar certificado…».", "Elegí «Equipo local» → «Colocar todos los certificados en el siguiente almacén» → «Entidades de certificación raíz de confianza».", "Reiniciá el navegador."],
  linux: ["Debian/Ubuntu: sudo cp tangoos-ca.crt /usr/local/share/ca-certificates/ && sudo update-ca-certificates", "Fedora: sudo cp tangoos-ca.crt /etc/pki/ca-trust/source/anchors/ && sudo update-ca-trust", "Firefox usa su propio almacén: Ajustes → Privacidad → Certificados → Importar."],
  mobile: ["iPhone/iPad: abrí el archivo, instalá el perfil y activalo en Ajustes → General → Información → Confianza de certificados.", "Android: Ajustes → Seguridad → Cifrado y credenciales → Instalar un certificado → Certificado de CA."],
};

export function Certificate() {
  const t = useTransport();
  const real = useIsReal("cert.info");
  const { data: cert, reload } = useLive("cert.info", undefined);
  const [os, setOs] = useState<OS>(() => (/Mac/.test(navigator.platform) ? "mac" : /Win/.test(navigator.platform) ? "win" : "linux"));
  const log = useJobLog((j) => {
    reload();
    if (j.state === "succeeded") toast({ level: "ok", title: "CA local activa", body: "Descargá el certificado raíz e instalalo en tus equipos.", app: "tango.settings" });
  });

  const useLocal = async () => {
    try {
      const { job } = await t.call("cert.localca", undefined);
      log.start(job, "Generando la autoridad certificante…");
    } catch (e) {
      toast({ level: "crit", title: "No se pudo crear la CA local", body: e instanceof RpcError ? e.message : String(e), app: "tango.settings" });
    }
  };

  const download = async () => {
    try {
      const r = await t.call("cert.caDownload", undefined);
      saveBlob(new Blob([r.pem], { type: "application/x-x509-ca-cert" }), r.filename);
    } catch (e) {
      toast({ level: "warn", title: "No se pudo descargar", body: e instanceof RpcError ? e.message : String(e), app: "tango.settings" });
    }
  };

  const modeLabel = cert?.mode === "localca" ? "CA local de TangoOS" : cert?.mode === "acme" ? "Let's Encrypt" : "Autofirmado";

  return (
    <>
      <Group
        title={
          <>
            Certificado HTTPS {!real && <DemoBadge />}
          </>
        }
      >
        <Row label="Origen" hint={cert?.mode === "selfsigned" ? "El navegador muestra una advertencia y no se pueden ver videos ni hacer descargas grandes en streaming." : "Instalá la raíz en cada equipo una sola vez."}>
          <Status tone={cert?.mode === "selfsigned" ? "warn" : "ok"}>{modeLabel}</Status>
        </Row>
        <Row label="Nombres" hint={cert?.subject}>
          <Value>{cert?.sans.join(", ") ?? "—"}</Value>
        </Row>
        <Row label="Vence">
          <Value>{cert ? fmtDate(cert.notAfter) : "—"}</Value>
        </Row>
        <Row label="Huella" hint={<span className="tset-mono tset-fp">{cert?.fingerprint ?? "—"}</span>} />
      </Group>

      {log.id && <JobLog job={log.job} lines={log.lines} failed={log.failed} />}

      <div className="tset-actions">
        {cert?.mode !== "localca" && (
          <Button variant="primary" onClick={() => void useLocal()} disabled={log.running}>
            <KeyRound size={14} /> Usar CA local
          </Button>
        )}
        {cert?.mode === "localca" && (
          <Button variant="primary" onClick={() => void download()}>
            <Download size={14} /> Descargar certificado raíz
          </Button>
        )}
      </div>

      {cert?.mode === "localca" && (
        <Group title="Cómo instalarlo">
          <div className="tset-osteps">
            <Segmented
              label="Sistema"
              value={os}
              onChange={setOs}
              options={[
                { value: "mac", label: "macOS" },
                { value: "win", label: "Windows" },
                { value: "linux", label: "Linux" },
                { value: "mobile", label: "iOS y Android" },
              ]}
            />
            <ol>
              {STEPS[os].map((s) => (
                <li key={s}>{s}</li>
              ))}
            </ol>
          </div>
        </Group>
      )}
    </>
  );
}
