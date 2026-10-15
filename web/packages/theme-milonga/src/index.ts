import "./tokens.css";
export * from "./wallpaper";
export * from "./icons";

export const accents = [
  { id: "tango", name: "Tango", hex: "#2F7CF6" },
  { id: "violeta", name: "Violeta", hex: "#8B5CF6" },
  { id: "rosa", name: "Rosa", hex: "#EC4899" },
  { id: "rojo", name: "Rojo", hex: "#EF4444" },
  { id: "naranja", name: "Naranja", hex: "#F97316" },
  { id: "amarillo", name: "Amarillo", hex: "#EAB308" },
  { id: "verde", name: "Verde", hex: "#22C55E" },
  { id: "grafito", name: "Grafito", hex: "#8E8E96" },
] as const;

export function applyAccent(hex: string) {
  const n = parseInt(hex.slice(1), 16);
  const rgb = `${(n >> 16) & 255} ${(n >> 8) & 255} ${n & 255}`;
  const root = document.documentElement.style;
  root.setProperty("--tg-accent", hex);
  root.setProperty("--tg-accent-rgb", rgb);
}
