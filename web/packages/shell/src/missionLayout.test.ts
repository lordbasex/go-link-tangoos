import { describe, expect, it } from "vitest";
import { missionLayout } from "./missionLayout";

const area = { x: 0, y: 28, w: 1440, h: 800 };
const overlaps = (a: { x: number; y: number; w: number; h: number }, b: typeof a) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;

describe("Mission Control: distribución", () => {
  it("sin ventanas no hay nada", () => {
    expect(missionLayout([], area)).toEqual([]);
  });

  it("una ventana chica no se agranda", () => {
    const [p] = missionLayout([{ id: "a", x: 100, y: 100, w: 600, h: 400 }], area);
    expect(p.scale).toBe(1);
    expect(p.to.w).toBe(600);
  });

  it("muchas ventanas: todas dentro del área, sin superponerse y con su proporción", () => {
    const wins = Array.from({ length: 7 }, (_, i) => ({ id: `w${i}`, x: 40 * i, y: 60 + 20 * i, w: 900 + 40 * i, h: 600 }));
    const placed = missionLayout(wins, area);
    expect(placed).toHaveLength(7);
    for (const p of placed) {
      expect(p.to.x).toBeGreaterThanOrEqual(area.x);
      expect(p.to.y).toBeGreaterThanOrEqual(area.y);
      expect(p.to.x + p.to.w).toBeLessThanOrEqual(area.x + area.w);
      expect(p.to.y + p.to.h).toBeLessThanOrEqual(area.y + area.h);
      const w = wins.find((x) => x.id === p.id)!;
      expect(Math.abs(p.to.w / p.to.h - w.w / w.h)).toBeLessThan(0.02);
    }
    for (let i = 0; i < placed.length; i++) for (let j = i + 1; j < placed.length; j++) expect(overlaps(placed[i].to, placed[j].to)).toBe(false);
  });

  it("conserva el orden de lectura (arriba→abajo, izquierda→derecha)", () => {
    const placed = missionLayout(
      [
        { id: "abajo", x: 0, y: 500, w: 500, h: 300 },
        { id: "arriba-der", x: 800, y: 40, w: 500, h: 300 },
        { id: "arriba-izq", x: 0, y: 40, w: 500, h: 300 },
      ],
      area,
    );
    expect(placed.map((p) => p.id)).toEqual(["arriba-izq", "arriba-der", "abajo"]);
  });
});
