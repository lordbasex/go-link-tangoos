// Package metrics samples CPU, memory, network, disk I/O and temperatures
// from procfs/sysfs (docs/adr/0009: no exec) and turns two samples into a
// MetricsFrame, the JSON the `metrics` DataChannel carries at 4 Hz.
package metrics

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Frame is exactly the TS MetricsFrame (web/packages/client/src/protocol.ts).
//
// Memory, as the desktop draws it (used + cache + free = total):
//   - memUsed  = MemTotal − MemAvailable (what services really hold)
//   - memCache = Buffers + Cached + SReclaimable − Shmem, capped so that
//     memUsed + memCache ≤ memTotal (reclaimable page cache)
//
// Temperatures and fan speed are 0 when the hardware exposes none (VMs).
type Frame struct {
	T         int64      `json:"t"`
	CPU       float64    `json:"cpu"`
	PerCore   []float64  `json:"perCore"`
	Load      [3]float64 `json:"load"`
	MemUsed   uint64     `json:"memUsed"`
	MemCache  uint64     `json:"memCache"`
	MemTotal  uint64     `json:"memTotal"`
	NetRx     float64    `json:"netRx"`
	NetTx     float64    `json:"netTx"`
	DiskRead  float64    `json:"diskRead"`
	DiskWrite float64    `json:"diskWrite"`
	TempCPU   float64    `json:"tempCpu"`
	FanRPM    int        `json:"fanRpm"`
}

type cpuTimes struct{ busy, total uint64 }

type sample struct {
	at        time.Time
	cpu       cpuTimes
	cores     []cpuTimes
	netRx     uint64
	netTx     uint64
	diskRead  uint64 // bytes
	diskWrite uint64
}

// Collector reads from Proc and Sys (/proc and /sys; tests point them at
// fixture trees) and remembers the previous sample to compute rates.
type Collector struct {
	Proc string
	Sys  string

	mu   sync.Mutex
	prev *sample
	now  func() time.Time
}

// New returns a collector over the real /proc and /sys.
func New() *Collector { return &Collector{Proc: "/proc", Sys: "/sys", now: time.Now} }

// Frame takes a sample and returns rates against the previous one (the
// first call reports 0 rates and 0 CPU).
func (c *Collector) Frame() Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.now == nil {
		c.now = time.Now
	}
	cur := c.read()
	f := Frame{T: cur.at.UnixMilli()}
	if c.prev != nil {
		dt := cur.at.Sub(c.prev.at).Seconds()
		f.CPU = usage(c.prev.cpu, cur.cpu)
		f.PerCore = make([]float64, len(cur.cores))
		for i := range cur.cores {
			if i < len(c.prev.cores) {
				f.PerCore[i] = usage(c.prev.cores[i], cur.cores[i])
			}
		}
		if dt > 0 {
			f.NetRx = rate(c.prev.netRx, cur.netRx, dt)
			f.NetTx = rate(c.prev.netTx, cur.netTx, dt)
			f.DiskRead = rate(c.prev.diskRead, cur.diskRead, dt)
			f.DiskWrite = rate(c.prev.diskWrite, cur.diskWrite, dt)
		}
	} else {
		f.PerCore = make([]float64, len(cur.cores))
	}
	c.prev = &cur
	f.Load = c.load()
	f.MemTotal, f.MemUsed, f.MemCache = c.mem()
	f.TempCPU, f.FanRPM = c.thermal()
	return f
}

func usage(a, b cpuTimes) float64 {
	dt := float64(b.total - a.total)
	if b.total <= a.total || dt == 0 {
		return 0
	}
	u := float64(b.busy-a.busy) / dt
	if u < 0 {
		return 0
	}
	if u > 1 {
		return 1
	}
	return u
}

func rate(a, b uint64, dt float64) float64 {
	if b < a { // counter reset (interface down/up)
		return 0
	}
	return float64(b-a) / dt
}

func (c *Collector) read() sample {
	s := sample{at: c.now()}
	s.cpu, s.cores = c.cpu()
	s.netRx, s.netTx = c.net()
	s.diskRead, s.diskWrite = c.disks()
	return s
}

// /proc/stat: cpu user nice system idle iowait irq softirq steal …
func (c *Collector) cpu() (cpuTimes, []cpuTimes) {
	var total cpuTimes
	var cores []cpuTimes
	forLines(filepath.Join(c.Proc, "stat"), func(line string) {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			return
		}
		var t cpuTimes
		for i, v := range f[1:] {
			if i >= 8 { // guest/guest_nice are already inside user/nice
				break
			}
			n, _ := strconv.ParseUint(v, 10, 64)
			t.total += n
			if i != 3 && i != 4 { // idle, iowait
				t.busy += n
			}
		}
		if f[0] == "cpu" {
			total = t
		} else {
			cores = append(cores, t)
		}
	})
	return total, cores
}

func (c *Collector) load() [3]float64 {
	var l [3]float64
	b, err := os.ReadFile(filepath.Join(c.Proc, "loadavg"))
	if err != nil {
		return l
	}
	f := strings.Fields(string(b))
	for i := 0; i < 3 && i < len(f); i++ {
		l[i], _ = strconv.ParseFloat(f[i], 64)
	}
	return l
}

func (c *Collector) mem() (total, used, cache uint64) {
	m := map[string]uint64{}
	forLines(filepath.Join(c.Proc, "meminfo"), func(line string) {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			return
		}
		n, _ := strconv.ParseUint(f[0], 10, 64)
		m[k] = n * 1024
	})
	total = m["MemTotal"]
	if avail, ok := m["MemAvailable"]; ok && avail <= total {
		used = total - avail
	}
	c64 := int64(m["Buffers"]+m["Cached"]+m["SReclaimable"]) - int64(m["Shmem"])
	if c64 > 0 {
		cache = uint64(c64)
	}
	if used+cache > total {
		cache = total - used
	}
	return
}

// Physical interfaces only: those with a backing device (excludes lo,
// bridges, veth, docker, tun).
func (c *Collector) net() (rx, tx uint64) {
	base := filepath.Join(c.Sys, "class", "net")
	ents, _ := os.ReadDir(base)
	for _, e := range ents {
		if _, err := os.Stat(filepath.Join(base, e.Name(), "device")); err != nil {
			continue
		}
		rx += readUint(filepath.Join(base, e.Name(), "statistics", "rx_bytes"))
		tx += readUint(filepath.Join(base, e.Name(), "statistics", "tx_bytes"))
	}
	return
}

// SkipDisk reports whether a /sys/block name is not a physical disk.
func SkipDisk(name string) bool {
	for _, p := range []string{"loop", "ram", "zram", "sr", "md", "dm-", "fd", "nbd", "zd"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// /proc/diskstats: major minor name reads … sectors_read(6) … sectors_written(10)
func (c *Collector) disks() (rd, wr uint64) {
	forLines(filepath.Join(c.Proc, "diskstats"), func(line string) {
		f := strings.Fields(line)
		if len(f) < 10 || SkipDisk(f[2]) {
			return
		}
		// whole disks only: they have a /sys/block entry, partitions do not
		if _, err := os.Stat(filepath.Join(c.Sys, "block", f[2])); err != nil {
			return
		}
		r, _ := strconv.ParseUint(f[5], 10, 64)
		w, _ := strconv.ParseUint(f[9], 10, 64)
		rd += r * 512
		wr += w * 512
	})
	return
}

// CPU temperature from hwmon (coretemp, k10temp, zenpower, cpu_thermal) or
// the first CPU thermal zone; fan from the first spinning hwmon fan.
func (c *Collector) thermal() (temp float64, fan int) {
	hw, _ := filepath.Glob(filepath.Join(c.Sys, "class", "hwmon", "hwmon*"))
	for _, h := range hw {
		name := strings.TrimSpace(readString(filepath.Join(h, "name")))
		switch name {
		case "coretemp", "k10temp", "zenpower", "cpu_thermal", "soc_thermal":
			if temp == 0 {
				if v := readUint(filepath.Join(h, "temp1_input")); v > 0 {
					temp = float64(v) / 1000
				}
			}
		}
		if fan == 0 {
			fans, _ := filepath.Glob(filepath.Join(h, "fan*_input"))
			for _, f := range fans {
				if v := readUint(f); v > 0 {
					fan = int(v)
					break
				}
			}
		}
	}
	if temp == 0 {
		zones, _ := filepath.Glob(filepath.Join(c.Sys, "class", "thermal", "thermal_zone*"))
		for _, z := range zones {
			t := strings.TrimSpace(readString(filepath.Join(z, "type")))
			if t == "x86_pkg_temp" || strings.Contains(t, "cpu") || strings.Contains(t, "soc") {
				if v := readUint(filepath.Join(z, "temp")); v > 0 {
					temp = float64(v) / 1000
					break
				}
			}
		}
	}
	return
}

func forLines(path string, fn func(string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		fn(sc.Text())
	}
}

func readString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func readUint(path string) uint64 {
	n, _ := strconv.ParseUint(strings.TrimSpace(readString(path)), 10, 64)
	return n
}
