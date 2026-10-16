import { locale, t } from "./i18n";
const nf = (d: number) => new Intl.NumberFormat(locale(), { maximumFractionDigits: d });

/** Bytes en unidades decimales (como los fabricantes de discos y macOS). */
export function fmtBytes(n: number, digits = 1): string {
  const u = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  while (Math.abs(n) >= 1000 && i < u.length - 1) {
    n /= 1000;
    i++;
  }
  return `${nf(i === 0 ? 0 : n < 10 ? digits : n < 100 ? Math.min(digits, 1) : 0).format(n)} ${u[i]}`;
}

export const fmtRate = (bps: number) => `${fmtBytes(bps)}/s`;
export const fmtPct = (v: number, d = 0) => t("{v} %", { v: nf(d).format(v * 100) });

const rtf = new Intl.RelativeTimeFormat(locale(), { numeric: "auto" });
export function fmtAgo(isoDate: string): string {
  const s = (new Date(isoDate).getTime() - Date.now()) / 1000;
  const a = Math.abs(s);
  if (a < 45) return t("recién");
  if (a < 3600) return rtf.format(Math.round(s / 60), "minute");
  if (a < 86400) return rtf.format(Math.round(s / 3600), "hour");
  if (a < 86400 * 30) return rtf.format(Math.round(s / 86400), "day");
  return new Date(isoDate).toLocaleDateString(locale(), { day: "numeric", month: "short", year: "numeric" });
}

export function fmtDate(isoDate: string): string {
  return new Date(isoDate).toLocaleString(locale(), { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

export function fmtDuration(sec: number): string {
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return d ? `${d} d ${h} h` : h ? `${h} h ${m} min` : `${m} min`;
}

/** RAM en unidades binarias (como la muestran Linux, macOS y Windows), rotuladas "GB". */
export function fmtMem(n: number, digits = 1): string {
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) {
    n /= 1024;
    i++;
  }
  return `${nf(i < 3 ? 0 : digits).format(n)} ${u[i]}`;
}

/** Temperatura de un disco o sensor; `null` = el hardware no la informa. */
export const fmtTemp = (c: number | null | undefined) => (c == null ? "—" : `${Math.round(c)} °C`);

/** Máximo de temperaturas conocidas; `null` si ninguna se conoce. */
export function maxTemp(ts: (number | null | undefined)[]): number | null {
  const known = ts.filter((t): t is number => typeof t === "number");
  return known.length ? Math.max(...known) : null;
}

/** Sensores de la CPU/ventilador: 0 o negativo = el hardware no expone el sensor (p. ej. una VM). */
export const fmtCpuTemp = (v: number | undefined | null) => (v && v > 0 ? `${Math.round(v)} °C` : "—");
export const fmtRpm = (v: number | undefined | null) => (v && v > 0 ? `${new Intl.NumberFormat(locale()).format(v)} rpm` : "—");
