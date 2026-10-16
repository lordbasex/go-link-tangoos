// Fase M4: producto (protocol/README.md §12). Se suma a Methods por module augmentation para no
// chocar con otros cambios en protocol.ts.

export interface SetupState {
  state: "pending" | "done";
  hostname: string;
  suggestedHostname: string;
}

export type CertMode = "selfsigned" | "localca" | "acme";

export interface SetupParams {
  hostname: string;
  admin: { name: string; full: string; password: string };
  timezone: string;
  certificate: "selfsigned" | "localca";
}

export interface DebianUpdate {
  name: string;
  from: string;
  to: string;
  security: boolean;
}

export interface UpdatesInfo {
  tangoos: { current: string; available: string | null };
  debian: DebianUpdate[];
  checkedAt: string;
}

export interface RollbackPoint {
  id: string;
  at: string;
  label: string;
}

export interface CertInfo {
  mode: CertMode;
  subject: string;
  sans: string[];
  notAfter: string;
  fingerprint: string;
}

export type ServiceId = "smb" | "nfs" | "sftp" | "webdav" | "iscsi";

export interface ServiceInfo {
  id: ServiceId;
  enabled: boolean;
  running: boolean;
  port: number | null;
  detail: string;
}

export interface NfsClient {
  host: string;
  access: "rw" | "ro";
}

export interface IscsiTarget {
  name: string;
  iqn: string;
  /** Volume.id (archivo), "zfs:<pool>" (zvol) o Pool.id (LV). */
  volume: string;
  sizeBytes: number;
  chap: boolean;
  sessions: number;
  /** LUN sobre un volumen de bloque ZFS. */
  zvol?: string;
  /** LUN sobre un volumen lógico de un grupo (/dev/<grupo>/il_<nombre>). */
  lv?: string;
  thin?: boolean;
}

export interface NotifyChannels {
  email: { enabled: boolean; host: string; port: number; user: string; password: string; from: string; to: string; tls: "starttls" | "tls" | "none" };
  ntfy: { enabled: boolean; server: string; topic: string; token: string };
  telegram: { enabled: boolean; botToken: string; chatId: string };
  levels: ("crit" | "warn" | "info" | "ok")[];
}

export interface AuditEvent {
  at: string;
  actor: string;
  ip: string;
  client: string;
  method: string;
  target: string;
  result: "ok" | "error" | "denied";
  job?: string;
}

export type LogUnit = "tangod" | "tango-agent" | "smbd" | "docker" | "kernel";

export interface LogLine {
  at: string;
  unit: string;
  /** Prioridad syslog: 0 emerg … 7 debug. */
  prio: number;
  msg: string;
}

declare module "./protocol" {
  interface Methods {
    "setup.state": { params: void; result: SetupState };
    "setup.complete": { params: SetupParams; result: { user: User; token: string } };
    "system.updates.check": { params: void; result: UpdatesInfo };
    "system.updates.apply": { params: { scope: "all" | "security" }; result: { job: string } };
    "system.rollback.list": { params: void; result: RollbackPoint[] };
    "system.rollback.apply": { params: { id: string; typed: string }; result: { job: string } };
    "system.reboot": { params: { typed: string }; result: Record<string, never> };
    "system.shutdown": { params: { typed: string }; result: Record<string, never> };
    "cert.info": { params: void; result: CertInfo };
    "cert.localca": { params: void; result: { job: string } };
    "cert.caDownload": { params: void; result: { filename: string; pem: string } };
    "services.list": { params: void; result: ServiceInfo[] };
    "services.set": { params: { id: ServiceId; enabled: boolean; options?: Record<string, unknown> }; result: { job: string } };
    "nfs.exports.get": { params: { share: string }; result: { clients: NfsClient[] } };
    "nfs.exports.set": { params: { share: string; clients: NfsClient[] }; result: Record<string, never> };
    "iscsi.targets.list": { params: void; result: IscsiTarget[] };
    /** Un LUN va en un archivo de `volume`, en el zvol `zvol` o en un LV nuevo del grupo `pool` (`thin` opcional). */
    "iscsi.targets.create": {
      params: { name: string; volume?: string; sizeBytes: number; chap?: { user: string; secret: string }; zvol?: string; pool?: string; thin?: boolean };
      result: { job: string };
    };
    "iscsi.targets.delete": { params: { name: string; typed: string }; result: { job: string } };
    "notify.channels.get": { params: void; result: NotifyChannels };
    "notify.channels.set": { params: NotifyChannels; result: Record<string, never> };
    "notify.test": { params: { channel: "email" | "ntfy" | "telegram" }; result: Record<string, never> };
    "audit.list": { params: { q?: string; since?: string; limit: number }; result: AuditEvent[] };
    "logs.query": { params: { unit?: LogUnit; q?: string; since?: string; limit: number }; result: LogLine[] };
    "term.open": { params: { cols: number; rows: number }; result: { id: number } };
    "term.resize": { params: { id: number; cols: number; rows: number }; result: Record<string, never> };
    "term.close": { params: { id: number }; result: Record<string, never> };
    "apps.ticket": { params: { id: string }; result: { url: string } };
    "auth.challenge": { params: { tokenId: string; nonce: string }; result: { proof: string } };
  }
  interface Topics {
    "audit.event": AuditEvent;
    "services.changed": { id?: ServiceId };
  }
}

// ---- Fuerza de contraseña (sin dependencias; heurística honesta, no zxcvbn) -----------------

export interface PasswordStrength {
  score: 0 | 1 | 2 | 3 | 4;
  label: string;
  hint: string;
}

const COMMON = ["123456", "password", "contraseña", "qwerty", "admin", "tango", "111111", "abc123", "iloveyou", "argentina"];

export function passwordStrength(pw: string, context: string[] = []): PasswordStrength {
  if (!pw) return { score: 0, label: "", hint: "Usá al menos 10 caracteres." };
  const lower = pw.toLowerCase();
  if (COMMON.some((c) => lower.includes(c)) || context.some((c) => c && c.length >= 3 && lower.includes(c.toLowerCase())))
    return { score: 0, label: "Muy débil", hint: "Evitá palabras comunes o tu nombre." };
  let classes = 0;
  if (/[a-z]/.test(pw)) classes++;
  if (/[A-Z]/.test(pw)) classes++;
  if (/\d/.test(pw)) classes++;
  if (/[^A-Za-z0-9]/.test(pw)) classes++;
  const unique = new Set(pw).size;
  let s = 0;
  if (pw.length >= 10) s++;
  if (pw.length >= 14) s++;
  if (classes >= 3) s++;
  if (pw.length >= 18 || (classes === 4 && pw.length >= 12)) s++;
  if (unique < pw.length / 3) s = Math.max(0, s - 2);
  if (pw.length < 10) s = Math.min(s, 1);
  const score = Math.min(4, s) as PasswordStrength["score"];
  const labels = ["Muy débil", "Débil", "Aceptable", "Fuerte", "Muy fuerte"];
  const hints = [
    "Usá al menos 10 caracteres.",
    "Sumá largo: una frase de varias palabras funciona bien.",
    "Bien. Mezclar mayúsculas, números o símbolos la hace más fuerte.",
    "Buena contraseña.",
    "Excelente.",
  ];
  return { score, label: labels[score], hint: hints[score] };
}

/** Nombre de host válido (RFC 1123, una etiqueta): minúsculas, números y guiones. */
export function validHostname(h: string): string | null {
  if (!h) return "Elegí un nombre.";
  if (h.length > 63) return "Máximo 63 caracteres.";
  if (!/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(h)) return "Solo minúsculas, números y guiones (sin empezar ni terminar con guion).";
  return null;
}
