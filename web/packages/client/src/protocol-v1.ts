// Fase v1.0 (protocol/README.md §13): verificación en dos pasos, UPS por NUT y lectura de
// instantáneas. Se suma a Methods/Topics por module augmentation, como protocol-m4.ts.

export interface TotpBegin {
  secret: string;
  /** otpauth://totp/… */
  uri: string;
  /** data:image/svg+xml;base64,… */
  qr: string;
}

export type UpsMode = "none" | "local" | "remote";

export interface UpsPolicy {
  action: "shutdown" | "notify";
  afterMinutes: number;
  belowPercent: number;
}

export interface UpsConfig {
  mode: UpsMode;
  host: string;
  port: number;
  upsName: string;
  user?: string;
  /** Vacío al guardar = conservar la actual; nunca vuelve en `get`. */
  password?: string;
  netServer: boolean;
  policy: UpsPolicy;
}

export interface UpsDetected {
  vendor: string;
  product: string;
  id: string;
}

export interface UpsStatus {
  mode: UpsMode;
  configured: boolean;
  online: boolean;
  name: string;
  model: string;
  /** ups.status de NUT: "OL", "OB DISCHRG", "OL CHRG LB"… */
  status: string;
  statusText: string;
  batteryPct: number | null;
  runtimeSec: number | null;
  loadPct: number | null;
  inputVoltage: number | null;
  lastUpdate: string;
  onBatterySince: string | null;
  detected: UpsDetected[];
  error?: string;
}

export const onBattery = (s: string) => s.split(/\s+/).includes("OB");

declare module "./protocol" {
  interface Methods {
    "auth.totp": { params: { code: string }; result: { user: User; token?: string } };
    "users.totp.begin": { params: void; result: TotpBegin };
    "users.totp.confirm": { params: { code: string }; result: { recoveryCodes: string[] } };
    "users.totp.disable": { params: { password: string }; result: Record<string, never> };
    "users.totp.reset": { params: { name: string }; result: Record<string, never> };
    "ups.status": { params: void; result: UpsStatus };
    "ups.config.get": { params: void; result: UpsConfig };
    "ups.config.set": { params: UpsConfig; result: { job: string } };
    "files.stat": { params: { path: string; snapshot?: string }; result: FileEntry };
    "shares.update": { params: { share: string; description?: string; smb?: boolean; timeMachine?: boolean; timeMachineMaxBytes?: number | null }; result: { job: string } };
  }
  interface Topics {
    "ups.changed": UpsStatus;
  }
}
