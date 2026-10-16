// Instalador de TangoOS: interfaz sin dependencias (corre en el kiosco del live y desde otra PC).
// Habla con internal/installer: /api/state, /api/unlock, /api/disks, /api/install, /api/reboot.
"use strict";

const T = {
  es: {
    title: "Instalar TangoOS", back: "Atrás", continue: "Continuar", install: "Borrar e instalar",
    reboot: "Reiniciar", retry: "Volver a intentar", unlock: "Desbloquear",
    remoteTitle: "Desde otra PC", remoteCode: "código",
    steps: ["Introducción", "Disco", "Red y nombre", "Resumen", "Instalación", "Listo"],
    introTitle: "Te damos la bienvenida a TangoOS",
    introLead: "Vas a instalar TangoOS en un disco de este equipo. Tarda un par de minutos y no hace falta internet. Los discos de datos no se tocan: los configurás después desde Almacenamiento.",
    language: "Idioma", keyboard: "Teclado",
    keyboardHelp: "La distribución del teclado de este equipo.",
    timezone: "Zona horaria", tzSearch: "Buscar una ciudad o región",
    timezoneHelp: "La hora del NAS para las instantáneas programadas, los registros y las verificaciones.",
    tzNone: "No hay zonas con ese nombre.",
    diskTitle: "Elegí el disco de sistema",
    diskLead: "TangoOS se instala en este disco y lo borra por completo. Para el sistema alcanza con uno chico (16 GB o más): los discos grandes conviene dejarlos para tus datos.",
    noDisks: "No encontré ningún disco donde instalar. Conectá uno y probá de nuevo.",
    reload: "Buscar de nuevo", noSerial: "sin número de serie",
    netTitle: "Red y nombre del equipo",
    netLead: "Así vas a encontrar tu NAS en la red. Podés cambiarlo después desde Ajustes.",
    hostname: "Nombre del equipo", hostnameHelp: "Letras, números y guiones. Vas a entrar por https://NOMBRE.local",
    network: "Conexión", dhcp: "Automática (DHCP)", static: "IP fija",
    address: "Dirección IP y máscara", gateway: "Puerta de enlace", dns: "DNS",
    dnsHelp: "Si lo dejás vacío se usa la puerta de enlace.",
    sumTitle: "Revisá antes de instalar", sumLead: "Esto es lo que se va a hacer.",
    sumDisk: "Disco de sistema", sumName: "Nombre", sumNet: "Red", sumKbd: "Teclado", sumTz: "Zona horaria",
    warn: "Se borra todo lo que hay en {disk}. Esto no se puede deshacer.",
    confirm: "Entiendo que se borra el disco {disk}",
    instTitle: "Instalando TangoOS", instLead: "No apagues el equipo. Podés seguir el progreso también desde otra PC.",
    doneTitle: "TangoOS está instalado",
    doneLead: "Sacá el medio de instalación (USB o CD) y reiniciá. Cuando arranque, abrí esta dirección desde cualquier navegador de tu red para crear tu cuenta de administrador:",
    doneOr: "o con la IP que muestra la pantalla del equipo.",
    rebooting: "Reiniciando… ya podés cerrar esta ventana.",
    failTitle: "La instalación no terminó",
    failLead: "El disco no quedó listo para arrancar. Revisá el detalle y volvé a intentar.",
    lockTitle: "Ingresá el código",
    lockLead: "Por seguridad, para instalar desde otra PC hace falta el código de 6 dígitos que se ve en la pantalla del equipo.",
    errHost: "El nombre admite letras, números y guiones (hasta 63).",
    errIP: "Escribí la IP con su máscara, por ejemplo 192.168.1.50/24.",
    errGw: "Falta la puerta de enlace.",
    errNet: "No me pude comunicar con el instalador.",
  },
  en: {
    title: "Install TangoOS", back: "Back", continue: "Continue", install: "Erase and install",
    reboot: "Restart", retry: "Try again", unlock: "Unlock",
    remoteTitle: "From another computer", remoteCode: "code",
    steps: ["Introduction", "Disk", "Network and name", "Summary", "Installation", "Done"],
    introTitle: "Welcome to TangoOS",
    introLead: "You are about to install TangoOS on a disk in this computer. It takes a couple of minutes and needs no internet. Data disks are left alone: you set them up later in Storage.",
    language: "Language", keyboard: "Keyboard",
    keyboardHelp: "The keyboard layout of this computer.",
    timezone: "Time zone", tzSearch: "Search for a city or region",
    timezoneHelp: "The NAS clock for scheduled snapshots, logs and checks.",
    tzNone: "No time zone matches that name.",
    diskTitle: "Choose the system disk",
    diskLead: "TangoOS is installed on this disk and erases it completely. A small one (16 GB or more) is enough for the system: keep the large disks for your data.",
    noDisks: "No disk to install on was found. Connect one and try again.",
    reload: "Search again", noSerial: "no serial number",
    netTitle: "Network and computer name",
    netLead: "This is how you will find your NAS on the network. You can change it later in Settings.",
    hostname: "Computer name", hostnameHelp: "Letters, numbers and hyphens. You will open https://NAME.local",
    network: "Connection", dhcp: "Automatic (DHCP)", static: "Fixed IP",
    address: "IP address and mask", gateway: "Gateway", dns: "DNS",
    dnsHelp: "Leave it empty to use the gateway.",
    sumTitle: "Check before installing", sumLead: "This is what will happen.",
    sumDisk: "System disk", sumName: "Name", sumNet: "Network", sumKbd: "Keyboard", sumTz: "Time zone",
    warn: "Everything on {disk} is erased. This cannot be undone.",
    confirm: "I understand the disk {disk} will be erased",
    instTitle: "Installing TangoOS", instLead: "Do not turn the computer off. You can follow the progress from another computer too.",
    doneTitle: "TangoOS is installed",
    doneLead: "Remove the installation media (USB or CD) and restart. When it is up, open this address from any browser on your network to create your administrator account:",
    doneOr: "or the IP address shown on the computer's screen.",
    rebooting: "Restarting… you can close this window now.",
    failTitle: "The installation did not finish",
    failLead: "The disk is not ready to start. Check the details and try again.",
    lockTitle: "Enter the code",
    lockLead: "For security, installing from another computer needs the 6-digit code shown on the computer's screen.",
    errHost: "The name takes letters, numbers and hyphens (up to 63).",
    errIP: "Write the IP with its mask, for example 192.168.1.50/24.",
    errGw: "The gateway is missing.",
    errNet: "Could not reach the installer.",
  },
  pt: {
    title: "Instalar o TangoOS", back: "Voltar", continue: "Continuar", install: "Apagar e instalar",
    reboot: "Reiniciar", retry: "Tentar de novo", unlock: "Desbloquear",
    remoteTitle: "De outro computador", remoteCode: "código",
    steps: ["Introdução", "Disco", "Rede e nome", "Resumo", "Instalação", "Pronto"],
    introTitle: "Boas-vindas ao TangoOS",
    introLead: "Você vai instalar o TangoOS em um disco deste computador. Leva alguns minutos e não precisa de internet. Os discos de dados não são tocados: você os configura depois em Armazenamento.",
    language: "Idioma", keyboard: "Teclado",
    keyboardHelp: "O layout do teclado deste computador.",
    timezone: "Fuso horário", tzSearch: "Buscar uma cidade ou região",
    timezoneHelp: "O relógio do NAS para os snapshots programados, os registros e as verificações.",
    tzNone: "Nenhum fuso horário com esse nome.",
    diskTitle: "Escolha o disco do sistema",
    diskLead: "O TangoOS é instalado neste disco e o apaga por completo. Para o sistema basta um pequeno (16 GB ou mais): é melhor deixar os discos grandes para os seus dados.",
    noDisks: "Não encontrei nenhum disco para instalar. Conecte um e tente de novo.",
    reload: "Procurar de novo", noSerial: "sem número de série",
    netTitle: "Rede e nome do computador",
    netLead: "É assim que você vai encontrar o seu NAS na rede. Dá para mudar depois em Ajustes.",
    hostname: "Nome do computador", hostnameHelp: "Letras, números e hífens. Você vai entrar por https://NOME.local",
    network: "Conexão", dhcp: "Automática (DHCP)", static: "IP fixo",
    address: "Endereço IP e máscara", gateway: "Gateway", dns: "DNS",
    dnsHelp: "Se ficar vazio, usa o gateway.",
    sumTitle: "Confira antes de instalar", sumLead: "Isto é o que vai ser feito.",
    sumDisk: "Disco do sistema", sumName: "Nome", sumNet: "Rede", sumKbd: "Teclado", sumTz: "Fuso horário",
    warn: "Tudo o que existe em {disk} vai ser apagado. Isso não pode ser desfeito.",
    confirm: "Entendo que o disco {disk} vai ser apagado",
    instTitle: "Instalando o TangoOS", instLead: "Não desligue o computador. Você também pode acompanhar o progresso de outro computador.",
    doneTitle: "O TangoOS está instalado",
    doneLead: "Retire a mídia de instalação (USB ou CD) e reinicie. Quando ligar, abra este endereço em qualquer navegador da sua rede para criar a sua conta de administrador:",
    doneOr: "ou pelo IP que aparece na tela do computador.",
    rebooting: "Reiniciando… você já pode fechar esta janela.",
    failTitle: "A instalação não terminou",
    failLead: "O disco não ficou pronto para iniciar. Confira os detalhes e tente de novo.",
    lockTitle: "Digite o código",
    lockLead: "Por segurança, para instalar de outro computador é preciso o código de 6 dígitos que aparece na tela do computador.",
    errHost: "O nome aceita letras, números e hífens (até 63).",
    errIP: "Escreva o IP com a máscara, por exemplo 192.168.1.50/24.",
    errGw: "Falta o gateway.",
    errNet: "Não consegui me comunicar com o instalador.",
  },
};

// Mensajes que llegan del instalador (motor y servidor) en español, por prefijo.
const SRV = {
  en: [
    ["Preparando…", "Preparing…"], ["Disco de sistema: ", "System disk: "], ["Particiones creadas", "Partitions created"],
    ["Copiando el sistema…", "Copying the system…"], ["Sistema copiado", "System copied"], ["Preparando el arranque", "Preparing to boot"],
    ["Instalando el cargador de arranque", "Installing the boot loader"], ["TangoOS instalado en ", "TangoOS installed in "],
    ["elegí uno de los discos de la lista", "choose one of the disks in the list"], ["confirmá que se borra el disco", "confirm that the disk is erased"],
    ["el nombre del equipo admite letras, números y guiones (hasta 63)", "the computer name takes letters, numbers and hyphens (up to 63)"],
    ["distribución de teclado desconocida", "unknown keyboard layout"], ["zona horaria desconocida", "unknown time zone"],
    ["la dirección IP va con su máscara, por ejemplo 192.168.1.50/24", "write the IP address with its mask, for example 192.168.1.50/24"],
    ["la puerta de enlace tiene que estar en la misma red que la IP", "the gateway has to be on the same network as the IP"],
    ["el DNS tiene que ser una dirección IPv4", "the DNS has to be an IPv4 address"], ["modo de red desconocido", "unknown network mode"],
    ["el código no coincide", "the code does not match"], ["demasiados intentos: esperá ", "too many attempts: wait "], ["pedido inválido", "invalid request"],
    ["esperá a que termine la instalación", "wait for the installation to finish"], ["ingresá el código que se ve en la pantalla del equipo", "enter the code shown on the computer's screen"],
    ["no pude listar los discos", "could not list the disks"],
  ],
  pt: [
    ["Preparando…", "Preparando…"], ["Disco de sistema: ", "Disco do sistema: "], ["Particiones creadas", "Partições criadas"],
    ["Copiando el sistema…", "Copiando o sistema…"], ["Sistema copiado", "Sistema copiado"], ["Preparando el arranque", "Preparando a inicialização"],
    ["Instalando el cargador de arranque", "Instalando o carregador de inicialização"], ["TangoOS instalado en ", "TangoOS instalado em "],
    ["elegí uno de los discos de la lista", "escolha um dos discos da lista"], ["confirmá que se borra el disco", "confirme que o disco vai ser apagado"],
    ["el nombre del equipo admite letras, números y guiones (hasta 63)", "o nome do computador aceita letras, números e hífens (até 63)"],
    ["distribución de teclado desconocida", "layout de teclado desconhecido"], ["zona horaria desconocida", "fuso horário desconhecido"],
    ["la dirección IP va con su máscara, por ejemplo 192.168.1.50/24", "escreva o IP com a máscara, por exemplo 192.168.1.50/24"],
    ["la puerta de enlace tiene que estar en la misma red que la IP", "o gateway tem que estar na mesma rede que o IP"],
    ["el DNS tiene que ser una dirección IPv4", "o DNS tem que ser um endereço IPv4"], ["modo de red desconocido", "modo de rede desconhecido"],
    ["el código no coincide", "o código não confere"], ["demasiados intentos: esperá ", "tentativas demais: espere "], ["pedido inválido", "pedido inválido"],
    ["esperá a que termine la instalación", "espere a instalação terminar"], ["ingresá el código que se ve en la pantalla del equipo", "digite o código que aparece na tela do computador"],
    ["no pude listar los discos", "não consegui listar os discos"],
  ],
};
const srv = (msg) => {
  if (!msg || S.lang === "es") return msg || "";
  for (const [es, out] of SRV[S.lang] || []) if (msg.startsWith(es)) return out + msg.slice(es.length);
  return msg;
};

// Zona horaria por defecto: la del navegador (desde otra PC) o la del idioma (kiosco: suele decir UTC).
const TZ_BY_LANG = { es: "America/Argentina/Buenos_Aires", pt: "America/Sao_Paulo", en: "UTC" };
const KBD_BY_LANG = { es: "latam", pt: "br", en: "us" };
const browserTZ = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone || ""; } catch { return ""; } })();
const tzLabel = (z) => z.replace(/_/g, " ").split("/").join(" / ");
const KBD_NAMES = { latam: "Español (Latinoamérica)", es: "Español (España)", us: "English (US)", br: "Português (Brasil)", pt: "Português (Portugal)", gb: "English (UK)", fr: "Français", de: "Deutsch", it: "Italiano" };

const navLang = (navigator.language || "es").slice(0, 2);
const S = {
  lang: T[navLang] ? navLang : "es",
  step: 0, state: null, disks: null, keyboards: Object.keys(KBD_NAMES),
  disk: "", keyboard: "latam", hostname: "tango", timezone: "", timezones: null, tzTouched: false, tzQuery: "",
  net: { mode: "dhcp", address: "", gateway: "", dns: "" },
  confirmed: false, error: "", polling: false, rebooting: false,
};
const t = (k, vars) => {
  let s = T[S.lang][k] ?? T.es[k] ?? k;
  for (const [a, b] of Object.entries(vars || {})) s = s.replace("{" + a + "}", b);
  return s;
};
const $ = (id) => document.getElementById(id);
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of kids.flat()) if (c != null && c !== false) el.append(c.nodeType ? c : String(c));
  return el;
}
const svg = (markup) => { const d = document.createElement("span"); d.innerHTML = markup; return d.firstElementChild; };
const DISK_ICON = '<svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="M3 14h18"/><circle cx="17" cy="17" r="1" fill="currentColor"/></svg>';
const CHECK = '<svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.4" aria-hidden="true"><path d="m5 12.5 4.5 4.5L19 7.5"/></svg>';
const CROSS = '<svg width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.4" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18"/></svg>';

function fmtBytes(n) {
  const u = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  while (n >= 1000 && i < u.length - 1) { n /= 1000; i++; }
  return (n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1)).toString().replace(".", S.lang === "en" ? "." : ",") + " " + u[i];
}
const diskObj = () => (S.disks || []).find((d) => d.path === S.disk);
const diskLabel = (d) => d ? `${d.model || d.path} · ${fmtBytes(d.bytes)}` : S.disk;

async function api(path, body) {
  const opt = body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json", "X-Tango-Installer": "1" }, body: JSON.stringify(body) };
  const r = await fetch(path, opt);
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw Object.assign(new Error(j.message ? srv(j.message) : t("errNet")), { code: j.code });
  return j;
}

// ---- Validación en el navegador (el servidor valida igual) ----
const HOST_RE = /^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/;
const CIDR_RE = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/;
function validNet() {
  if (!HOST_RE.test(S.hostname)) return t("errHost");
  if (S.net.mode === "static") {
    const m = CIDR_RE.exec(S.net.address.trim());
    if (!m || m.slice(1, 5).some((x) => +x > 255) || +m[5] < 8 || +m[5] > 30) return t("errIP");
    if (!S.net.gateway.trim()) return t("errGw");
  }
  return "";
}

// ---- Pasos ----
// Sin <select>: en el kiosco (cog) la lista nativa es una superficie aparte que se queda con la entrada.
function renderIntro() {
  const lang = (code, name) => h("button", { type: "button", "aria-pressed": String(S.lang === code), "data-lang": code,
    onclick: () => { S.lang = code; if (!S.kbdTouched) S.keyboard = KBD_BY_LANG[code]; if (!S.tzTouched) S.timezone = defaultTZ(); render(); } }, name);
  const kbd = (k) => h("button", { type: "button", class: "opt", role: "radio", "aria-checked": String(S.keyboard === k), "data-kbd": k,
    onclick: () => { S.keyboard = k; S.kbdTouched = true; render(); } }, KBD_NAMES[k] || k);
  return [
    h("h2", {}, t("introTitle")),
    h("p", { class: "lead" }, t("introLead")),
    h("div", { class: "field" }, h("span", {}, t("language")), h("div", { class: "seg", role: "group" }, lang("es", "Español"), lang("en", "English"), lang("pt", "Português"))),
    h("div", { class: "field wide" }, h("span", { id: "kbd-label" }, t("keyboard")),
      h("div", { class: "opts", role: "radiogroup", "aria-labelledby": "kbd-label" }, S.keyboards.map(kbd)),
      h("small", {}, t("keyboardHelp"))),
    renderTZ(),
  ];
}

function defaultTZ() {
  const list = S.timezones || [];
  if (browserTZ && browserTZ !== "UTC" && !browserTZ.startsWith("Etc/") && list.includes(browserTZ)) return browserTZ;
  return TZ_BY_LANG[S.lang] || "UTC";
}

// Zona horaria: buscador + lista corta (sin <select>, ver arriba).
function renderTZ() {
  if (S.timezones === null) { loadTimezones(); return h("div", { class: "field wide" }, h("span", {}, t("timezone"))); }
  const q = S.tzQuery.trim().toLowerCase().replace(/\s+/g, " ");
  const match = (z) => !q || tzLabel(z).toLowerCase().includes(q) || z.toLowerCase().includes(q.replace(/ /g, "_"));
  let shown = S.timezones.filter(match).slice(0, 8);
  if (S.timezone && !shown.includes(S.timezone) && !q) shown = [S.timezone, ...shown.slice(0, 7)];
  const opt = (z) => h("button", { type: "button", class: "opt", role: "radio", "aria-checked": String(S.timezone === z), "data-tz": z,
    onclick: () => { S.timezone = z; S.tzTouched = true; render(); } }, tzLabel(z));
  const input = h("input", { type: "text", id: "tz-search", value: S.tzQuery, placeholder: t("tzSearch"), autocomplete: "off", spellcheck: "false", "aria-label": t("tzSearch"),
    oninput: (e) => { S.tzQuery = e.target.value; const pos = e.target.selectionStart; render(); const el = $("tz-search"); if (el) { el.focus(); el.setSelectionRange(pos, pos); } } });
  return h("div", { class: "field wide" }, h("span", { id: "tz-label" }, t("timezone") + (S.timezone ? ": " + tzLabel(S.timezone) : "")), input,
    h("div", { class: "opts", role: "radiogroup", "aria-labelledby": "tz-label" }, shown.length ? shown.map(opt) : h("small", {}, t("tzNone"))),
    h("small", {}, t("timezoneHelp")));
}

async function loadTimezones() {
  try {
    const j = await api("/api/disks");
    S.timezones = j.timezones || ["UTC"];
    if (j.keyboards) S.keyboards = j.keyboards;
  } catch { S.timezones = ["UTC"]; }
  if (!S.timezone) S.timezone = defaultTZ();
  render();
}

function renderDisk() {
  const out = [h("h2", {}, t("diskTitle")), h("p", { class: "lead" }, t("diskLead"))];
  if (S.disks === null) { loadDisks(); return out; }
  if (!S.disks.length) {
    out.push(h("div", { class: "warn" }, t("noDisks")), h("button", { class: "btn", type: "button", onclick: () => { S.disks = null; render(); } }, t("reload")));
    return out;
  }
  out.push(h("div", { class: "disks", role: "group" }, S.disks.map((d) =>
    h("button", { type: "button", class: "disk", "aria-pressed": String(S.disk === d.path), onclick: () => { S.disk = d.path; S.confirmed = false; render(); } },
      svg(DISK_ICON),
      h("div", {}, h("b", {}, d.model || d.path), h("span", {}, `${d.path} · ${d.serial || t("noSerial")}`)),
      h("span", { class: "size" }, fmtBytes(d.bytes))))));
  return out;
}

async function loadDisks() {
  try {
    const j = await api("/api/disks");
    S.disks = j.disks || [];
    if (j.keyboards) S.keyboards = j.keyboards;
    if (S.disks.length === 1) S.disk = S.disks[0].path;
  } catch (e) { S.disks = []; S.error = e.message; }
  render();
}

function renderNet() {
  const set = (k) => (e) => { S.net[k] = e.target.value; S.error = ""; syncNext(); };
  const mode = (m) => h("button", { type: "button", "aria-pressed": String(S.net.mode === m), onclick: () => { S.net.mode = m; render(); } }, t(m));
  const out = [
    h("h2", {}, t("netTitle")), h("p", { class: "lead" }, t("netLead")),
    h("label", { class: "field" }, h("span", {}, t("hostname")),
      h("input", { type: "text", id: "hostname", value: S.hostname, maxlength: "63", autocomplete: "off", spellcheck: "false", oninput: (e) => { S.hostname = e.target.value.trim(); S.error = ""; syncNext(); } }),
      h("small", {}, t("hostnameHelp").replace("NOMBRE", S.hostname || "tango").replace("NAME", S.hostname || "tango").replace("NOME", S.hostname || "tango"))),
    h("div", { class: "field" }, h("span", {}, t("network")), h("div", { class: "seg" }, mode("dhcp"), mode("static"))),
  ];
  if (S.net.mode === "static") {
    out.push(
      h("label", { class: "field" }, h("span", {}, t("address")), h("input", { type: "text", id: "address", value: S.net.address, placeholder: "192.168.1.50/24", inputmode: "decimal", oninput: set("address") })),
      h("div", { class: "row" },
        h("label", { class: "field" }, h("span", {}, t("gateway")), h("input", { type: "text", id: "gateway", value: S.net.gateway, placeholder: "192.168.1.1", inputmode: "decimal", oninput: set("gateway") })),
        h("label", { class: "field" }, h("span", {}, t("dns")), h("input", { type: "text", id: "dns", value: S.net.dns, placeholder: "1.1.1.1", inputmode: "decimal", oninput: set("dns") }), h("small", {}, t("dnsHelp")))));
  }
  out.push(h("p", { class: "error", role: "alert" }, S.error));
  return out;
}

function renderSummary() {
  const d = diskObj();
  const net = S.net.mode === "dhcp" ? t("dhcp") : `${S.net.address} · ${S.net.gateway}`;
  return [
    h("h2", {}, t("sumTitle")), h("p", { class: "lead" }, t("sumLead")),
    h("dl", { class: "summary" },
      h("dt", {}, t("sumDisk")), h("dd", {}, `${diskLabel(d)} (${S.disk})`),
      h("dt", {}, t("sumName")), h("dd", {}, `${S.hostname}.local`),
      h("dt", {}, t("sumNet")), h("dd", {}, net),
      h("dt", {}, t("sumKbd")), h("dd", {}, KBD_NAMES[S.keyboard] || S.keyboard),
      h("dt", {}, t("sumTz")), h("dd", {}, tzLabel(S.timezone || "UTC"))),
    h("div", { class: "warn", role: "note" }, t("warn", { disk: diskLabel(d) })),
    h("label", { class: "check" }, h("input", { type: "checkbox", id: "confirm", checked: S.confirmed, onchange: (e) => { S.confirmed = e.target.checked; syncNext(); } }), t("confirm", { disk: S.disk })),
    h("p", { class: "error", role: "alert" }, S.error),
  ];
}

function renderInstall() {
  const st = S.state || {};
  return [
    h("h2", {}, t("instTitle")), h("p", { class: "lead" }, t("instLead")),
    h("div", { class: "progress", role: "progressbar", "aria-valuemin": "0", "aria-valuemax": "100", "aria-valuenow": String(st.percent || 0) }, h("div", { id: "bar" })),
    h("div", { class: "progress-text" }, h("span", { id: "msg" }, srv(st.message)), h("span", { id: "pct" }, `${st.percent || 0} %`)),
    h("div", { class: "log", id: "log" }, (st.log || []).join("\n")),
  ];
}

function renderDone() {
  const host = (S.state && S.state.hostname) || S.hostname;
  if (S.rebooting) return [h("h2", {}, t("doneTitle")), h("p", { class: "lead" }, t("rebooting"))];
  return [
    h("div", { class: "done-mark" }, svg(CHECK)),
    h("h2", {}, t("doneTitle")),
    h("p", { class: "lead" }, t("doneLead")),
    h("p", { class: "big-url" }, `https://${host}.local`),
    h("p", { class: "lead" }, t("doneOr")),
  ];
}

function renderFailed() {
  const st = S.state || {};
  return [
    h("div", { class: "done-mark fail-mark" }, svg(CROSS)),
    h("h2", {}, t("failTitle")), h("p", { class: "lead" }, t("failLead")),
    h("p", { class: "error" }, srv(st.error)),
    h("div", { class: "log" }, (st.log || []).join("\n")),
  ];
}

function renderLock() {
  const input = h("input", { type: "text", class: "pin-input", id: "pin", inputmode: "numeric", maxlength: "6", autocomplete: "one-time-code", "aria-label": t("lockTitle"),
    oninput: (e) => { e.target.value = e.target.value.replace(/\D/g, ""); S.error = ""; syncNext(); },
    onkeydown: (e) => { if (e.key === "Enter") next(); } });
  setTimeout(() => input.focus(), 0);
  return [h("h2", {}, t("lockTitle")), h("p", { class: "lead" }, t("lockLead")), input, h("p", { class: "error", role: "alert" }, S.error)];
}

// ---- Navegación ----
const STEPS = [renderIntro, renderDisk, renderNet, renderSummary, renderInstall, renderDone];
const locked = () => S.state && !S.state.unlocked;

function render() {
  document.documentElement.lang = S.lang;
  document.title = t("title");
  document.querySelectorAll("[data-t]").forEach((el) => { el.textContent = t(el.dataset.t); });
  const st = S.state || {};
  $("steps").replaceChildren(...(locked() ? [] : t("steps").map((name, i) =>
    h("li", { class: i < S.step ? "done" : i === S.step ? "now" : "", "aria-current": i === S.step ? "step" : null }, name))));
  const pane = $("pane");
  pane.replaceChildren(...(locked() ? renderLock() : st.phase === "failed" && S.step === 4 ? renderFailed() : STEPS[S.step]()));
  if (st.pin) {
    $("remote").hidden = !(st.urls && st.urls.length);
    $("remote-url").textContent = (st.urls || []).join("  ");
    $("remote-pin").textContent = st.pin.slice(0, 3) + " " + st.pin.slice(3);
  }
  $("ver").textContent = st.version ? "TangoOS " + st.version : "";
  updateProgress();
  syncNext();
}

function syncNext() {
  const back = $("back"), nx = $("next"), st = S.state || {};
  back.hidden = locked() || S.step === 0 || S.step >= 4;
  nx.hidden = false;
  nx.className = "btn primary";
  nx.disabled = false;
  if (locked()) { nx.textContent = t("unlock"); nx.disabled = (($("pin") || {}).value || "").length !== 6; return; }
  if (S.step === 1) nx.disabled = !S.disk;
  else if (S.step === 2) nx.disabled = !S.hostname;
  else if (S.step === 3) { nx.textContent = t("install"); nx.className = "btn danger"; nx.disabled = !S.confirmed; return; }
  else if (S.step === 4) {
    if (st.phase === "failed") { nx.textContent = t("retry"); return; }
    nx.hidden = true; return;
  } else if (S.step === 5) { nx.textContent = t("reboot"); nx.hidden = S.rebooting; return; }
  nx.textContent = t("continue");
}

async function next() {
  S.error = "";
  try {
    if (locked()) {
      await api("/api/unlock", { pin: $("pin").value });
      S.state = await api("/api/state");
      return syncFromState(true);
    }
    if (S.step === 2) { const e = validNet(); if (e) { S.error = e; return render(); } }
    if (S.step === 3) {
      await api("/api/install", { disk: S.disk, confirm: S.confirmed ? S.disk : "", hostname: S.hostname, keyboard: S.keyboard, timezone: S.timezone,
        net: S.net.mode === "static" ? { mode: "static", address: S.net.address.trim(), gateway: S.net.gateway.trim(), dns: S.net.dns.trim() } : { mode: "dhcp" } });
      S.step = 4; S.state = await api("/api/state");
      poll();
      return render();
    }
    if (S.step === 4 && S.state && S.state.phase === "failed") { S.step = 3; S.confirmed = false; return render(); }
    if (S.step === 5) { await api("/api/reboot", {}); S.rebooting = true; return render(); }
    S.step = Math.min(S.step + 1, 5);
  } catch (e) { S.error = e.message; }
  render();
}

function updateProgress() {
  const st = S.state || {};
  const bar = $("bar");
  if (!bar) return;
  bar.style.width = (st.percent || 0) + "%";
  $("pct").textContent = (st.percent || 0) + " %";
  $("msg").textContent = srv(st.message);
  const log = $("log");
  const txt = (st.log || []).join("\n");
  if (log.textContent !== txt) { log.textContent = txt; log.scrollTop = log.scrollHeight; }
}

// Si se abre a mitad de camino (recarga, otra PC) se salta al paso que corresponde.
function syncFromState(full) {
  const ph = S.state && S.state.phase;
  if (ph === "installing" || ph === "failed") { S.step = 4; if (ph === "installing") poll(); }
  else if (ph === "done") S.step = 5;
  if (full) render(); else if (S.step === 4) { updateProgress(); }
}

async function poll() {
  if (S.polling) return;
  S.polling = true;
  while (S.state && S.state.phase === "installing") {
    await new Promise((r) => setTimeout(r, 1000));
    try { S.state = await api("/api/state"); } catch { continue; }
    if (S.state.phase !== "installing") { S.step = S.state.phase === "done" ? 5 : 4; render(); break; }
    updateProgress();
  }
  S.polling = false;
}

$("next").addEventListener("click", next);
// WPE WebKit en el kiosco (cog sobre cage) entrega pointerdown/up pero no genera "click": si no llega
// solo, se genera acá. En los demás navegadores el click nativo llega primero y esto no hace nada.
{
  let downEl = null, clicked = false;
  addEventListener("pointerdown", (e) => { downEl = e.target; clicked = false; }, true);
  addEventListener("click", () => { clicked = true; }, true);
  addEventListener("pointerup", (e) => {
    const el = downEl;
    downEl = null;
    if (!el || el !== e.target || e.button !== 0) return;
    setTimeout(() => {
      if (clicked) return;
      const target = el.closest("button, label, input[type=checkbox], a") || el;
      if (target.disabled) return;
      target.click();
    }, 60);
  }, true);
}
// En el kiosco cog cambia de tamaño al tomar la pantalla: repintar todo evita restos del primer cuadro.
addEventListener("resize", () => { document.body.classList.toggle("repaint"); render(); });
$("back").addEventListener("click", () => { S.error = ""; S.step = Math.max(0, S.step - 1); render(); });
document.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && e.target.tagName === "INPUT" && e.target.type === "text" && !$("next").disabled && !$("next").hidden) next();
});

(async function boot() {
  try { S.state = await api("/api/state"); } catch (e) { S.error = e.message; }
  if (S.state && S.state.local && T[S.state.lang]) S.lang = S.state.lang;
  S.keyboard = KBD_BY_LANG[S.lang] || "latam";
  // cog arranca a 1024×768 y recién después toma toda la pantalla: repintar la página entera
  // (cambia el fondo del body) borra los restos del primer cuadro.
  for (const ms of [800, 2500]) setTimeout(() => document.body.classList.toggle("repaint"), ms);
  syncFromState(false);
  render();
  // La pantalla local refresca las URL (la IP llega por DHCP después de abrir el kiosco).
  setInterval(async () => {
    if (S.polling || S.rebooting) return;
    try {
      const st = await api("/api/state");
      const changed = JSON.stringify([st.urls, st.unlocked, st.phase]) !== JSON.stringify([S.state && S.state.urls, S.state && S.state.unlocked, S.state && S.state.phase]);
      S.state = st;
      if (changed) syncFromState(true);
    } catch { /* el servidor se apaga al reiniciar */ }
  }, 4000);
})();
