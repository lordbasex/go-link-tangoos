// Distribución de Mission Control: todas las ventanas visibles en una grilla, conservando su
// proporción y su orden de lectura (arriba→abajo, izquierda→derecha). Pura: se testea sola.
import type { Rect } from "./store";

export interface Placed {
  id: string;
  /** Rect destino (en coordenadas de la pantalla). */
  to: Rect;
  /** Escala aplicada a la ventana (≤ 1). */
  scale: number;
}

export function missionLayout(wins: (Rect & { id: string })[], area: Rect, gap = 28, labelH = 26): Placed[] {
  const n = wins.length;
  if (!n) return [];
  const ordered = [...wins].sort((a, b) => (Math.abs(a.y - b.y) > 40 ? a.y - b.y : a.x - b.x));
  // Probamos cantidades de columnas y nos quedamos con la que deja las ventanas más grandes.
  let best: { cols: number; score: number } = { cols: 1, score: -1 };
  for (let cols = 1; cols <= n; cols++) {
    const rows = Math.ceil(n / cols);
    const cw = (area.w - gap * (cols + 1)) / cols;
    const ch = (area.h - gap * (rows + 1)) / rows - labelH;
    if (cw <= 0 || ch <= 0) continue;
    const score = ordered.reduce((acc, w) => acc + Math.min(1, cw / w.w, ch / w.h) ** 2 * w.w * w.h, 0);
    if (score > best.score) best = { cols, score };
  }
  const cols = best.cols;
  const rows = Math.ceil(n / cols);
  const cw = (area.w - gap * (cols + 1)) / cols;
  const ch = (area.h - gap * (rows + 1)) / rows - labelH;
  return ordered.map((w, i) => {
    const r = Math.floor(i / cols);
    const inRow = r === rows - 1 ? n - r * cols : cols; // la última fila se centra
    const c = i % cols;
    const rowOffset = ((cols - inRow) * (cw + gap)) / 2;
    const scale = Math.min(1, cw / w.w, ch / w.h);
    const tw = w.w * scale;
    const th = w.h * scale;
    const cellX = area.x + gap + c * (cw + gap) + rowOffset;
    const cellY = area.y + gap + r * (ch + labelH + gap);
    return { id: w.id, scale, to: { x: Math.round(cellX + (cw - tw) / 2), y: Math.round(cellY + (ch - th) / 2), w: Math.round(tw), h: Math.round(th) } };
  });
}
