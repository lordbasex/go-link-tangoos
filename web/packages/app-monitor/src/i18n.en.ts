import { registerEn } from "@tango/client";

registerEn({
  // Pestañas y marco
  Resumen: "Overview",
  Memoria: "Memory",
  Red: "Network",
  Discos: "Disks",
  Conexiones: "Connections",
  Monitor: "Monitor",
  Sección: "Section",
  "Esperando las primeras métricas del canal «metrics»…": "Waiting for the first metrics on the “metrics” channel…",
  "Canal «metrics» · 4 muestras por segundo · últimos 60 s": "“metrics” channel · 4 samples per second · last 60 s",
  "Carga {load}": "Load {load}",

  // Resumen
  "{n} núcleo": "{n} core",
  "{n} núcleos": "{n} cores",
  "de {total}": "of {total}",
  Usada: "Used",
  Recibido: "Received",
  Enviado: "Sent",
  Disco: "Disk",
  Lectura: "Read",
  Escritura: "Write",
  "Temperatura de CPU": "CPU temperature",
  Alta: "High",
  Elevada: "Elevated",
  Normal: "Normal",
  Ventilador: "Fan",
  "Carga (1 min)": "Load (1 min)",
  "Carga (5 / 15 min)": "Load (5 / 15 min)",
  "Memoria en caché": "Cached memory",

  // CPU
  "Uso total": "Total usage",
  "Por núcleo": "Per core",
  "Núcleo {n}": "Core {n}",
  "Carga 1 min": "Load 1 min",
  "Carga 5 min": "Load 5 min",
  "Carga 15 min": "Load 15 min",
  Temperatura: "Temperature",

  // Memoria
  "Usada por servicios": "Used by services",
  "Caché de archivos": "File cache",
  Libre: "Free",
  "Uso de memoria": "Memory usage",
  "{total} en total": "{total} total",
  "Usada {used}, caché {cache}, libre {free}": "Used {used}, cache {cache}, free {free}",
  "Últimos 60 segundos": "Last 60 seconds",
  "Usada + caché": "Used + cache",
  "La caché de archivos acelera las lecturas repetidas desde los volúmenes. Linux la libera sola cuando un servicio necesita memoria, así que no cuenta como ocupada.":
    "The file cache speeds up repeated reads from volumes. Linux frees it on its own when a service needs memory, so it doesn't count as used.",

  // Red
  Tráfico: "Traffic",
  Interfaz: "Interface",
  "Dirección IP": "IP address",
  Enlace: "Link",
  "full dúplex": "full duplex",
  "Pico recibido (60 s)": "Peak received (60 s)",
  "Pico enviado (60 s)": "Peak sent (60 s)",

  // Discos
  "Total de todos los discos": "Total across all disks",
  Ubicación: "Location",
  Estado: "Status",
  "Bahía {n}": "Bay {n}",
  "Interno (sistema)": "Internal (system)",
  "Sin respuesta": "Not responding",
  "En {volume}": "In {volume}",
  Sistema: "System",
  "Sin usar": "Unused",

  // Conexiones
  Navegador: "Browser",
  "App de Windows": "Windows app",
  "App de Linux": "Linux app",
  "Carpeta compartida": "Shared folder",
  Terminal: "Terminal",
  "Navegador remoto": "Remote browser",
  "Enlace público": "Public link",
  "Conexiones activas": "Active connections",
  "{n} en este momento": "{n} right now",
  Usuario: "User",
  Desde: "From",
  Protocolo: "Protocol",
  Conectado: "Connected",
  "Esta sesión": "This session",

  // WebRTC
  "Leyendo estadísticas de la conexión…": "Reading connection statistics…",
  "Toda la administración viaja por un único RTCPeerConnection cifrado": "All administration travels over a single encrypted RTCPeerConnection",
  "Archivos, eventos, métricas y la terminal comparten una conexión DTLS directa entre este navegador y el NAS. No hay API REST ni sondeos: el servidor empuja cada cambio en el momento.":
    "Files, events, metrics and the terminal share a direct DTLS connection between this browser and the NAS. There's no REST API and no polling: the server pushes every change as it happens.",
  "Estado ICE": "ICE state",
  "Latencia (RTT)": "Latency (RTT)",
  "Tipo de candidato": "Candidate type",
  "Directo (LAN)": "Direct (LAN)",
  Cifrado: "Encryption",
  "Par de candidatos seleccionado": "Selected candidate pair",
  "Este equipo": "This computer",
  "{n} canal abierto": "{n} open channel",
  "{n} canales abiertos": "{n} open channels",
  Canal: "Channel",
  Entrega: "Delivery",
  "En buffer": "Buffered",
  "Últimos 60 s": "Last 60 s",
  Mensajes: "Messages",
  "Ordenada, confiable": "Ordered, reliable",
  "Sin orden, sin reintentos": "Unordered, no retransmits",

  // Gráficos
  "En pausa": "Paused",
  Ahora: "Now",
  "Hace {n} s": "{n} s ago",
});
