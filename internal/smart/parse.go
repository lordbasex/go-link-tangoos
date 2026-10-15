// Package smart lee la salud de discos sin herramientas externas (ADR 0009):
// NVMe por NVME_IOCTL_ADMIN_CMD (Get Log Page 02h) y SATA por SG_IO con
// ATA PASS-THROUGH(16) (SMART READ DATA / READ THRESHOLDS / RETURN STATUS).
//
// Los parsers de este archivo son puros y se prueban con fixtures de bytes.
package smart

import (
	"encoding/binary"
	"errors"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// SectorSize es el tamaño de las páginas SMART (ATA y NVMe).
const SectorSize = 512

// Atributos ATA que se reportan.
var ataNames = map[uint8]string{
	1:   "Raw read error rate",
	5:   "Reallocated sectors",
	9:   "Power-on hours",
	10:  "Spin retry count",
	12:  "Power cycle count",
	187: "Reported uncorrectable",
	190: "Airflow temperature",
	194: "Temperature",
	197: "Current pending sectors",
	198: "Offline uncorrectable",
	199: "UDMA CRC errors",
}

// ATAReport es el resultado de parsear SMART READ DATA + READ THRESHOLDS.
type ATAReport struct {
	Attributes  []agentproto.SmartAttr
	TempC       *int
	PowerOnH    *uint64
	Reallocated *uint64
	Pending     *uint64
	Uncorrect   *uint64
	AnyFailing  bool
	// Count es la cantidad de atributos presentes (id ≠ 0), reportados o no.
	Count int
}

// ParseATA interpreta la página de datos SMART (512 bytes) y, si está, la de umbrales.
// Formato: 30 entradas de 12 bytes desde el offset 2.
func ParseATA(data, thresholds []byte) (*ATAReport, error) {
	if len(data) < SectorSize {
		return nil, errors.New("página SMART corta")
	}
	thr := map[uint8]uint8{}
	if len(thresholds) >= SectorSize {
		for i := 0; i < 30; i++ {
			e := thresholds[2+i*12 : 2+(i+1)*12]
			if e[0] != 0 {
				thr[e[0]] = e[1]
			}
		}
	}
	r := &ATAReport{}
	for i := 0; i < 30; i++ {
		e := data[2+i*12 : 2+(i+1)*12]
		id := e[0]
		if id == 0 {
			continue
		}
		r.Count++
		raw := uint64(e[5]) | uint64(e[6])<<8 | uint64(e[7])<<16 | uint64(e[8])<<24 | uint64(e[9])<<32 | uint64(e[10])<<40
		a := agentproto.SmartAttr{ID: id, Name: ataNames[id], Value: e[3], Worst: e[4], Threshold: thr[id], Raw: raw}
		a.Failing = a.Threshold > 0 && a.Value <= a.Threshold
		if a.Failing {
			r.AnyFailing = true
		}
		switch id {
		case 9:
			v := raw & 0xFFFFFFFF // algunos fabricantes empaquetan minutos en los bytes altos
			r.PowerOnH = &v
		case 194:
			t := int(raw & 0xFF)
			r.TempC = &t
		case 190:
			if r.TempC == nil {
				t := int(raw & 0xFF)
				r.TempC = &t
			}
		case 5:
			v := raw & 0xFFFFFFFF
			r.Reallocated = &v
		case 197:
			v := raw & 0xFFFFFFFF
			r.Pending = &v
		case 198:
			v := raw & 0xFFFFFFFF
			r.Uncorrect = &v
		}
		if a.Name != "" || a.Failing {
			if a.Name == "" {
				a.Name = "Atributo desconocido"
			}
			r.Attributes = append(r.Attributes, a)
		}
	}
	return r, nil
}

// NVMeReport es el resultado de parsear el SMART/Health Information log (02h).
type NVMeReport struct {
	CriticalWarning uint8
	TempC           int
	PercentageUsed  uint8
	PowerOnH        uint64
	MediaErrors     uint64
}

// ParseNVMe interpreta la página 02h. Los contadores son de 128 bits LE: se usan los 64 bajos.
func ParseNVMe(b []byte) (*NVMeReport, error) {
	if len(b) < SectorSize {
		return nil, errors.New("página NVMe corta")
	}
	kelvin := int(binary.LittleEndian.Uint16(b[1:3]))
	return &NVMeReport{
		CriticalWarning: b[0],
		TempC:           kelvin - 273,
		PercentageUsed:  b[5],
		PowerOnH:        binary.LittleEndian.Uint64(b[128:136]),
		MediaErrors:     binary.LittleEndian.Uint64(b[160:168]),
	}, nil
}

// HealthATA resume el estado: crit si falla un atributo o el disco reporta umbral superado;
// warn ante sectores reasignados, pendientes o incorregibles.
func HealthATA(r *ATAReport, statusFailed bool) string {
	switch {
	case r.AnyFailing || statusFailed:
		return "crit"
	case nz(r.Reallocated) || nz(r.Pending) || nz(r.Uncorrect):
		return "warn"
	}
	return "ok"
}

// HealthNVMe: crit ante cualquier critical warning; warn por desgaste ≥ 90 % o errores de medio.
func HealthNVMe(r *NVMeReport) string {
	switch {
	case r.CriticalWarning != 0:
		return "crit"
	case r.PercentageUsed >= 90 || r.MediaErrors > 0:
		return "warn"
	}
	return "ok"
}

func nz(p *uint64) bool { return p != nil && *p > 0 }

// ParseReturnStatus lee el descriptor "ATA Status Return" (09h) del sense en formato
// descriptor (72h) de SMART RETURN STATUS: LBA mid/high 4Fh/C2h = bien, F4h/2Ch = umbral superado.
// ok=false si el sense no trae el descriptor.
func ParseReturnStatus(sense []byte) (failed bool, ok bool) {
	if len(sense) < 8 || sense[0]&0x7F != 0x72 {
		return false, false
	}
	n := int(sense[7]) + 8
	if n > len(sense) {
		n = len(sense)
	}
	for i := 8; i+1 < n; {
		code, l := sense[i], int(sense[i+1])
		if code == 0x09 && i+13 < n {
			mid, high := sense[i+9], sense[i+11]
			switch {
			case mid == 0x4F && high == 0xC2:
				return false, true
			case mid == 0xF4 && high == 0x2C:
				return true, true
			}
			return false, false
		}
		i += 2 + l
	}
	return false, false
}
