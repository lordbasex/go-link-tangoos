// Package ups habla con un UPS a través de NUT (Network UPS Tools): cliente del
// protocolo de upsd (TCP 3493) en Go, monitor con política de apagado, archivos de
// configuración de NUT para el modo local y detección de UPS por USB
// (protocol/README.md §13).
package ups

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// DefaultPort es el puerto de upsd.
const DefaultPort = 3493

// Client es una conexión a upsd. No es seguro para uso concurrente.
type Client struct {
	conn net.Conn
	rd   *bufio.Reader
}

// Dial conecta con upsd.
func Dial(ctx context.Context, host string, port int) (*Client, error) {
	if port <= 0 {
		port = DefaultPort
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, rd: bufio.NewReaderSize(conn, 16<<10)}, nil
}

// Close cierra la conexión (con LOGOUT, cortesía del protocolo).
func (c *Client) Close() error {
	_ = c.conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	_, _ = c.conn.Write([]byte("LOGOUT\n"))
	return c.conn.Close()
}

// ErrNUT es un "ERR <código>" de upsd.
type ErrNUT struct{ Code string }

func (e *ErrNUT) Error() string { return "upsd: " + e.Code }

func (c *Client) cmd(ctx context.Context, line string) error {
	if dl, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(dl)
	} else {
		_ = c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	}
	_, err := c.conn.Write([]byte(line + "\n"))
	return err
}

func (c *Client) line() (string, error) {
	l, err := c.rd.ReadString('\n')
	if err != nil {
		return "", err
	}
	l = strings.TrimRight(l, "\r\n")
	if strings.HasPrefix(l, "ERR ") {
		return "", &ErrNUT{Code: strings.TrimSpace(strings.TrimPrefix(l, "ERR "))}
	}
	return l, nil
}

// list manda "LIST <what>" y devuelve las líneas entre BEGIN y END (sin el prefijo).
func (c *Client) list(ctx context.Context, what string) ([][]string, error) {
	if err := c.cmd(ctx, "LIST "+what); err != nil {
		return nil, err
	}
	first, err := c.line()
	if err != nil {
		return nil, err
	}
	if first != "BEGIN LIST "+what {
		return nil, fmt.Errorf("upsd: respuesta inesperada %q", first)
	}
	var out [][]string
	for {
		l, err := c.line()
		if err != nil {
			return nil, err
		}
		if l == "END LIST "+what {
			return out, nil
		}
		f, err := splitFields(l)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
}

// UPSInfo es un UPS que publica upsd.
type UPSInfo struct {
	Name string
	Desc string
}

// ListUPS lista los UPS de upsd.
func (c *Client) ListUPS(ctx context.Context) ([]UPSInfo, error) {
	rows, err := c.list(ctx, "UPS")
	if err != nil {
		return nil, err
	}
	out := []UPSInfo{}
	for _, f := range rows {
		if len(f) >= 3 && f[0] == "UPS" {
			out = append(out, UPSInfo{Name: f[1], Desc: f[2]})
		}
	}
	return out, nil
}

// Vars devuelve las variables de un UPS (battery.charge, ups.status, …).
func (c *Client) Vars(ctx context.Context, ups string) (map[string]string, error) {
	if !validName(ups) {
		return nil, errors.New("nombre de UPS inválido")
	}
	rows, err := c.list(ctx, "VAR "+ups)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, f := range rows {
		if len(f) >= 4 && f[0] == "VAR" && f[1] == ups {
			out[f[2]] = f[3]
		}
	}
	return out, nil
}

// splitFields separa una línea de upsd respetando las comillas y los escapes "\".
func splitFields(l string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQ, esc, has := false, false, false
	for _, r := range l {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\' && inQ:
			esc = true
		case r == '"':
			inQ = !inQ
			has = true
		case r == ' ' && !inQ:
			if has || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if inQ {
		return nil, fmt.Errorf("upsd: comillas sin cerrar en %q", l)
	}
	if has || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
}

func validName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
