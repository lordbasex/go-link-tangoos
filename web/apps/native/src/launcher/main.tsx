import "./i18n.en";
import "@fontsource-variable/inter";
import "@tango/theme-milonga";
import "@tango/ui";
import "./launcher.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { getLang } from "@tango/client";
import { wallpaperURL } from "@tango/theme-milonga";
import { Launcher } from "./Launcher";

document.documentElement.lang = getLang();
document.documentElement.dataset.theme = "dark";
document.body.style.backgroundImage = wallpaperURL("noche");
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Launcher />
  </StrictMode>,
);
