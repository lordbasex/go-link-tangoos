package api

import (
	"context"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/storage"
)

type countingSmart struct{ n int }

func (c *countingSmart) Smart(context.Context, storage.Disk) (storage.SmartInfo, error) {
	c.n++
	return storage.SmartInfo{Health: "ok"}, nil
}

func TestSmartCache(t *testing.T) {
	src := &countingSmart{}
	c := NewSmartCache(src, time.Minute)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	d := storage.Disk{ByID: "wwn-1", Serial: "S1"}
	for i := 0; i < 50; i++ {
		if _, err := c.Smart(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}
	if src.n != 1 {
		t.Fatalf("50 lecturas dentro del TTL hicieron %d consultas al agente", src.n)
	}
	now = now.Add(61 * time.Second)
	_, _ = c.Smart(context.Background(), d)
	if src.n != 2 {
		t.Fatalf("vencido el TTL esperaba una consulta nueva, hubo %d", src.n)
	}
	_, _ = c.Smart(context.Background(), storage.Disk{ByID: "wwn-2", Serial: "S2"})
	if src.n != 3 {
		t.Fatalf("otro disco debería consultar: %d", src.n)
	}
}
