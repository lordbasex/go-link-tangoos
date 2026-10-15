// Wallpapers propios, generados: el Río de la Plata y el skyline porteño con el Obelisco.
// Se rasterizan una vez como data-URL SVG (el navegador los cachea), no se repintan al mover ventanas.

export type WallpaperId = "atardecer" | "noche" | "amanecer" | "dinamico";

interface Palette {
  sky: [string, number][];
  sun: { x: number; y: number; r: number; color: string; glow: string } | null;
  city: string;
  cityFar: string;
  water: [string, string];
  shimmer: string;
  lights: number; // densidad de ventanas encendidas
  stars: boolean;
}

const palettes: Record<Exclude<WallpaperId, "dinamico">, Palette> = {
  atardecer: {
    sky: [["#1b1748", 0], ["#3b2470", 0.28], ["#a33a7a", 0.52], ["#f0705a", 0.68], ["#ffb36b", 0.78]],
    sun: { x: 1720, y: 1010, r: 92, color: "#ffd59a", glow: "#ff8a5c" },
    city: "#1a1233",
    cityFar: "#4a2a62",
    water: ["#3a2560", "#120d26"],
    shimmer: "#ffb784",
    lights: 0.12,
    stars: false,
  },
  noche: {
    sky: [["#05060f", 0], ["#0b1030", 0.45], ["#1c1d4f", 0.7], ["#3a2b66", 0.79]],
    sun: { x: 560, y: 260, r: 34, color: "#eef0ff", glow: "#7c86ff" },
    city: "#07070f",
    cityFar: "#161634",
    water: ["#141533", "#03040a"],
    shimmer: "#ffd38a",
    lights: 0.5,
    stars: true,
  },
  amanecer: {
    sky: [["#9fc2ef", 0], ["#c8d6f2", 0.35], ["#f2d3dc", 0.58], ["#ffd9b8", 0.74], ["#ffe9cf", 0.79]],
    sun: { x: 780, y: 1040, r: 80, color: "#fff4df", glow: "#ffc59a" },
    city: "#55607e",
    cityFar: "#93a0c0",
    water: ["#a8b8d8", "#5e6f95"],
    shimmer: "#fff1dc",
    lights: 0,
    stars: false,
  },
};

const W = 2560;
const H = 1600;
const HORIZON = 1080;

function rng(seed: number) {
  return () => {
    seed = (seed * 1664525 + 1013904223) >>> 0;
    return seed / 4294967296;
  };
}

function skyline(r: () => number, base: number, minH: number, maxH: number, gap: number) {
  const parts: string[] = [];
  const windows: [number, number][] = [];
  let x = -20;
  while (x < W + 20) {
    const w = 26 + r() * 70;
    const h = minH + Math.pow(r(), 1.8) * (maxH - minH);
    const top = base - h;
    if (r() < 0.18) {
      // edificio con remate escalonado
      parts.push(`M${x},${base}V${top + 18}H${x + w * 0.2}V${top}H${x + w * 0.8}V${top + 18}H${x + w}V${base}Z`);
    } else if (r() < 0.12) {
      // antena
      parts.push(`M${x},${base}V${top}H${x + w}V${base}Z M${x + w / 2 - 1.5},${top}V${top - 40}h3V${top}Z`);
    } else {
      parts.push(`M${x},${base}V${top}H${x + w}V${base}Z`);
    }
    for (let wy = top + 10; wy < base - 8; wy += 13) for (let wx = x + 6; wx < x + w - 6; wx += 10) windows.push([wx, wy]);
    x += w + r() * gap;
  }
  return { path: parts.join(""), windows };
}

/** El Obelisco: 67,5 m de fuste con punta piramidal. */
function obelisco(cx: number, base: number) {
  const h = 330;
  const bw = 34;
  const tw = 22;
  return `M${cx - bw / 2},${base}L${cx - tw / 2},${base - h}L${cx},${base - h - 34}L${cx + tw / 2},${base - h}L${cx + bw / 2},${base}Z`;
}

export function wallpaperSVG(id: Exclude<WallpaperId, "dinamico">): string {
  const p = palettes[id];
  const r = rng(1906); // semilla fija: el skyline es siempre el mismo
  const far = skyline(r, HORIZON, 30, 120, 6);
  const near = skyline(r, HORIZON, 50, 230, 14);
  const stops = p.sky.map(([c, o]) => `<stop offset="${o}" stop-color="${c}"/>`).join("");
  const lights = p.lights
    ? near.windows
        .filter(() => r() < p.lights)
        .map(([x, y]) => `<rect x="${x.toFixed(0)}" y="${y.toFixed(0)}" width="4" height="5" fill="${p.shimmer}" opacity="${(0.45 + r() * 0.5).toFixed(2)}"/>`)
        .join("")
    : "";
  const stars = p.stars
    ? Array.from({ length: 220 }, () => {
        const y = r() * HORIZON * 0.75;
        return `<circle cx="${(r() * W).toFixed(0)}" cy="${y.toFixed(0)}" r="${(0.6 + r() * 1.4).toFixed(1)}" fill="#fff" opacity="${(0.25 + r() * 0.7 * (1 - y / HORIZON)).toFixed(2)}"/>`;
      }).join("")
    : "";
  const sun = p.sun
    ? `<circle cx="${p.sun.x}" cy="${p.sun.y}" r="${p.sun.r * 7}" fill="url(#glow)"/><circle cx="${p.sun.x}" cy="${p.sun.y}" r="${p.sun.r}" fill="${p.sun.color}"/>`
    : "";
  // reflejo: líneas horizontales cortas bajo el sol
  const shimmer = p.sun
    ? Array.from({ length: 46 }, (_, i) => {
        const y = HORIZON + 14 + i * i * 0.22 + i * 3;
        const w = (p.sun!.r * 2.4 + i * 9) * (0.4 + r() * 0.8);
        const x = p.sun!.x - w / 2 + (r() - 0.5) * (30 + i * 6);
        return `<rect x="${x.toFixed(0)}" y="${y.toFixed(0)}" width="${w.toFixed(0)}" height="${(2 + i * 0.12).toFixed(1)}" rx="1.5" fill="${p.shimmer}" opacity="${(0.55 - i * 0.011).toFixed(2)}"/>`;
      }).join("")
    : "";
  const cityLightsReflection = p.lights
    ? Array.from({ length: 90 }, () => {
        const x = r() * W;
        const y = HORIZON + 10 + r() * 140;
        return `<rect x="${x.toFixed(0)}" y="${y.toFixed(0)}" width="${(6 + r() * 30).toFixed(0)}" height="2" fill="${p.shimmer}" opacity="${(0.08 + r() * 0.25).toFixed(2)}"/>`;
      }).join("")
    : "";
  const obeliscoX = 980;

  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid slice">
<defs>
<linearGradient id="sky" x1="0" y1="0" x2="0" y2="${HORIZON}" gradientUnits="userSpaceOnUse">${stops}</linearGradient>
<linearGradient id="water" x1="0" y1="${HORIZON}" x2="0" y2="${H}" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="${p.water[0]}"/><stop offset="1" stop-color="${p.water[1]}"/></linearGradient>
<radialGradient id="glow"><stop offset="0" stop-color="${p.sun?.glow ?? "#000"}" stop-opacity="0.55"/><stop offset="1" stop-color="${p.sun?.glow ?? "#000"}" stop-opacity="0"/></radialGradient>
<linearGradient id="haze" x1="0" y1="${HORIZON - 260}" x2="0" y2="${HORIZON}" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="${p.sky[p.sky.length - 1][0]}" stop-opacity="0"/><stop offset="1" stop-color="${p.sky[p.sky.length - 1][0]}" stop-opacity="0.55"/></linearGradient>
</defs>
<rect width="${W}" height="${HORIZON}" fill="url(#sky)"/>
${stars}${sun}
<path d="${far.path}" fill="${p.cityFar}" opacity="0.9"/>
<rect y="${HORIZON - 260}" width="${W}" height="260" fill="url(#haze)"/>
<path d="${near.path}${obelisco(obeliscoX, HORIZON)}" fill="${p.city}"/>
${lights}
<rect y="${HORIZON}" width="${W}" height="${H - HORIZON}" fill="url(#water)"/>
${shimmer}${cityLightsReflection}
</svg>`;
}

const cache = new Map<string, string>();
export function wallpaperURL(id: WallpaperId, now = new Date()): string {
  const resolved = id === "dinamico" ? (now.getHours() < 7 || now.getHours() >= 20 ? "noche" : now.getHours() < 11 ? "amanecer" : "atardecer") : id;
  let url = cache.get(resolved);
  if (!url) {
    url = `url("data:image/svg+xml;charset=utf-8,${encodeURIComponent(wallpaperSVG(resolved))}")`;
    cache.set(resolved, url);
  }
  return url;
}

export const wallpapers: { id: WallpaperId; name: string; dark: boolean }[] = [
  { id: "atardecer", name: "Atardecer en el Río", dark: true },
  { id: "noche", name: "Noche porteña", dark: true },
  { id: "amanecer", name: "Amanecer", dark: false },
  { id: "dinamico", name: "Dinámico (según la hora)", dark: true },
];
