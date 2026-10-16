// Idiomas de la interfaz (español, que es la fuente, e inglés). El texto en español es la
// clave: `t("Crear volumen")` devuelve "Create volume" con la interfaz en inglés y el mismo
// texto en español. Cada paquete registra su catálogo con `registerEn` (archivo
// i18n.en.ts). Los mensajes que llegan del NAS (errores, avisos, jobs) vienen en español:
// `tr` los traduce por coincidencia exacta o con los formatos de Go (%s, %d, %v, %q)
// registrados con `registerEnFormats`.

export type Lang = "es" | "en";

export const LANGS: { id: Lang; name: string }[] = [
  { id: "es", name: "Español" },
  { id: "en", name: "English" },
];

const KEY = "tango.lang";

function detect(): Lang {
  // Los tests unitarios esperan los textos fuente (español).
  if ((globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env?.VITEST) return "es";
  try {
    const saved = localStorage.getItem(KEY);
    if (saved === "es" || saved === "en") return saved;
  } catch {
    /* sin almacenamiento: idioma del navegador */
  }
  const nav = typeof navigator !== "undefined" ? (navigator.languages?.[0] ?? navigator.language ?? "es") : "es";
  return nav.toLowerCase().startsWith("es") ? "es" : "en";
}

let current: Lang = detect();
const en = new Map<string, string>();
// group[n] es el grupo de captura del argumento n (1-based) del formato de origen.
type Format = { re: RegExp; out: string; group: number[] };
const formats: Format[] = [];

/** Idioma actual de la interfaz. */
export function getLang(): Lang {
  return current;
}

/** Cambia el idioma (queda guardado en este navegador). La interfaz se vuelve a cargar. */
export function setLang(l: Lang, reload = true) {
  current = l;
  try {
    localStorage.setItem(KEY, l);
  } catch {
    /* sin almacenamiento: dura hasta recargar */
  }
  if (typeof document !== "undefined") document.documentElement.lang = l;
  if (reload && typeof location !== "undefined") location.reload();
}

/** Solo para tests y la demo. */
export function useLangForTests(l: Lang) {
  current = l;
}

/** Locale para Intl (fechas, números). */
export function locale(): string {
  return current === "en" ? "en-US" : "es-AR";
}

/** Claves registradas dos veces con traducciones distintas (un test exige que no haya). */
export const enConflicts: { key: string; a: string; b: string }[] = [];

/** Registra traducciones al inglés: { "texto en español": "English text" }. */
export function registerEn(c: Record<string, string>) {
  for (const k in c) {
    const prev = en.get(k);
    if (prev !== undefined && prev !== c[k]) enConflicts.push({ key: k, a: prev, b: c[k] });
    en.set(k, c[k]);
  }
}

const goVerb = /%(\[\d+\])?[-+#0]*\d*(\.\d+)?[sdvqwxXfgt]/g;

/** Registra formatos del NAS: { "no existe el volumen %s": "volume %s does not exist" }. */
export function registerEnFormats(c: Record<string, string>) {
  for (const raw in c) {
    // %% se imprime como %: se aparta antes de buscar los verbos.
    const k = raw.replace(/%%/g, "\u0000");
    const parts = k.split(goVerb);
    if (!goVerb.test(k)) {
      en.set(raw.replace(/%%/g, "%"), c[raw].replace(/%%/g, "%"));
      continue;
    }
    goVerb.lastIndex = 0;
    const verbs = k.match(goVerb) ?? [];
    const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    // split con grupos de captura deja undefined en las posiciones de los grupos.
    const literal = parts.filter((_, i) => i % 3 === 0).map((s) => s.replace(/\u0000/g, "%"));
    const re = new RegExp("^" + literal.map(esc).join("(.+?)") + "$", "s");
    const group: number[] = [];
    let next = 1;
    verbs.forEach((v, i) => {
      const pos = /^%\[(\d+)\]/.exec(v);
      const arg = pos ? Number(pos[1]) : next;
      next = arg + 1;
      group[arg] = i + 1;
    });
    formats.push({ re, out: c[raw].replace(/%%/g, "\u0000"), group });
  }
  // Los más largos primero: más específicos.
  formats.sort((a, b) => b.re.source.length - a.re.source.length);
}

function interpolate(s: string, vars?: Record<string, string | number>): string {
  if (!vars) return s;
  return s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));
}

/**
 * Texto de la interfaz. `vars` reemplaza {nombre} en el texto (en los dos idiomas). `ctx`
 * distingue la misma palabra con dos sentidos ("Grupo" de usuarios o de almacenamiento): la
 * clave del catálogo es "ctx|texto"; sin esa clave se usa la del texto solo.
 */
export function t(es: string, vars?: Record<string, string | number>, ctx?: string): string {
  if (current !== "en") return interpolate(es, vars);
  return interpolate((ctx ? en.get(ctx + "|" + es) : undefined) ?? en.get(es) ?? es, vars);
}

/** Elige entre singular y plural según n ({n} se reemplaza). */
export function tn(n: number, one: string, many: string, vars?: Record<string, string | number>, ctx?: string): string {
  return t(n === 1 ? one : many, { n, ...vars }, ctx);
}

/** Mensaje que llegó del NAS (o de la demo) en español. */
export function tr(msg: string | undefined | null): string {
  if (!msg || current !== "en") return msg ?? "";
  const exact = en.get(msg);
  if (exact !== undefined) return exact;
  // Mensajes compuestos "contexto: detalle" (agentMessage, errores envueltos).
  for (const f of formats) {
    const m = f.re.exec(msg);
    if (!m) continue;
    let next = 1;
    return f.out.replace(goVerb, (v) => {
      const pos = /^%\[(\d+)\]/.exec(v);
      const n = pos ? Number(pos[1]) : next;
      next = n + 1;
      const arg = m[f.group[n] ?? n];
      return arg === undefined ? v : tr(arg);
    }).replace(/\u0000/g, "%");
  }
  const colon = msg.indexOf(": ");
  if (colon > 0) {
    const head = en.get(msg.slice(0, colon));
    if (head !== undefined) return head + ": " + tr(msg.slice(colon + 2));
  }
  return msg;
}
