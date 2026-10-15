// Package upstest es un upsd falso (protocolo de NUT) para tests unitarios y e2e:
// no hace falta un UPS real para probar el monitor y la política de apagado.
package upstest

import (
	"bufio"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
)

// Server responde LIST UPS y LIST VAR para un UPS con variables que se pueden cambiar.
type Server struct {
	Name string

	mu   sync.Mutex
	vars map[string]string
	ln   net.Listener
}

// Start escucha en addr ("127.0.0.1:0" para un puerto libre).
func Start(addr, name string, vars map[string]string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{Name: name, vars: map[string]string{}, ln: ln}
	for k, v := range vars {
		s.vars[k] = v
	}
	go s.serve()
	return s, nil
}

// Addr es la dirección real (host:puerto).
func (s *Server) Addr() *net.TCPAddr { return s.ln.Addr().(*net.TCPAddr) }

// Close deja de escuchar.
func (s *Server) Close() error { return s.ln.Close() }

// Set cambia una variable (por ejemplo ups.status = "OB DISCHRG").
func (s *Server) Set(k, v string) {
	s.mu.Lock()
	s.vars[k] = v
	s.mu.Unlock()
}

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func q(v string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	sc := bufio.NewScanner(c)
	w := bufio.NewWriter(c)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch {
		case len(f) == 1 && f[0] == "LOGOUT":
			fmt.Fprint(w, "OK Goodbye\n")
			w.Flush()
			return
		case len(f) == 2 && f[0] == "LIST" && f[1] == "UPS":
			fmt.Fprintf(w, "BEGIN LIST UPS\nUPS %s %s\nEND LIST UPS\n", s.Name, q("UPS de prueba"))
		case len(f) == 3 && f[0] == "LIST" && f[1] == "VAR":
			if f[2] != s.Name {
				fmt.Fprint(w, "ERR UNKNOWN-UPS\n")
				break
			}
			s.mu.Lock()
			keys := make([]string, 0, len(s.vars))
			for k := range s.vars {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Fprintf(w, "BEGIN LIST VAR %s\n", s.Name)
			for _, k := range keys {
				fmt.Fprintf(w, "VAR %s %s %s\n", s.Name, k, q(s.vars[k]))
			}
			fmt.Fprintf(w, "END LIST VAR %s\n", s.Name)
			s.mu.Unlock()
		default:
			fmt.Fprint(w, "ERR UNKNOWN-COMMAND\n")
		}
		w.Flush()
	}
}
