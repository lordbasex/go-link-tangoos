package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSecretsKeptAndHidden(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	c := s.Get()
	c.Ntfy = Ntfy{Enabled: true, Server: "https://ntfy.example", Topic: "tango", Token: "tk_secret"}
	c.Telegram = Telegram{Enabled: true, BotToken: "123:abc", ChatID: "42"}
	if err := s.Set(c); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.Ntfy.Token != "" || got.Telegram.BotToken != "" {
		t.Fatal("Get must hide secrets")
	}
	got.Ntfy.Topic = "tango2" // reenvío sin secretos: se conservan
	if err := s.Set(got); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dir + "/notify.json")
	if !strings.Contains(string(b), "tk_secret") || !strings.Contains(string(b), "123:abc") || !strings.Contains(string(b), "tango2") {
		t.Fatalf("secrets lost: %s", b)
	}
	st, _ := os.Stat(dir + "/notify.json")
	if st.Mode().Perm() != 0o600 {
		t.Fatal("perm")
	}
	bad := got
	bad.Ntfy.Topic = "a/b"
	if s.Set(bad) == nil {
		t.Fatal("invalid topic accepted")
	}
}

func TestNtfyAndTelegramPayloads(t *testing.T) {
	var ntfyTitle, ntfyPrio, ntfyAuth, ntfyBody, tgPath string
	var tg map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/bot") {
			tgPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&tg)
			return
		}
		ntfyTitle, ntfyPrio, ntfyAuth = r.Header.Get("Title"), r.Header.Get("Priority"), r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		ntfyBody = string(b)
	}))
	defer srv.Close()
	s := Open(t.TempDir())
	s.TelegramAPI = srv.URL
	c := s.Get()
	c.Levels = []string{"crit"}
	c.Ntfy = Ntfy{Enabled: true, Server: srv.URL, Topic: "nas", Token: "tok"}
	c.Telegram = Telegram{Enabled: true, BotToken: "1:x", ChatID: "99"}
	if err := s.Set(c); err != nil {
		t.Fatal(err)
	}
	s.Dispatch(context.Background(), Message{Level: "warn", Title: "no", Body: "no"}, nil)
	if ntfyTitle != "" {
		t.Fatal("warn must not be sent when only crit is selected")
	}
	s.Dispatch(context.Background(), Message{Level: "crit", Title: "volume1 sin redundancia", Body: "Reemplazá el disco"}, nil)
	if ntfyTitle != "volume1 sin redundancia" || ntfyPrio != "5" || ntfyAuth != "Bearer tok" || ntfyBody != "Reemplazá el disco" {
		t.Fatalf("ntfy: %q %q %q %q", ntfyTitle, ntfyPrio, ntfyAuth, ntfyBody)
	}
	if tgPath != "/bot1:x/sendMessage" || tg["chat_id"] != "99" || !strings.Contains(tg["text"], "volume1") {
		t.Fatalf("telegram: %s %v", tgPath, tg)
	}
}

// Servidor SMTP mínimo en memoria (sin TLS) para probar el envío.
func TestSMTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		w := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
		w("220 test")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					w("250 ok")
					got <- data.String()
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				w("250 test")
			case strings.HasPrefix(cmd, "DATA"):
				inData = true
				w("354 go")
			case strings.HasPrefix(cmd, "QUIT"):
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	s := Open(t.TempDir())
	c := s.Get()
	p := 0
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	c.Email = Email{Enabled: true, Host: host, Port: p, From: "nas@casa.lan", To: "yo@casa.lan", TLS: "none"}
	if err := s.Set(c); err != nil {
		t.Fatal(err)
	}
	if err := s.Test(context.Background(), "email"); err != nil {
		t.Fatal(err)
	}
	msg := <-got
	if !strings.Contains(msg, "To: yo@casa.lan") || !strings.Contains(msg, "funcionan") {
		t.Fatalf("mail: %s", msg)
	}
}
