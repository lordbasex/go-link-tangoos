package main

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/wsd"
)

// startWSD publica el NAS por WS-Discovery para «Red» del Explorador de Windows
// (protocol/README.md §14). Solo si Samba está instalado: sin SMB no hay nada que abrir.
func startWSD(ctx context.Context, cfg config) {
	if cfg.dev || os.Getenv("TANGOD_NO_WSD") != "" {
		return
	}
	if _, err := os.Stat("/usr/sbin/smbd"); err != nil {
		return
	}
	id := nasID(cfg.stateDir)
	instance := time.Now().Unix()
	s := &wsd.Server{Log: slog.Default(), Device: func() wsd.Device {
		return wsd.Device{UUID: wsd.UUIDFromID(id), Hostname: shortHostname(), Workgroup: sambaWorkgroup(), InstanceID: instance}
	}}
	go func() {
		if err := s.Run(ctx); err != nil {
			slog.Warn("wsd: stopped", "err", err)
		}
	}()
}

// shortHostname lee /etc/hostname en cada llamada (cambia con el asistente de primer
// arranque; os.Hostname queda viejo con ProtectHostname).
func shortHostname() string {
	b, err := os.ReadFile("/etc/hostname")
	h := strings.TrimSpace(string(b))
	if err != nil || h == "" {
		h, _ = os.Hostname()
	}
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	return h
}

// sambaWorkgroup lee workgroup de smb.conf (por defecto WORKGROUP).
func sambaWorkgroup() string {
	f, err := os.Open("/etc/samba/smb.conf")
	if err != nil {
		return "WORKGROUP"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "workgroup") {
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		}
	}
	return "WORKGROUP"
}
