package diskops

import (
	"errors"
	"fmt"
	"slices"
	"sort"
)

// Grupos de almacenamiento (ADR 0011): perfiles, reglas de discos y el RAID híbrido.
//
// RAID híbrido: los tamaños útiles de los discos, ordenados, marcan los límites de los
// "tramos"; cada disco lleva una partición GPT por tramo que alcanza y cada tramo es un md
// con las particiones de los discos que llegan a él. Así los discos de distinto tamaño
// se aprovechan casi enteros en vez de cortarse en el más chico.

// PoolProfiles son los perfiles de un grupo de almacenamiento.
var PoolProfiles = []string{"basic", "raid1", "raid5", "raid6", "raid10", "hybrid1", "hybrid2"}

// IsHybrid indica si el perfil es RAID híbrido.
func IsHybrid(profile string) bool { return profile == "hybrid1" || profile == "hybrid2" }

// CheckPoolDisks valida la cantidad de discos de un grupo nuevo.
func CheckPoolDisks(profile string, n int) error {
	switch profile {
	case "hybrid1":
		if n < 2 || n > 16 {
			return fmt.Errorf("Híbrido-1 necesita entre 2 y 16 discos")
		}
		return nil
	case "hybrid2":
		if n < 4 || n > 16 {
			return fmt.Errorf("Híbrido-2 necesita entre 4 y 16 discos")
		}
		return nil
	}
	return CheckProfileDisks(profile, n)
}

const (
	mib = uint64(1) << 20
	// PartAlign es la alineación de las particiones (y su comienzo: 1 MiB).
	PartAlign = mib
	// partTail es lo que se reserva al final del disco para la copia de la GPT.
	partTail = mib
)

// HybridUsable es el espacio de un disco que se reparte en tramos: desde 1 MiB hasta
// 1 MiB antes del final, alineado a 1 MiB.
func HybridUsable(size uint64) uint64 {
	if size <= PartAlign+partTail+PartAlign {
		return 0
	}
	return (size - PartAlign - partTail) / PartAlign * PartAlign
}

// Tier es un tramo de un grupo híbrido (o el único tramo de un grupo RAID).
type Tier struct {
	Index   int    // 1..N
	Start   uint64 // desde el comienzo de los datos de cada disco (las particiones arrancan en 1 MiB + Start)
	Size    uint64 // por miembro
	Members []int  // índices de los discos que llegan a este tramo
	Level   string // raid1 | raid5 | raid6 | unused (híbrido); basic | raid1 | raid5 | raid6 | raid10 (RAID)
	Usable  uint64
}

// HybridLevel decide el nivel de un tramo según cuántos discos lo alcanzan.
func HybridLevel(profile string, members int) string {
	switch profile {
	case "hybrid1":
		switch {
		case members >= 3:
			return "raid5"
		case members == 2:
			return "raid1"
		}
	case "hybrid2":
		if members >= 4 {
			return "raid6"
		}
	}
	return "unused"
}

func levelUsable(level string, members int, size uint64) uint64 {
	n := uint64(members)
	switch level {
	case "basic", "raid1":
		return size
	case "raid5":
		return (n - 1) * size
	case "raid6":
		return (n - 2) * size
	case "raid10":
		return n / 2 * size
	}
	return 0
}

// HybridBoundaries calcula los límites de los tramos a partir de los tamaños útiles: cada
// tamaño distinto es un límite; los que difieren menos de 1 % se juntan en el menor (dos
// discos "de 4 TB" de marcas distintas no tienen el mismo tamaño exacto).
func HybridBoundaries(usable []uint64) []uint64 {
	s := slices.Clone(usable)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	var out []uint64
	for _, u := range s {
		if u == 0 {
			continue
		}
		if n := len(out); n > 0 && u-out[n-1] <= out[n-1]/100 {
			continue
		}
		out = append(out, u)
	}
	return out
}

// TiersFromBoundaries arma los tramos con límites fijos: el disco i participa del tramo k
// si su tamaño útil llega al límite k.
func TiersFromBoundaries(profile string, boundaries, usable []uint64) []Tier {
	var tiers []Tier
	prev := uint64(0)
	for k, b := range boundaries {
		t := Tier{Index: k + 1, Start: prev, Size: b - prev}
		for i, u := range usable {
			if u >= b {
				t.Members = append(t.Members, i)
			}
		}
		t.Level = HybridLevel(profile, len(t.Members))
		t.Usable = levelUsable(t.Level, len(t.Members), t.Size)
		tiers = append(tiers, t)
		prev = b
	}
	return tiers
}

// PoolLayout calcula los tramos de un grupo nuevo con estos tamaños de disco (bytes).
// En los perfiles RAID hay un solo tramo con los discos enteros (md usa el más chico).
func PoolLayout(profile string, sizes []uint64) ([]Tier, error) {
	if err := CheckPoolDisks(profile, len(sizes)); err != nil {
		return nil, err
	}
	if !IsHybrid(profile) {
		all := make([]int, len(sizes))
		for i := range all {
			all[i] = i
		}
		m := MinSize(sizes)
		return []Tier{{Index: 1, Size: m, Members: all, Level: profile, Usable: UsableBytes(profile, sizes)}}, nil
	}
	usable := make([]uint64, len(sizes))
	for i, s := range sizes {
		usable[i] = HybridUsable(s)
		if usable[i] == 0 {
			return nil, errors.New("hay un disco demasiado chico")
		}
	}
	return TiersFromBoundaries(profile, HybridBoundaries(usable), usable), nil
}

// TiersUsable suma la capacidad útil de los tramos.
func TiersUsable(tiers []Tier) uint64 {
	var n uint64
	for _, t := range tiers {
		n += t.Usable
	}
	return n
}

// TiersUnused suma el espacio de los discos que todavía no protege ningún tramo
// (tramos sin nivel): queda reservado para cuando llegue otro disco.
func TiersUnused(tiers []Tier) uint64 {
	var n uint64
	for _, t := range tiers {
		if t.Level == "unused" {
			n += t.Size * uint64(len(t.Members))
		}
	}
	return n
}

// HybridGrow calcula los límites después de sumar un disco de tamaño útil add a un grupo
// híbrido con estos límites. El disco nuevo tiene que alcanzar el último límite: sus
// particiones tienen que entrar en los tramos que ya existen.
func HybridGrow(boundaries []uint64, add uint64) ([]uint64, error) {
	if len(boundaries) == 0 {
		return nil, errors.New("el grupo no tiene tramos")
	}
	top := boundaries[len(boundaries)-1]
	if add < top {
		return nil, fmt.Errorf("el disco nuevo tiene que ser al menos tan grande como el más grande del grupo")
	}
	out := slices.Clone(boundaries)
	if add-top > top/100 {
		out = append(out, add)
	}
	return out, nil
}
