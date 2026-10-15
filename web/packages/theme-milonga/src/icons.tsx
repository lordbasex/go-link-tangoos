// Íconos de app propios: superelipse (squircle), degradé, glifo y brillo superior.
// Ningún ícono de Apple ni de otros fabricantes de NAS (docs/11).
import type { ReactNode } from "react";

/** Superelipse |x|^n + |y|^n = 1 con n = 5, en un viewBox de 100. */
const SQUIRCLE = (() => {
  const n = 5;
  const pts: string[] = [];
  for (let i = 0; i < 96; i++) {
    const t = (i / 96) * Math.PI * 2;
    const c = Math.cos(t);
    const s = Math.sin(t);
    const x = 50 + 46 * Math.sign(c) * Math.pow(Math.abs(c), 2 / n);
    const y = 50 + 46 * Math.sign(s) * Math.pow(Math.abs(s), 2 / n);
    pts.push(`${x.toFixed(2)},${y.toFixed(2)}`);
  }
  return `M${pts.join("L")}Z`;
})();

let uid = 0;

export function IconBase({ from, to, children, size = 52, angle = 160 }: { from: string; to: string; children: ReactNode; size?: number; angle?: number }) {
  const id = `ic${++uid}`;
  const rad = (angle * Math.PI) / 180;
  return (
    <svg width={size} height={size} viewBox="0 0 100 100" aria-hidden="true" style={{ display: "block" }}>
      <defs>
        <linearGradient id={`${id}g`} x1={50 - Math.sin(rad) * 50} y1={50 + Math.cos(rad) * 50} x2={50 + Math.sin(rad) * 50} y2={50 - Math.cos(rad) * 50} gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor={to} />
          <stop offset="1" stopColor={from} />
        </linearGradient>
        <linearGradient id={`${id}h`} x1="0" y1="4" x2="0" y2="52" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#fff" stopOpacity="0.32" />
          <stop offset="1" stopColor="#fff" stopOpacity="0" />
        </linearGradient>
        <clipPath id={`${id}c`}>
          <path d={SQUIRCLE} />
        </clipPath>
      </defs>
      <path d={SQUIRCLE} fill={`url(#${id}g)`} />
      <g clipPath={`url(#${id}c)`}>
        {children}
        <rect x="0" y="0" width="100" height="52" fill={`url(#${id}h)`} />
      </g>
      <path d={SQUIRCLE} fill="none" stroke="#000" strokeOpacity="0.14" strokeWidth="0.8" />
    </svg>
  );
}

type P = { size?: number };

export const FilesIcon = ({ size }: P) => (
  <IconBase from="#5ab0ff" to="#1f62e0" size={size}>
    <path d="M20 34c0-3 2.4-5.4 5.4-5.4h15.2l5.6 6h28.4c3 0 5.4 2.4 5.4 5.4v29.6c0 3-2.4 5.4-5.4 5.4H25.4c-3 0-5.4-2.4-5.4-5.4Z" fill="#cfe6ff" />
    <path d="M20 44c0-3 2.4-5.4 5.4-5.4h49.2c3 0 5.4 2.4 5.4 5.4v25.6c0 3-2.4 5.4-5.4 5.4H25.4c-3 0-5.4-2.4-5.4-5.4Z" fill="#fff" />
    <path d="M37 57h26" stroke="#7fb6ff" strokeWidth="3.4" strokeLinecap="round" />
  </IconBase>
);

export const StorageIcon = ({ size }: P) => (
  <IconBase from="#5b5f6b" to="#1d1f25" size={size}>
    {[24, 43, 62].map((y, i) => (
      <g key={y}>
        <rect x="21" y={y} width="58" height="15" rx="4" fill="#e9ebf0" />
        <circle cx="70" cy={y + 7.5} r="2.6" fill={i === 2 ? "#febc2e" : "#28c840"} />
        <path d={`M28 ${y + 7.5}h22`} stroke="#9aa0ad" strokeWidth="2.4" strokeLinecap="round" />
      </g>
    ))}
  </IconBase>
);

export const SettingsIcon = ({ size }: P) => {
  const teeth = Array.from({ length: 12 }, (_, i) => {
    const a = (i / 12) * Math.PI * 2;
    return <rect key={i} x="46.5" y="17" width="7" height="12" rx="2" fill="#5d6270" transform={`rotate(${(a * 180) / Math.PI} 50 50)`} />;
  });
  return (
    <IconBase from="#d9dce3" to="#8f949f" size={size}>
      {teeth}
      <circle cx="50" cy="50" r="24" fill="#5d6270" />
      <circle cx="50" cy="50" r="17" fill="#c9ccd4" />
      <circle cx="50" cy="50" r="8" fill="#5d6270" />
    </IconBase>
  );
};

export const MonitorIcon = ({ size }: P) => (
  <IconBase from="#2b2f36" to="#0c0e11" size={size}>
    {[30, 44, 58, 72].map((y) => (
      <path key={y} d={`M18 ${y}H82`} stroke="#fff" strokeOpacity=".07" strokeWidth="1" />
    ))}
    <path d="M18 66L30 60L38 64L47 40L55 52L63 34L71 46L82 38V80H18Z" fill="#28c840" fillOpacity=".22" />
    <path d="M18 66L30 60L38 64L47 40L55 52L63 34L71 46L82 38" fill="none" stroke="#3ee07a" strokeWidth="3.4" strokeLinejoin="round" strokeLinecap="round" />
  </IconBase>
);

export const LogIcon = ({ size }: P) => (
  <IconBase from="#ffc24d" to="#f08a1c" size={size}>
    <rect x="24" y="18" width="52" height="64" rx="7" fill="#fff" />
    {[32, 43, 54, 65].map((y, i) => (
      <g key={y}>
        <circle cx="33" cy={y} r="2.8" fill={i === 1 ? "#e5484d" : "#f0a531"} />
        <path d={`M40 ${y}H${i % 2 ? 60 : 67}`} stroke="#b9bdc8" strokeWidth="3" strokeLinecap="round" />
      </g>
    ))}
  </IconBase>
);

export const TerminalIcon = ({ size }: P) => (
  <IconBase from="#3a3d45" to="#111216" size={size}>
    <rect x="16" y="22" width="68" height="56" rx="6" fill="#0b0c0f" stroke="#fff" strokeOpacity=".12" />
    <path d="M27 40l10 8-10 8" fill="none" stroke="#e8e8ea" strokeWidth="4" strokeLinecap="round" strokeLinejoin="round" />
    <path d="M42 58h16" stroke="#e8e8ea" strokeWidth="4" strokeLinecap="round" />
  </IconBase>
);

export const SnapshotsIcon = ({ size }: P) => (
  <IconBase from="#4fe0c4" to="#0e9c8f" size={size}>
    <circle cx="52" cy="52" r="24" fill="none" stroke="#fff" strokeWidth="6" />
    <path d="M52 38v15l10 6" fill="none" stroke="#fff" strokeWidth="5" strokeLinecap="round" strokeLinejoin="round" />
    <path d="M20 40l8 10 9-9" fill="none" stroke="#fff" strokeWidth="5" strokeLinecap="round" strokeLinejoin="round" />
  </IconBase>
);

export const AppsIcon = ({ size }: P) => (
  <IconBase from="#7aa8ff" to="#5a3de6" size={size}>
    {[
      [24, 24, "#fff"],
      [53, 24, "#ffd166"],
      [24, 53, "#7ef0c4"],
      [53, 53, "#ff9bc8"],
    ].map(([x, y, c]) => (
      <rect key={`${x}${y}`} x={x as number} y={y as number} width="23" height="23" rx="7" fill={c as string} fillOpacity=".95" />
    ))}
  </IconBase>
);

export const LauncherIcon = ({ size }: P) => (
  <IconBase from="#f2f3f6" to="#c3c6cf" size={size}>
    {Array.from({ length: 9 }, (_, i) => (
      <rect key={i} x={26 + (i % 3) * 17} y={26 + Math.floor(i / 3) * 17} width="12" height="12" rx="3.5" fill={["#ff5f57", "#febc2e", "#28c840", "#2f7cf6", "#8b5cf6", "#ec4899", "#22c55e", "#f97316", "#5b5f6b"][i]} />
    ))}
  </IconBase>
);

export const BackupIcon = ({ size }: P) => (
  <IconBase from="#6ee28a" to="#16a34a" size={size}>
    <path d="M30 62a14 14 0 0 1 2-27.8A19 19 0 0 1 69 38a12 12 0 0 1 1 24Z" fill="#fff" />
    <path d="M50 70V48m-9 8 9-9 9 9" fill="none" stroke="#16a34a" strokeWidth="5" strokeLinecap="round" strokeLinejoin="round" />
  </IconBase>
);

export const SecurityIcon = ({ size }: P) => (
  <IconBase from="#5b9bff" to="#2340b8" size={size}>
    <path d="M50 18l26 9v20c0 17-11 29-26 35-15-6-26-18-26-35V27Z" fill="#fff" />
    <path d="M39 50l8 8 15-16" fill="none" stroke="#2f62e0" strokeWidth="5.5" strokeLinecap="round" strokeLinejoin="round" />
  </IconBase>
);

export const DownloadsIcon = ({ size }: P) => (
  <IconBase from="#6fb8ff" to="#2a6ee8" size={size}>
    <circle cx="50" cy="50" r="27" fill="#fff" fillOpacity=".95" />
    <path d="M50 35v26m-11-11 11 11 11-11" fill="none" stroke="#2a6ee8" strokeWidth="5.5" strokeLinecap="round" strokeLinejoin="round" />
  </IconBase>
);

export const TrashIcon = ({ size, full }: P & { full?: boolean }) => (
  <IconBase from="#eef0f4" to="#b9bec8" size={size}>
    <path d="M30 32h40l-4 46H34Z" fill="#fff" fillOpacity=".7" stroke="#8c919c" strokeWidth="2" />
    {[40, 50, 60].map((x) => (
      <path key={x} d={`M${x} 38v34`} stroke="#9ca1ac" strokeWidth="2" />
    ))}
    {full && <path d="M34 34c6-8 14-4 18-10 6 6 12 2 16 10" fill="#dfe3ea" stroke="#9ca1ac" strokeWidth="1.5" />}
    <rect x="27" y="27" width="46" height="6" rx="3" fill="#a8adb7" />
  </IconBase>
);

/** Logo TangoOS: el "fueye" de un bandoneón estilizado en un nodo de datos. */
export function TangoLogo({ size = 16, color = "currentColor" }: { size?: number; color?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" style={{ display: "block" }}>
      <path d="M3 7.5 7 5v14l-4-2.5Z M8.5 4.2 12 2v20l-3.5-2.2Z M15.5 4.2 12 2v20l3.5-2.2Z M21 7.5 17 5v14l4-2.5Z" fill={color} />
    </svg>
  );
}
