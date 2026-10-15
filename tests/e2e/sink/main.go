// Command sink es un receptor de notificaciones para las pruebas E2E: escucha HTTP (ntfy)
// y SMTP mínimo en loopback y anota lo que recibe, una línea JSON por mensaje.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	mu  sync.Mutex
	out *os.File
)

func record(kind string, fields map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	fields["kind"] = kind
	fields["at"] = time.Now().Format(time.RFC3339)
	b, _ := json.Marshal(fields)
	_, _ = out.Write(append(b, '\n'))
}

func main() {
	httpAddr := flag.String("http", "127.0.0.1:18081", "")
	smtpAddr := flag.String("smtp", "127.0.0.1:18025", "")
	path := flag.String("out", "/tmp/e2e-sink.log", "")
	flag.Parse()
	var err error
	if out, err = os.OpenFile(*path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err != nil {
		log.Fatal(err)
	}
	go func() {
		log.Fatal(http.ListenAndServe(*httpAddr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
			record("http", map[string]string{"path": r.URL.Path, "title": r.Header.Get("Title"), "priority": r.Header.Get("Priority"), "auth": r.Header.Get("Authorization"), "body": string(body)})
			_, _ = w.Write([]byte(`{"id":"e2e"}`))
		})))
	}()
	ln, err := net.Listen("tcp", *smtpAddr)
	if err != nil {
		log.Fatal(err)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go smtp(c)
	}
}

func smtp(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	w("220 e2e-sink ESMTP")
	var from, to string
	var data strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			w("250-e2e-sink")
			w("250 AUTH PLAIN LOGIN")
		case strings.HasPrefix(cmd, "AUTH"):
			w("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			from = strings.TrimSpace(line[10:])
			w("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			to = strings.TrimSpace(line[8:])
			w("250 ok")
		case cmd == "DATA":
			w("354 end with .")
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(l, "\r\n") == "." {
					break
				}
				data.WriteString(l)
			}
			record("smtp", map[string]string{"from": from, "to": to, "data": data.String()})
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}
