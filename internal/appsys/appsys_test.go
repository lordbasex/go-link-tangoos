package appsys

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestComposeArgs(t *testing.T) {
	argv, err := ComposeArgs([]string{"/usr/bin/docker-compose"}, "uptime-kuma", "/volume1/@apps/uptime-kuma", "up", "-d", "--remove-orphans")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argv, " ")
	want := "/usr/bin/docker-compose --ansi never --progress plain -p tango-uptime-kuma --project-directory /volume1/@apps/uptime-kuma -f /volume1/@apps/uptime-kuma/compose.yaml --env-file /volume1/@apps/uptime-kuma/.env up -d --remove-orphans"
	if got != want {
		t.Fatalf("argv:\n%s\nquería:\n%s", got, want)
	}
	bad := []struct {
		id, dir string
		sub     []string
	}{
		{"../x", "/volume1/@apps/../x", []string{"up"}},
		{"kuma", "/volume1/@apps/other", []string{"up"}},
		{"kuma", "/volume1/@apps/kuma", []string{"exec"}},
		{"kuma", "relative/@apps/kuma", []string{"up"}},
	}
	for _, b := range bad {
		if _, err := ComposeArgs([]string{"/usr/bin/docker-compose"}, b.id, b.dir, b.sub...); err == nil {
			t.Errorf("%+v: se esperaba error", b)
		}
	}
}

func TestUsageOf(t *testing.T) {
	var s statsJSON
	s.CPU.Usage.Total, s.PreCPU.Usage.Total = 2_000, 1_000
	s.CPU.System, s.PreCPU.System = 20_000, 10_000
	s.Memory.Usage = 100 << 20
	s.Memory.Stats = map[string]uint64{"inactive_file": 30 << 20}
	u := usageOf(s)
	if u.CPU != 0.1 || u.Mem != 70<<20 {
		t.Fatalf("%+v", u)
	}
}

func frame(stream byte, s string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
	return append(h, s...)
}

func TestDemuxAndMerge(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(frame(1, "2026-10-14T10:00:01Z \x1b[36mhola\x1b[0m\n"))
	buf.Write(frame(2, "2026-10-14T10:00:03Z error\n"))
	raw, err := demux(&buf)
	if err != nil {
		t.Fatal(err)
	}
	web := parseLogLines(raw)
	db := []LogLine{{At: "2026-10-14T10:00:02Z", Text: "db lista"}}
	got := MergeLogs(map[string][]LogLine{"web": web, "db": db}, 2, true)
	if strings.Join(got, "|") != "db | db lista|web | error" || web[0].Text != "hola" {
		t.Fatalf("%v", got)
	}
}

func TestHealth(t *testing.T) {
	st, r, u := Health([]Container{{State: "running", Status: "Up (healthy)"}, {State: "exited"}})
	if st != "partial" || r != 1 || u != 0 {
		t.Fatal(st, r, u)
	}
	if st, _, _ := Health(nil); st != "missing" {
		t.Fatal(st)
	}
}

func TestPullProgress(t *testing.T) {
	var p PullProgress
	for _, l := range []string{" uptime-kuma Pulling", " 3d75e56d73fd Pulling fs layer", " c51afa4fcef3 Pulling fs layer", " 3d75e56d73fd Downloading [==>  ]", " 3d75e56d73fd Pull complete"} {
		p.Line(l)
	}
	if got := p.Line(" c51afa4fcef3 Already exists"); got != 1 {
		t.Fatalf("progreso %v", got)
	}
}
