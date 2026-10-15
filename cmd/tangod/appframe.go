package main

import (
	"net"
	"regexp"
	"strings"
)

var frameHostRe = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$|^\[[0-9A-Fa-f:.]+\]$`)

// frameSrc permite que las ventanas del Centro de apps carguen la app por su proxy
// HTTPS (https://<mismo host>:<puerto propio>, protocol/README.md §9). CSP no admite
// rangos de puertos: se limita al mismo host con el que llegó el navegador.
func frameSrc(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
		if strings.Contains(h, ":") {
			host = "[" + h + "]"
		}
	}
	if host == "" || !frameHostRe.MatchString(host) {
		return "; frame-src 'self'"
	}
	return "; frame-src 'self' https://" + host + ":*"
}
