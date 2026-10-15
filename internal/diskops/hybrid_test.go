package diskops

import (
	"slices"
	"testing"
)

const tg = uint64(1) << 30

// disco de n GiB útiles exactos (HybridUsable le saca 2 MiB).
func disk(n float64) uint64 { return uint64(n*float64(tg)) + 2*mib }

func TestPoolLayoutHybrid(t *testing.T) {
	type tierWant struct {
		level   string
		members int
		usable  float64
	}
	cases := []struct {
		name    string
		profile string
		sizes   []float64
		tiers   []tierWant
		usable  float64
		unused  float64
	}{
		{"2+2+4", "hybrid1", []float64{2, 2, 4}, []tierWant{{"raid5", 3, 4}, {"unused", 1, 0}}, 4, 2},
		{"2+4+4", "hybrid1", []float64{2, 4, 4}, []tierWant{{"raid5", 3, 4}, {"raid1", 2, 2}}, 6, 0},
		{"2+2+4+4", "hybrid1", []float64{2, 2, 4, 4}, []tierWant{{"raid5", 4, 6}, {"raid1", 2, 2}}, 8, 0},
		{"4+2+4+2 (orden)", "hybrid1", []float64{4, 2, 4, 2}, []tierWant{{"raid5", 4, 6}, {"raid1", 2, 2}}, 8, 0},
		{"uno más grande que la suma", "hybrid1", []float64{1, 10}, []tierWant{{"raid1", 2, 1}, {"unused", 1, 0}}, 1, 9},
		{"dos iguales", "hybrid1", []float64{4, 4}, []tierWant{{"raid1", 2, 4}}, 4, 0},
		{"2+4+8+8", "hybrid1", []float64{2, 4, 8, 8}, []tierWant{{"raid5", 4, 6}, {"raid5", 3, 4}, {"raid1", 2, 4}}, 14, 0},
		{"híbrido-2 2+2+4+4+4", "hybrid2", []float64{2, 2, 4, 4, 4}, []tierWant{{"raid6", 5, 6}, {"unused", 3, 0}}, 6, 6},
		{"híbrido-2 4x4", "hybrid2", []float64{4, 4, 4, 4}, []tierWant{{"raid6", 4, 8}}, 8, 0},
		{"híbrido-2 2+4+4+4+4", "hybrid2", []float64{2, 4, 4, 4, 4}, []tierWant{{"raid6", 5, 6}, {"raid6", 4, 4}}, 10, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sizes := make([]uint64, len(c.sizes))
			for i, s := range c.sizes {
				sizes[i] = disk(s)
			}
			tiers, err := PoolLayout(c.profile, sizes)
			if err != nil {
				t.Fatal(err)
			}
			if len(tiers) != len(c.tiers) {
				t.Fatalf("tramos = %d, quiero %d (%+v)", len(tiers), len(c.tiers), tiers)
			}
			for i, w := range c.tiers {
				g := tiers[i]
				if g.Level != w.level || len(g.Members) != w.members || g.Usable != uint64(w.usable*float64(tg)) {
					t.Errorf("tramo %d = %s×%d %d, quiero %s×%d %.0f GiB", i+1, g.Level, len(g.Members), g.Usable, w.level, w.members, w.usable)
				}
				if g.Index != i+1 {
					t.Errorf("tramo %d con Index %d", i+1, g.Index)
				}
			}
			if got := TiersUsable(tiers); got != uint64(c.usable*float64(tg)) {
				t.Errorf("útil = %d, quiero %.0f GiB", got, c.usable)
			}
			if got := TiersUnused(tiers); got != uint64(c.unused*float64(tg)) {
				t.Errorf("sin usar = %d, quiero %.0f GiB", got, c.unused)
			}
			// Las particiones de cada disco entran en el disco.
			for i, s := range sizes {
				end := uint64(0)
				for _, tr := range tiers {
					if slices.Contains(tr.Members, i) {
						if tr.Start != end {
							t.Errorf("disco %d: tramo %d empieza en %d y el anterior termina en %d", i, tr.Index, tr.Start, end)
						}
						end = tr.Start + tr.Size
					}
				}
				if PartAlign+end+partTail > s {
					t.Errorf("disco %d: las particiones (%d) no entran en %d", i, end, s)
				}
			}
		})
	}
}

func TestPoolLayoutRAID(t *testing.T) {
	tiers, err := PoolLayout("raid5", []uint64{disk(2), disk(2), disk(4)})
	if err != nil {
		t.Fatal(err)
	}
	if len(tiers) != 1 || tiers[0].Level != "raid5" || len(tiers[0].Members) != 3 || tiers[0].Usable != 2*disk(2) {
		t.Fatalf("raid5 = %+v", tiers)
	}
	if _, err := PoolLayout("raid10", []uint64{1, 2, 3}); err == nil {
		t.Error("RAID 10 con 3 discos")
	}
	if _, err := PoolLayout("hybrid2", []uint64{disk(4), disk(4), disk(4)}); err == nil {
		t.Error("Híbrido-2 con 3 discos")
	}
	if _, err := PoolLayout("hybrid1", []uint64{disk(4)}); err == nil {
		t.Error("Híbrido-1 con 1 disco")
	}
	if _, err := PoolLayout("zfs", []uint64{disk(4)}); err == nil {
		t.Error("perfil desconocido")
	}
}

func TestHybridBoundariesMergesClose(t *testing.T) {
	got := HybridBoundaries([]uint64{4000 * mib, 4010 * mib, 2000 * mib, 0})
	if !slices.Equal(got, []uint64{2000 * mib, 4000 * mib}) {
		t.Fatalf("límites = %v", got)
	}
}

func TestHybridGrow(t *testing.T) {
	b := []uint64{2 * tg, 4 * tg}
	if got, err := HybridGrow(b, 4*tg); err != nil || !slices.Equal(got, b) {
		t.Errorf("igual al mayor: %v %v", got, err)
	}
	if got, err := HybridGrow(b, 4*tg+10*mib); err != nil || !slices.Equal(got, b) {
		t.Errorf("1 %% más grande: %v %v", got, err)
	}
	if got, err := HybridGrow(b, 8*tg); err != nil || !slices.Equal(got, []uint64{2 * tg, 4 * tg, 8 * tg}) {
		t.Errorf("más grande: %v %v", got, err)
	}
	if _, err := HybridGrow(b, 3*tg); err == nil {
		t.Error("más chico que el mayor")
	}
	// 2+2+4 → +4: el tramo de arriba pasa de 1 a 2 miembros (RAID 1).
	usable := []uint64{2 * tg, 2 * tg, 4 * tg, 4 * tg}
	tiers := TiersFromBoundaries("hybrid1", b, usable)
	if tiers[0].Level != "raid5" || len(tiers[0].Members) != 4 || tiers[1].Level != "raid1" {
		t.Errorf("después de crecer: %+v", tiers)
	}
}

func TestHybridLevel(t *testing.T) {
	for _, c := range []struct {
		p    string
		n    int
		want string
	}{
		{"hybrid1", 1, "unused"}, {"hybrid1", 2, "raid1"}, {"hybrid1", 3, "raid5"}, {"hybrid1", 8, "raid5"},
		{"hybrid2", 3, "unused"}, {"hybrid2", 4, "raid6"}, {"hybrid2", 9, "raid6"},
	} {
		if got := HybridLevel(c.p, c.n); got != c.want {
			t.Errorf("%s con %d = %s, quiero %s", c.p, c.n, got, c.want)
		}
	}
}
