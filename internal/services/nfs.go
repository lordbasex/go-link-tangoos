// Package services tiene la lógica portable de los servicios de archivos de M4
// (protocol/README.md §12): exports de NFS y el plan de configfs de iSCSI (LIO). Los
// ejecuta el agente; acá solo se valida y se arma el texto, con tests.
package services

import (
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ExportsFile es el archivo que TangoOS administra (no toca /etc/exports del admin).
const ExportsFile = "/etc/exports.d/tango.exports"

var hostRe = regexp.MustCompile(`^(\*|[a-zA-Z0-9*?]([a-zA-Z0-9*?.-]{0,251}[a-zA-Z0-9*?])?)$`)

// ValidNFSHost acepta una IP, una red CIDR, un nombre o comodín (*.casa.lan).
func ValidNFSHost(h string) error {
	h = strings.TrimSpace(h)
	if h == "" {
		return errors.New("falta el equipo o la red")
	}
	if strings.Contains(h, "/") {
		if _, _, err := net.ParseCIDR(h); err != nil {
			return fmt.Errorf("%q no es una red válida (ej. 192.168.1.0/24)", h)
		}
		return nil
	}
	if net.ParseIP(h) != nil {
		return nil
	}
	if !hostRe.MatchString(h) {
		return fmt.Errorf("%q no es un equipo válido", h)
	}
	return nil
}

// ValidExportPath: solo carpetas compartidas de TangoOS (/<volumen>/@shares/<Carpeta>).
func ValidExportPath(p string) error {
	if p != path.Clean(p) || !strings.HasPrefix(p, "/") {
		return fmt.Errorf("ruta inválida %q", p)
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(parts) != 3 || parts[1] != "@shares" || parts[0] == "" || parts[2] == "" || strings.ContainsAny(p, " \t\n\"'\\") {
		return fmt.Errorf("%q no es una carpeta compartida de TangoOS", p)
	}
	return nil
}

// RenderExports arma el contenido de tango.exports. root_squash siempre; sync para no
// perder datos ante un corte; no_subtree_check (recomendado por nfs-utils).
func RenderExports(exports []agentproto.NFSExport) (string, error) {
	sorted := append([]agentproto.NFSExport(nil), exports...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	var b strings.Builder
	b.WriteString("# Generado por TangoOS: no editar a mano (se reescribe al cambiar el acceso NFS).\n")
	for _, e := range sorted {
		if len(e.Clients) == 0 {
			continue
		}
		if err := ValidExportPath(e.Path); err != nil {
			return "", err
		}
		b.WriteString(e.Path)
		for _, c := range e.Clients {
			if err := ValidNFSHost(c.Host); err != nil {
				return "", err
			}
			mode := "ro"
			if c.Access == "rw" {
				mode = "rw"
			} else if c.Access != "ro" {
				return "", fmt.Errorf("acceso inválido %q", c.Access)
			}
			fmt.Fprintf(&b, " %s(%s,sync,no_subtree_check,root_squash,sec=sys)", strings.TrimSpace(c.Host), mode)
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}
