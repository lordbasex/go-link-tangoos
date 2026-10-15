import { IconBase } from "@tango/theme-milonga";

/** Tres nodos unidos: el ícono de la app Clúster. */
export const ClusterIcon = ({ size }: { size?: number }) => (
  <IconBase from="#9a8cff" to="#4a35d6" size={size}>
    <path d="M50 27L27 67M50 27l23 40M27 67h46" stroke="#fff" strokeOpacity=".75" strokeWidth="4.5" strokeLinecap="round" />
    <circle cx="50" cy="27" r="11" fill="#fff" />
    <circle cx="27" cy="67" r="11" fill="#c9f0ff" />
    <circle cx="73" cy="67" r="11" fill="#ffd8ef" />
  </IconBase>
);
