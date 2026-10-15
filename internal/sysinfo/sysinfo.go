// Package sysinfo answers system.info from procfs, sysfs, /etc and uname
// (no exec, docs/adr/0009).
package sysinfo

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Info is exactly the TS SystemInfo.
type Info struct {
	Hostname  string `json:"hostname"`
	Model     string `json:"model"`
	CPU       string `json:"cpu"`
	Cores     int    `json:"cores"`
	MemTotal  uint64 `json:"memTotal"`
	Version   string `json:"version"`
	Debian    string `json:"debian"`
	Kernel    string `json:"kernel"`
	IP        string `json:"ip"`
	UptimeSec int64  `json:"uptimeSec"`
	Serial    string `json:"serial"`
}

// Reader reads under Root ("/" in production; tests use a fixture tree).
type Reader struct {
	Root    string
	Version string
}

func (r Reader) path(p string) string { return filepath.Join(r.Root, p) }

// Read collects the info.
func (r Reader) Read() Info {
	// /etc/hostname en cada llamada: tangod corre con ProtectHostname (su UTS queda
	// congelado al arrancar), así que os.Hostname() no ve el cambio del asistente.
	host := strings.TrimSpace(read(r.path("etc/hostname")))
	if host == "" {
		host, _ = os.Hostname()
	}
	in := Info{
		Hostname: host,
		Model:    r.model(),
		CPU:      r.cpuModel(),
		Cores:    runtime.NumCPU(),
		MemTotal: r.memTotal(),
		Version:  r.Version,
		Debian:   r.debian(),
		Kernel:   kernel(),
		IP:       r.primaryIP(),
		Serial:   read(r.path("sys/class/dmi/id/product_serial")), // root-only: "" for tangod
	}
	if f := strings.Fields(read(r.path("proc/uptime"))); len(f) > 0 {
		v, _ := strconv.ParseFloat(f[0], 64)
		in.UptimeSec = int64(v)
	}
	return in
}

func (r Reader) model() string {
	vendor := read(r.path("sys/class/dmi/id/sys_vendor"))
	product := read(r.path("sys/class/dmi/id/product_name"))
	if product == "" {
		product = read(r.path("proc/device-tree/model")) // ARM boards
	}
	switch {
	case vendor == "" || strings.HasPrefix(product, vendor):
		return strings.TrimRight(product, "\x00")
	case product == "":
		return vendor
	}
	return vendor + " " + product
}

func (r Reader) cpuModel() string {
	var model string
	forLines(r.path("proc/cpuinfo"), func(line string) bool {
		k, v, ok := strings.Cut(line, ":")
		if ok && (strings.TrimSpace(k) == "model name" || strings.TrimSpace(k) == "Model") {
			model = strings.TrimSpace(v)
			return false
		}
		return true
	})
	if model == "" {
		model = runtime.GOARCH
	}
	return model
}

func (r Reader) memTotal() uint64 {
	var total uint64
	forLines(r.path("proc/meminfo"), func(line string) bool {
		if strings.HasPrefix(line, "MemTotal:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				n, _ := strconv.ParseUint(f[1], 10, 64)
				total = n * 1024
			}
			return false
		}
		return true
	})
	return total
}

// debian: "Debian 13.7 (trixie)" from /etc/debian_version + os-release.
func (r Reader) debian() string {
	osr := map[string]string{}
	forLines(r.path("etc/os-release"), func(line string) bool {
		if k, v, ok := strings.Cut(line, "="); ok {
			osr[k] = strings.Trim(v, `"`)
		}
		return true
	})
	ver := read(r.path("etc/debian_version"))
	name := osr["NAME"]
	if strings.HasPrefix(name, "Debian") {
		name = "Debian"
	}
	if name == "" {
		name = "Linux"
	}
	switch {
	case ver != "" && osr["VERSION_CODENAME"] != "":
		return name + " " + ver + " (" + osr["VERSION_CODENAME"] + ")"
	case ver != "":
		return name + " " + ver
	case osr["PRETTY_NAME"] != "":
		return osr["PRETTY_NAME"]
	}
	return name
}

func kernel() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return ""
	}
	return unix.ByteSliceToString(u.Release[:])
}

// primaryIP: the IPv4 of the default-route interface, else the first
// non-loopback IPv4.
func (r Reader) primaryIP() string {
	var iface string
	forLines(r.path("proc/net/route"), func(line string) bool {
		f := strings.Fields(line)
		if len(f) > 2 && f[1] == "00000000" {
			iface = f[0]
			return false
		}
		return true
	})
	ifs, _ := net.Interfaces()
	pick := func(i net.Interface) string {
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
				return n.IP.String()
			}
		}
		return ""
	}
	for _, i := range ifs {
		if i.Name == iface {
			if ip := pick(i); ip != "" {
				return ip
			}
		}
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp != 0 && i.Flags&net.FlagLoopback == 0 {
			if ip := pick(i); ip != "" {
				return ip
			}
		}
	}
	return ""
}

func read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(string(b), "\x00"))
}

func forLines(path string, fn func(string) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if !fn(sc.Text()) {
			return
		}
	}
}
