// Código de vinculación de signalhub: 9 dígitos, se muestra "113 134 323".
export const CODE_LEN = 9;

/** Solo dígitos, máximo 9 (acepta pegado con espacios o guiones). */
export function normalizeCode(raw: string): string {
  return raw.replace(/\D/g, "").slice(0, CODE_LEN);
}

export function formatCode(raw: string): string {
  const d = normalizeCode(raw);
  return [d.slice(0, 3), d.slice(3, 6), d.slice(6, 9)].filter(Boolean).join(" ");
}

export function isCompleteCode(raw: string): boolean {
  return normalizeCode(raw).length === CODE_LEN;
}

/**
 * Aplica una edición a los dígitos de las casillas: escribir en la posición i reemplaza y avanza;
 * pegar varios dígitos los distribuye desde i. Devuelve los dígitos nuevos y el foco siguiente.
 */
export function applyDigits(current: string, index: number, input: string): { value: string; focus: number } {
  const digits = input.replace(/\D/g, "");
  const cur = current.padEnd(CODE_LEN, " ").split("");
  if (!digits) return { value: current, focus: index };
  let i = Math.max(0, Math.min(CODE_LEN - 1, index));
  for (const d of digits) {
    if (i >= CODE_LEN) break;
    cur[i++] = d;
  }
  const value = cur.join("").replace(/\s+$/, "");
  return { value, focus: Math.min(i, CODE_LEN - 1) };
}

/** Borrar en la casilla i: si tiene dígito lo borra; si no, borra el anterior y retrocede. */
export function deleteDigit(current: string, index: number): { value: string; focus: number } {
  const cur = current.padEnd(CODE_LEN, " ").split("");
  let i = index;
  if (cur[i] === " " && i > 0) i--;
  cur[i] = " ";
  return { value: cur.join("").replace(/\s+$/, ""), focus: i };
}
