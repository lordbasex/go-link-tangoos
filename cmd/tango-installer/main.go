// Command tango-installer es el instalador gráfico de TangoOS (internal/installer).
// Solo existe en el live de la ISO (capa installer.squashfs), nunca en el disco instalado.
//
//	tango-installer                     sirve la interfaz en :80 (pantalla local + LAN con código)
//	tango-installer -banner             consola de respaldo sin pantalla gráfica: muestra la URL y el
//	                                    código para instalar desde otra PC (lee /api/state como local)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/installer"
)

var version = "dev"

func main() {
	listen := flag.String("listen", ":80", "dirección HTTP")
	engine := flag.String("engine", "/usr/local/sbin/tango-install", "motor de instalación")
	lang := flag.String("lang", "es", "idioma de la pantalla local (es|en|pt)")
	banner := flag.Bool("banner", false, "mostrar en la consola la URL y el código (sin pantalla gráfica)")
	flag.Parse()
	if *banner {
		_, port, err := net.SplitHostPort(*listen)
		if err != nil {
			port = "80"
		}
		showBanner("http://127.0.0.1:" + port)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s, err := installer.New(installer.Config{Engine: *engine, Version: version, Lang: *lang, Log: log})
	if err != nil {
		log.Error("instalador", "err", err)
		os.Exit(1)
	}
	log.Info("instalador gráfico listo", "listen", *listen)
	srv := &http.Server{Addr: *listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("instalador", "err", err)
		os.Exit(1)
	}
}

// showBanner repinta la consola cada pocos segundos (la IP puede llegar tarde por DHCP).
func showBanner(base string) {
	last := ""
	for {
		var st installer.State
		text := "\n  TangoOS - instalador\n\n  Iniciando…\n"
		if resp, err := http.Get(base + "/api/state"); err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&st)
			resp.Body.Close()
			var b strings.Builder
			b.WriteString("\n  TangoOS - instalador\n\n")
			b.WriteString("  Este equipo no tiene pantalla gráfica. Instalá desde otra PC de la red:\n\n")
			if len(st.URLs) == 0 {
				b.WriteString("    (esperando una dirección IP por DHCP…)\n")
			}
			for _, u := range st.URLs {
				fmt.Fprintf(&b, "    abrí  %s\n", u)
			}
			fmt.Fprintf(&b, "\n    código  %s\n\n", spaced(st.PIN))
			switch st.Phase {
			case installer.PhaseInstalling:
				fmt.Fprintf(&b, "  Instalando: %d %%  %s\n", st.Percent, st.Message)
			case installer.PhaseDone:
				b.WriteString("  TangoOS está instalado. Reiniciá desde la otra PC.\n")
			case installer.PhaseFailed:
				fmt.Fprintf(&b, "  La instalación falló: %s\n", st.Error)
			}
			b.WriteString("\n  Para el instalador en modo texto, reiniciá y elegí \"Instalar en modo texto\".\n")
			text = b.String()
		}
		if text != last {
			fmt.Print("\033[H\033[2J" + text)
			last = text
		}
		time.Sleep(3 * time.Second)
	}
}

func spaced(pin string) string {
	if len(pin) != 6 {
		return pin
	}
	return pin[:3] + " " + pin[3:]
}
