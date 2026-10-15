package api

import (
	"context"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/storage"
)

// SmartCache evita consultar SMART al agente en cada storage.disks.list: con 13 discos y
// varios clientes (UI, widgets, monitor) se agotaba el cupo de 20 pedidos/s de la
// conexión con tango-agent y fallaban operaciones reales (hallazgo e2e 2026-10-15:
// 1221 disk.smart en 5 min → storage.volumes.plan RATE_LIMITED).
type SmartCache struct {
	src storage.SmartSource
	ttl time.Duration
	now func() time.Time

	mu sync.Mutex
	m  map[string]smartEntry
}

type smartEntry struct {
	info storage.SmartInfo
	err  error
	exp  time.Time
}

// NewSmartCache: ttl para discos con SMART; sin SMART (discos virtuales) se recuerda 10×
// más y un error del agente solo 15 s (para reintentar pronto sin martillar).
func NewSmartCache(src storage.SmartSource, ttl time.Duration) *SmartCache {
	return &SmartCache{src: src, ttl: ttl, now: time.Now, m: map[string]smartEntry{}}
}

func (c *SmartCache) Smart(ctx context.Context, d storage.Disk) (storage.SmartInfo, error) {
	key := d.ByID + "|" + d.Serial
	c.mu.Lock()
	if e, ok := c.m[key]; ok && c.now().Before(e.exp) {
		c.mu.Unlock()
		return e.info, e.err
	}
	c.mu.Unlock()
	info, err := c.src.Smart(ctx, d)
	ttl := c.ttl
	switch {
	case err != nil:
		ttl = 15 * time.Second
	case info.Health == "unknown":
		ttl = 10 * c.ttl
	}
	c.mu.Lock()
	c.m[key] = smartEntry{info: info, err: err, exp: c.now().Add(ttl)}
	c.mu.Unlock()
	return info, err
}
