// Las fuentes las sirve el propio sitio (nunca un servicio de terceros que vea la IP de cada visita).
import "@fontsource/chakra-petch/600.css";
import "@fontsource/chakra-petch/700.css";
import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "@fontsource/jetbrains-mono/500.css";
import "./styles/tokens.css";
import "./styles/global.css";
import "./styles/landing.css";
import "./styles/webapp.css";
import "./styles/connect.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { setPortalRoot } from "./portalRoot";

const root = createRoot(document.getElementById("root")!);
setPortalRoot(root);
root.render(
  <StrictMode>
    <App />
  </StrictMode>,
);
