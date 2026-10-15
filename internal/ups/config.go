package ups

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Validate revisa una configuración antes de guardarla o aplicarla.
func (c Config) Validate() error {
	switch c.Mode {
	case "none":
		return nil
	case "local", "remote":
	default:
		return fmt.Errorf("modo inválido %q", c.Mode)
	}
	if !validName(c.UPSName) {
		return errors.New("nombre de UPS inválido (letras, números, - _ .)")
	}
	if c.Mode == "remote" {
		if c.Host == "" || strings.ContainsAny(c.Host, " /\\\"'\n") {
			return errors.New("indicá la dirección del equipo con el UPS")
		}
		if ip := net.ParseIP(c.Host); ip == nil && !validHostname(c.Host) {
			return errors.New("dirección inválida")
		}
		if c.Port < 1 || c.Port > 65535 {
			return errors.New("puerto inválido")
		}
	}
	switch c.Policy.Action {
	case "shutdown", "notify":
	default:
		return errors.New("acción inválida (shutdown o notify)")
	}
	if c.Policy.AfterMinutes < 0 || c.Policy.AfterMinutes > 240 || c.Policy.BelowPercent < 0 || c.Policy.BelowPercent > 95 {
		return errors.New("límites fuera de rango (0–240 minutos, 0–95 %)")
	}
	if c.User != "" && !validName(c.User) {
		return errors.New("usuario de NUT inválido")
	}
	if strings.ContainsAny(c.Password, "\n\r\"") {
		return errors.New("la contraseña no puede tener comillas ni saltos de línea")
	}
	return nil
}

func validHostname(h string) bool {
	if len(h) > 253 {
		return false
	}
	for _, p := range strings.Split(h, ".") {
		if p == "" || len(p) > 63 {
			return false
		}
		for _, r := range p {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// NUTFiles son los archivos de /etc/nut para el modo local (UPS por USB en este equipo).
// monPassword es la contraseña del usuario "tango" de upsd (la genera tangod).
func NUTFiles(c Config, monPassword string) (map[string]string, error) {
	if c.Mode != "local" {
		return nil, errors.New("los archivos de NUT son solo para el modo local")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if strings.ContainsAny(monPassword, "\n\r\" ") || len(monPassword) < 12 {
		return nil, errors.New("contraseña interna de NUT inválida")
	}
	mode := "standalone"
	listen := "LISTEN 127.0.0.1 3493\nLISTEN ::1 3493\n"
	if c.NetServer {
		mode = "netserver"
		listen = "LISTEN 0.0.0.0 3493\nLISTEN :: 3493\n"
	}
	head := "# Generado por TangoOS: los cambios a mano se pisan desde Ajustes → Hardware y energía.\n"
	return map[string]string{
		"nut.conf":   head + "MODE=" + mode + "\n",
		"ups.conf":   head + "maxretry = 3\n\n[" + c.UPSName + "]\n\tdriver = usbhid-ups\n\tport = auto\n\tdesc = \"UPS de TangoOS\"\n",
		"upsd.conf":  head + listen,
		"upsd.users": head + "[tango]\n\tpassword = " + monPassword + "\n\tupsmon primary\n",
	}, nil
}

// Detected es un UPS USB conectado.
type Detected struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	ID      string `json:"id"` // vvvv:pppp
}

// upsVendors son fabricantes de UPS que maneja usbhid-ups (idVendor USB).
var upsVendors = map[string]string{
	"051d": "APC", "0463": "Eaton / MGE", "0764": "CyberPower", "09ae": "Tripp Lite",
	"0d9f": "Powercom", "050d": "Belkin", "06da": "Phoenixtec / Liebert", "10af": "Liebert",
	"2b2d": "Ever", "0665": "Cypress (genérico)", "047c": "Dell", "03f0": "HP",
}

// DetectUSB lista UPS USB leyendo sysfs (root normalmente "/sys").
func DetectUSB(root string) []Detected {
	out := []Detected{}
	dirs, _ := filepath.Glob(filepath.Join(root, "bus/usb/devices/*"))
	sort.Strings(dirs)
	for _, d := range dirs {
		v := readTrim(filepath.Join(d, "idVendor"))
		name, ok := upsVendors[strings.ToLower(v)]
		if !ok {
			continue
		}
		// El vendor 03f0/047c/0665 también hace otras cosas: solo si la interfaz es HID.
		if v == "03f0" || v == "047c" || v == "0665" {
			if !hasHID(d) {
				continue
			}
		}
		p := readTrim(filepath.Join(d, "idProduct"))
		prod := firstNonEmpty(readTrim(filepath.Join(d, "product")), name)
		out = append(out, Detected{Vendor: firstNonEmpty(readTrim(filepath.Join(d, "manufacturer")), name), Product: prod, ID: strings.ToLower(v + ":" + p)})
	}
	return out
}

func hasHID(dev string) bool {
	ifs, _ := filepath.Glob(dev + "/*:*/bInterfaceClass")
	for _, f := range ifs {
		if readTrim(f) == "03" {
			return true
		}
	}
	return false
}

func readTrim(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
