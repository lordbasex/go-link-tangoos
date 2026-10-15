package diskops

import "fmt"

// Perfiles de volumen (protocol/README.md §8) y sus reglas: cantidad de discos y
// capacidad útil antes de Btrfs. Con discos de distinto tamaño md usa en cada miembro
// el tamaño del más chico.

// RAIDLevel es el nivel de md de cada perfil ("" = sin md, Btrfs directo en el disco).
var RAIDLevel = map[string]string{"basic": "", "raid1": "1", "raid5": "5", "raid6": "6", "raid10": "10"}

// CheckProfileDisks valida la cantidad de discos de un perfil.
func CheckProfileDisks(profile string, n int) error {
	switch profile {
	case "basic":
		if n != 1 {
			return fmt.Errorf("Básico usa exactamente 1 disco")
		}
	case "raid1":
		if n != 2 {
			return fmt.Errorf("RAID 1 usa exactamente 2 discos")
		}
	case "raid5":
		if n < 3 || n > 16 {
			return fmt.Errorf("RAID 5 necesita entre 3 y 16 discos")
		}
	case "raid6":
		if n < 4 || n > 16 {
			return fmt.Errorf("RAID 6 necesita entre 4 y 16 discos")
		}
	case "raid10":
		if n < 4 || n > 16 || n%2 != 0 {
			return fmt.Errorf("RAID 10 necesita una cantidad par de discos, entre 4 y 16")
		}
	default:
		return fmt.Errorf("perfil desconocido %q", profile)
	}
	return nil
}

// UsableBytes calcula la capacidad útil de un perfil (antes de Btrfs) con estos tamaños.
func UsableBytes(profile string, sizes []uint64) uint64 {
	if len(sizes) == 0 {
		return 0
	}
	m := sizes[0]
	for _, s := range sizes[1:] {
		m = min(m, s)
	}
	n := uint64(len(sizes))
	switch profile {
	case "basic":
		return sizes[0]
	case "raid1":
		return m
	case "raid5":
		return (n - 1) * m
	case "raid6":
		return (n - 2) * m
	case "raid10":
		return n / 2 * m
	}
	return 0
}

// MixedSizes indica si los discos tienen tamaños distintos (más de 1 % de diferencia).
func MixedSizes(sizes []uint64) bool {
	if len(sizes) < 2 {
		return false
	}
	lo, hi := sizes[0], sizes[0]
	for _, s := range sizes[1:] {
		lo, hi = min(lo, s), max(hi, s)
	}
	return hi-lo > hi/100
}

// MinSize es el tamaño del disco más chico.
func MinSize(sizes []uint64) uint64 {
	if len(sizes) == 0 {
		return 0
	}
	m := sizes[0]
	for _, s := range sizes[1:] {
		m = min(m, s)
	}
	return m
}
