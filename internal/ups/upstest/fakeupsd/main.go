// Command fakeupsd es un upsd falso para pruebas e2e del UPS sin hardware:
//
//	fakeupsd -listen 127.0.0.1:13493 -status "OL" -charge 100
//
// Lee de la entrada estándar líneas "clave=valor" para cambiar variables en vivo
// (por ejemplo "ups.status=OB DISCHRG").
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/lordbasex/go-link-tangoos/internal/ups/upstest"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:13493", "dirección")
	name := flag.String("name", "ups", "nombre del UPS")
	status := flag.String("status", "OL", "ups.status inicial")
	charge := flag.String("charge", "100", "battery.charge inicial")
	flag.Parse()
	s, err := upstest.Start(*listen, *name, map[string]string{
		"ups.status": *status, "battery.charge": *charge, "battery.runtime": "1800",
		"ups.load": "23", "input.voltage": "220.0", "device.mfr": "TangoOS", "device.model": "UPS de prueba",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("fakeupsd escuchando en", s.Addr())
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			s.Set(strings.TrimSpace(k), strings.TrimSpace(v))
			fmt.Println("ok", k)
		}
	}
	select {} // sin stdin (nohup): seguir sirviendo
}
