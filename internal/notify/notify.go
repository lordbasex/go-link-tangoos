// Package notify envía los avisos de TangoOS por correo (SMTP), ntfy y Telegram
// (protocol/README.md §12). La configuración vive en <state>/notify.json (0600): los
// secretos nunca salen hacia el cliente (se devuelven vacíos) y un secreto vacío en
// set conserva el actual.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Email es la configuración SMTP.
type Email struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"`
	TLS      string `json:"tls"` // starttls | tls | none
}

// Ntfy es un servidor ntfy (ntfy.sh o propio).
type Ntfy struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Topic   string `json:"topic"`
	Token   string `json:"token"`
}

// Telegram es un bot.
type Telegram struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"botToken"`
	ChatID   string `json:"chatId"`
}

// Channels es exactamente el NotifyChannels del cliente.
type Channels struct {
	Email    Email    `json:"email"`
	Ntfy     Ntfy     `json:"ntfy"`
	Telegram Telegram `json:"telegram"`
	Levels   []string `json:"levels"`
}

// Message es un aviso a enviar.
type Message struct {
	Level string
	Title string
	Body  string
}

// Service guarda la configuración y envía.
type Service struct {
	path string
	HTTP *http.Client
	// TelegramAPI se puede apuntar a un servidor de prueba.
	TelegramAPI string

	mu  sync.Mutex
	cfg Channels
}

// Open carga <dir>/notify.json (si no existe: todo apagado, niveles crit y warn).
func Open(dir string) *Service {
	s := &Service{path: filepath.Join(dir, "notify.json"), HTTP: &http.Client{Timeout: 15 * time.Second}, TelegramAPI: "https://api.telegram.org"}
	s.cfg = Channels{Email: Email{Port: 587, TLS: "starttls"}, Ntfy: Ntfy{Server: "https://ntfy.sh"}, Levels: []string{"crit", "warn"}}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.cfg)
	}
	return s
}

// Get devuelve la configuración sin secretos.
func (s *Service) Get() Channels {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Email.Password, c.Ntfy.Token, c.Telegram.BotToken = "", "", ""
	if c.Levels == nil {
		c.Levels = []string{}
	}
	return c
}

// Set valida y guarda. Un secreto vacío conserva el anterior.
func (s *Service) Set(c Channels) error {
	if err := Validate(c); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.Email.Password == "" {
		c.Email.Password = s.cfg.Email.Password
	}
	if c.Ntfy.Token == "" {
		c.Ntfy.Token = s.cfg.Ntfy.Token
	}
	if c.Telegram.BotToken == "" {
		c.Telegram.BotToken = s.cfg.Telegram.BotToken
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(s.path, b); err != nil {
		return err
	}
	s.cfg = c
	return nil
}

// Validate revisa lo mínimo para no guardar configuraciones imposibles.
func Validate(c Channels) error {
	for _, l := range c.Levels {
		if !slices.Contains([]string{"crit", "warn", "info", "ok"}, l) {
			return fmt.Errorf("nivel desconocido: %s", l)
		}
	}
	if c.Email.Enabled {
		if c.Email.Host == "" || c.Email.Port <= 0 || c.Email.Port > 65535 {
			return errors.New("correo: falta el servidor SMTP o el puerto es inválido")
		}
		if !strings.Contains(c.Email.To, "@") || !strings.Contains(c.Email.From, "@") {
			return errors.New("correo: revisá el remitente y el destinatario")
		}
		if !slices.Contains([]string{"starttls", "tls", "none"}, c.Email.TLS) {
			return errors.New("correo: modo TLS inválido")
		}
	}
	if c.Ntfy.Enabled {
		u, err := url.Parse(c.Ntfy.Server)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errors.New("ntfy: la dirección del servidor no es válida")
		}
		if c.Ntfy.Topic == "" || strings.ContainsAny(c.Ntfy.Topic, "/?# ") {
			return errors.New("ntfy: el tema es obligatorio y no puede tener / ? # ni espacios")
		}
	}
	if c.Telegram.Enabled && strings.TrimSpace(c.Telegram.ChatID) == "" {
		return errors.New("telegram: falta el chat id")
	}
	return nil
}

// Wants dice si un nivel se envía.
func (s *Service) Wants(level string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.cfg.Levels, level)
}

// Dispatch envía a todos los canales activos si el nivel está elegido. Errores al log.
func (s *Service) Dispatch(ctx context.Context, m Message, logf func(string, ...any)) {
	if !s.Wants(m.Level) {
		return
	}
	s.mu.Lock()
	c := s.cfg
	s.mu.Unlock()
	for _, ch := range []string{"email", "ntfy", "telegram"} {
		on := (ch == "email" && c.Email.Enabled) || (ch == "ntfy" && c.Ntfy.Enabled) || (ch == "telegram" && c.Telegram.Enabled)
		if !on {
			continue
		}
		if err := s.send(ctx, c, ch, m); err != nil && logf != nil {
			logf("notificación no enviada", "channel", ch, "err", err)
		}
	}
}

// Test envía un aviso de prueba por un canal (aunque esté apagado, si está configurado).
func (s *Service) Test(ctx context.Context, channel string) error {
	s.mu.Lock()
	c := s.cfg
	s.mu.Unlock()
	return s.send(ctx, c, channel, Message{Level: "info", Title: "Prueba de TangoOS", Body: "Si ves esto, las notificaciones de tu NAS funcionan."})
}

func (s *Service) send(ctx context.Context, c Channels, channel string, m Message) error {
	switch channel {
	case "email":
		return sendEmail(c.Email, m)
	case "ntfy":
		return s.sendNtfy(ctx, c.Ntfy, m)
	case "telegram":
		return s.sendTelegram(ctx, c.Telegram, m)
	}
	return fmt.Errorf("canal desconocido: %s", channel)
}

func priority(level string) string {
	switch level {
	case "crit":
		return "5"
	case "warn":
		return "4"
	}
	return "3"
}

func (s *Service) sendNtfy(ctx context.Context, n Ntfy, m Message) error {
	if n.Server == "" || n.Topic == "" {
		return errors.New("ntfy sin configurar")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(n.Server, "/")+"/"+url.PathEscape(n.Topic), strings.NewReader(m.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", m.Title)
	req.Header.Set("Priority", priority(m.Level))
	req.Header.Set("Tags", "tango,"+m.Level)
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}
	return s.do(req)
}

func (s *Service) sendTelegram(ctx context.Context, t Telegram, m Message) error {
	if t.BotToken == "" || t.ChatID == "" {
		return errors.New("telegram sin configurar")
	}
	body, _ := json.Marshal(map[string]string{"chat_id": t.ChatID, "text": "*" + m.Title + "*\n" + m.Body, "parse_mode": "Markdown"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.TelegramAPI+"/bot"+t.BotToken+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.do(req)
}

func (s *Service) do(req *http.Request) error {
	resp, err := s.HTTP.Do(req)
	if err != nil {
		// No filtrar el token del bot que va en la URL.
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("no se pudo conectar: %v", ue.Err)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("el servidor respondió %s", resp.Status)
	}
	return nil
}

func sendEmail(e Email, m Message) error {
	if e.Host == "" || e.To == "" || e.From == "" {
		return errors.New("correo sin configurar")
	}
	addr := net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	msg := BuildEmail(e.From, e.To, "[TangoOS] "+m.Title, m.Body)
	var auth smtp.Auth
	if e.User != "" {
		auth = smtp.PlainAuth("", e.User, e.Password, e.Host)
	}
	if e.TLS == "tls" {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{ServerName: e.Host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return err
		}
		c, err := smtp.NewClient(conn, e.Host)
		if err != nil {
			return err
		}
		defer c.Close()
		return deliver(c, auth, e, msg)
	}
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, e.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if e.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("el servidor SMTP no ofrece STARTTLS")
		}
		if err := c.StartTLS(&tls.Config{ServerName: e.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	return deliver(c, auth, e, msg)
}

func deliver(c *smtp.Client, auth smtp.Auth, e Email, msg []byte) error {
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return err
			}
		}
	}
	if err := c.Mail(e.From); err != nil {
		return err
	}
	for _, to := range strings.Split(e.To, ",") {
		if err := c.Rcpt(strings.TrimSpace(to)); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// BuildEmail arma un mensaje RFC 5322 en UTF-8.
func BuildEmail(from, to, subject, body string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: =?UTF-8?B?%s?=\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n",
		sanitize(from), sanitize(to), b64(subject), time.Now().Format(time.RFC1123Z))
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return b.Bytes()
}

func sanitize(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".notify.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
