package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/wsd"
)

// wsdProbe manda un Probe de WS-Discovery y pide los metadatos de cada NAS que responda.
func wsdProbe(ctx context.Context, secs string) {
	wait := 3 * time.Second
	if n, err := strconv.Atoi(secs); err == nil && n > 0 {
		wait = time.Duration(n) * time.Second
	}
	matches, err := wsd.Probe(ctx, wait)
	if err != nil {
		fail(err)
	}
	if len(matches) == 0 {
		fail(fmt.Errorf("nadie respondió al Probe"))
	}
	for _, m := range matches {
		addr := ""
		if len(m.Addresses) > 0 {
			addr = m.Addresses[0]
		}
		fmt.Printf("ProbeMatch %s xaddrs=%s\n", addr, m.XAddrs)
		x := strings.Fields(m.XAddrs)
		if len(x) == 0 {
			continue
		}
		get, _ := wsd.GetMessage(strings.TrimPrefix(addr, "urn:uuid:"))
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, x[0], bytes.NewReader(get))
		req.Header.Set("Content-Type", "application/soap+xml")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("  metadatos: %v\n", err)
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if g, err := wsd.Parse(b); err == nil {
			fmt.Printf("  metadatos %d: pub:Computer=%s\n", resp.StatusCode, g.Computer)
		} else {
			fmt.Printf("  metadatos %d: %v\n", resp.StatusCode, err)
		}
	}
}
