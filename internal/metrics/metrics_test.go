package metrics

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T, stat, netRx, diskstats string) (proc, sys string) {
	root := t.TempDir()
	proc, sys = filepath.Join(root, "proc"), filepath.Join(root, "sys")
	write(t, filepath.Join(proc, "stat"), stat)
	write(t, filepath.Join(proc, "loadavg"), "0.50 0.40 0.30 1/200 1234\n")
	write(t, filepath.Join(proc, "meminfo"), "MemTotal:       16384000 kB\nMemFree:         1000000 kB\nMemAvailable:   11264000 kB\nBuffers:          200000 kB\nCached:          6000000 kB\nSReclaimable:     300000 kB\nShmem:            100000 kB\n")
	write(t, filepath.Join(proc, "diskstats"), diskstats)
	write(t, filepath.Join(sys, "class/net/eth0/statistics/rx_bytes"), netRx)
	write(t, filepath.Join(sys, "class/net/eth0/statistics/tx_bytes"), "1000\n")
	write(t, filepath.Join(sys, "class/net/eth0/device/vendor"), "0x1af4\n")
	write(t, filepath.Join(sys, "class/net/lo/statistics/rx_bytes"), "999999999\n") // no device: ignored
	write(t, filepath.Join(sys, "block/sda/size"), "100\n")
	write(t, filepath.Join(sys, "class/hwmon/hwmon0/name"), "coretemp\n")
	write(t, filepath.Join(sys, "class/hwmon/hwmon0/temp1_input"), "48000\n")
	write(t, filepath.Join(sys, "class/hwmon/hwmon1/name"), "nct6775\n")
	write(t, filepath.Join(sys, "class/hwmon/hwmon1/fan2_input"), "1150\n")
	return
}

func TestFrame(t *testing.T) {
	proc, sys := fixture(t,
		"cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 50 0 50 400 0 0 0 0 0 0\ncpu1 50 0 50 400 0 0 0 0 0 0\n",
		"1000\n",
		"   8       0 sda 10 0 2000 0 5 0 1000 0 0 0 0\n   8       1 sda1 10 0 2000 0 5 0 1000 0 0 0 0\n 253 0 dm-0 1 0 9999 0 1 0 9999 0 0 0 0\n")
	now := time.Unix(1000, 0)
	c := &Collector{Proc: proc, Sys: sys, now: func() time.Time { return now }}
	f0 := c.Frame()
	if f0.CPU != 0 || len(f0.PerCore) != 2 || f0.NetRx != 0 {
		t.Fatalf("first frame: %+v", f0)
	}
	// one second later: +100 busy of +200 total overall, net +2048 B, sda +4 sectors read
	write(t, filepath.Join(proc, "stat"), "cpu  150 0 150 900 0 0 0 0 0 0\ncpu0 100 0 100 400 0 0 0 0 0 0\ncpu1 50 0 50 500 0 0 0 0 0 0\n")
	write(t, filepath.Join(sys, "class/net/eth0/statistics/rx_bytes"), "3048\n")
	write(t, filepath.Join(proc, "diskstats"), "   8       0 sda 10 0 2004 0 5 0 1000 0 0 0 0\n 253 0 dm-0 1 0 99999 0 1 0 9999 0 0 0 0\n")
	now = now.Add(time.Second)
	f := c.Frame()
	if math.Abs(f.CPU-0.5) > 1e-9 || f.PerCore[0] != 1 || f.PerCore[1] != 0 {
		t.Fatalf("cpu: %+v", f)
	}
	if f.NetRx != 2048 || f.NetTx != 0 || f.DiskRead != 4*512 || f.DiskWrite != 0 {
		t.Fatalf("rates: %+v", f)
	}
	if f.Load != [3]float64{0.5, 0.4, 0.3} {
		t.Fatalf("load: %v", f.Load)
	}
	if f.MemTotal != 16384000*1024 || f.MemUsed != (16384000-11264000)*1024 || f.MemCache != (200000+6000000+300000-100000)*1024 {
		t.Fatalf("mem: %+v", f)
	}
	if f.TempCPU != 48 || f.FanRPM != 1150 {
		t.Fatalf("thermal: %+v", f)
	}
}

func TestLive(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("no procfs")
	}
	c := New()
	c.Frame()
	time.Sleep(50 * time.Millisecond)
	f := c.Frame()
	if f.MemTotal == 0 || f.CPU < 0 || f.CPU > 1 {
		t.Fatalf("live frame: %+v", f)
	}
}
