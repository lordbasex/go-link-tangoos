//go:build linux

package smart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ---- NVMe ----------------------------------------------------------------------

// nvmePassthru replica struct nvme_passthru_cmd (linux/nvme_ioctl.h), 72 bytes.
type nvmePassthru struct {
	Opcode      uint8
	Flags       uint8
	Rsvd1       uint16
	NSID        uint32
	Cdw2        uint32
	Cdw3        uint32
	Metadata    uint64
	Addr        unsafe.Pointer // los punteros como unsafe.Pointer mantienen los buffers en el heap
	MetadataLen uint32
	DataLen     uint32
	Cdw10       uint32
	Cdw11       uint32
	Cdw12       uint32
	Cdw13       uint32
	Cdw14       uint32
	Cdw15       uint32
	TimeoutMs   uint32
	Result      uint32
}

// _IOWR('N', 0x41, struct nvme_admin_cmd)
const nvmeIoctlAdminCmd = 0xC0484E41

var nvmeNsRe = regexp.MustCompile(`^(nvme\d+)n\d+$`)

func readNVMe(dev string) (*NVMeReport, error) {
	// El comando de administración va al controlador (/dev/nvmeX), no al namespace.
	ctrl := dev
	if m := nvmeNsRe.FindStringSubmatch(filepath.Base(dev)); m != nil {
		ctrl = "/dev/" + m[1]
	}
	f, err := os.OpenFile(ctrl, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, SectorSize)
	cmd := nvmePassthru{
		Opcode:    0x02, // Get Log Page
		NSID:      0xFFFFFFFF,
		Addr:      unsafe.Pointer(&buf[0]),
		DataLen:   SectorSize,
		Cdw10:     (SectorSize/4-1)<<16 | 0x02, // NUMDL | LID 02h (SMART / Health)
		TimeoutMs: 5000,
	}
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), nvmeIoctlAdminCmd, uintptr(unsafe.Pointer(&cmd)))
	runtime.KeepAlive(buf)
	if errno != 0 {
		return nil, errno
	}
	if cmd.Result != 0 {
		return nil, fmt.Errorf("NVMe status 0x%x", cmd.Result)
	}
	return ParseNVMe(buf)
}

// ---- SATA por SG_IO ------------------------------------------------------------

// sgIOHdr replica struct sg_io_hdr (scsi/sg.h) en amd64/arm64, 88 bytes.
type sgIOHdr struct {
	InterfaceID    int32
	DxferDirection int32
	CmdLen         uint8
	MxSbLen        uint8
	IovecCount     uint16
	DxferLen       uint32
	Dxferp         unsafe.Pointer
	Cmdp           unsafe.Pointer
	Sbp            unsafe.Pointer
	Timeout        uint32
	Flags          uint32
	PackID         int32
	UsrPtr         unsafe.Pointer
	Status         uint8
	MaskedStatus   uint8
	MsgStatus      uint8
	SbLenWr        uint8
	HostStatus     uint16
	DriverStatus   uint16
	Resid          int32
	Duration       uint32
	Info           uint32
}

const (
	sgIO            = 0x2285
	sgDxferNone     = -1
	sgDxferFromDev  = -3
	ataSmart        = 0xB0
	smartReadData   = 0xD0
	smartReadThresh = 0xD1
	smartRetStatus  = 0xDA
)

var _ = [1]struct{}{}[unsafe.Sizeof(sgIOHdr{})-88]      // falla la compilación si el layout cambia
var _ = [1]struct{}{}[unsafe.Sizeof(nvmePassthru{})-72] // ídem

// ataPassThrough16 arma el CDB para un comando SMART.
// dataIn: PIO data-in de un sector; si no, non-data con CK_COND para leer los registros.
func ataPassThrough16(feature uint8, dataIn bool) [16]byte {
	var c [16]byte
	c[0] = 0x85
	if dataIn {
		c[1] = 4 << 1 // protocolo PIO data-in
		c[2] = 0x0E   // T_DIR=1 (del dispositivo), BYT_BLOK=1, T_LENGTH=2 (sector count)
		c[6] = 1      // 1 sector
	} else {
		c[1] = 3 << 1 // non-data
		c[2] = 0x20   // CK_COND: devolver registros en el sense
	}
	c[4] = feature
	c[10] = 0x4F // LBA mid
	c[12] = 0xC2 // LBA high
	c[14] = ataSmart
	return c
}

func sgExec(fd uintptr, cdbv [16]byte, data []byte) (sense []byte, err error) {
	cdb := cdbv[:] // copia en el heap: el kernel la lee durante la syscall
	sense = make([]byte, 32)
	h := sgIOHdr{
		InterfaceID:    'S',
		DxferDirection: sgDxferNone,
		CmdLen:         16,
		MxSbLen:        uint8(len(sense)),
		Cmdp:           unsafe.Pointer(&cdb[0]),
		Sbp:            unsafe.Pointer(&sense[0]),
		Timeout:        5000,
	}
	if len(data) > 0 {
		h.DxferDirection = sgDxferFromDev
		h.DxferLen = uint32(len(data))
		h.Dxferp = unsafe.Pointer(&data[0])
	}
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, sgIO, uintptr(unsafe.Pointer(&h)))
	runtime.KeepAlive(cdb)
	runtime.KeepAlive(data)
	runtime.KeepAlive(sense)
	if errno != 0 {
		return nil, errno
	}
	if h.HostStatus != 0 || (h.DriverStatus&0x0F) != 0 && (h.DriverStatus&0x0F) != 0x08 {
		return sense[:h.SbLenWr], fmt.Errorf("SG_IO host=0x%x driver=0x%x", h.HostStatus, h.DriverStatus)
	}
	// Con CK_COND el dispositivo responde CHECK CONDITION a propósito; con datos no debería.
	if len(data) > 0 && h.Status != 0 {
		return sense[:h.SbLenWr], fmt.Errorf("el dispositivo rechazó ATA PASS-THROUGH (status 0x%x, sense key 0x%x)", h.Status, senseKey(sense[:h.SbLenWr]))
	}
	return sense[:h.SbLenWr], nil
}

func senseKey(s []byte) byte {
	switch {
	case len(s) > 1 && s[0]&0x7F >= 0x72:
		return s[1] & 0x0F
	case len(s) > 2:
		return s[2] & 0x0F
	}
	return 0
}

func readATA(dev string) (*ATAReport, bool, error) {
	f, err := os.OpenFile(dev, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	data := make([]byte, SectorSize)
	if _, err := sgExec(f.Fd(), ataPassThrough16(smartReadData, true), data); err != nil {
		return nil, false, err
	}
	thr := make([]byte, SectorSize)
	if _, err := sgExec(f.Fd(), ataPassThrough16(smartReadThresh, true), thr); err != nil {
		thr = nil // READ THRESHOLDS es opcional (obsoleto en ACS-4)
	}
	rep, err := ParseATA(data, thr)
	if err != nil {
		return nil, false, err
	}
	if rep.Count == 0 {
		return nil, false, errors.New("el dispositivo no devolvió atributos SMART")
	}
	failed := false
	if sense, err := sgExec(f.Fd(), ataPassThrough16(smartRetStatus, false), nil); err == nil {
		failed, _ = ParseReturnStatus(sense)
	}
	return rep, failed, nil
}

// Read devuelve el resumen SMART de un dispositivo de bloque ya resuelto.
// Si el disco no soporta SMART (p. ej. un disco virtual) devuelve Supported=false.
func Read(devID, dev string) agentproto.DiskSmartResult {
	res := agentproto.DiskSmartResult{DevID: devID, Dev: dev}
	if nvmeNsRe.MatchString(filepath.Base(dev)) {
		res.Transport = "nvme"
		r, err := readNVMe(dev)
		if err != nil {
			res.Reason = "el controlador NVMe no respondió al log SMART: " + err.Error()
			return res
		}
		res.Supported = true
		res.Health = HealthNVMe(r)
		t, poh, cw, pu, me := r.TempC, r.PowerOnH, r.CriticalWarning, r.PercentageUsed, r.MediaErrors
		res.TempC, res.PowerOnH, res.CriticalWarning, res.PercentageUsed, res.MediaErrors = &t, &poh, &cw, &pu, &me
		return res
	}
	res.Transport = "ata"
	r, failed, err := readATA(dev)
	if err != nil {
		res.Transport = ""
		res.Reason = "el disco no expone SMART (ATA PASS-THROUGH no soportado): " + err.Error()
		return res
	}
	res.Supported = true
	res.Health = HealthATA(r, failed)
	res.TempC, res.PowerOnH = r.TempC, r.PowerOnH
	res.Reallocated, res.Pending, res.Uncorrect = r.Reallocated, r.Pending, r.Uncorrect
	res.Attributes = r.Attributes
	return res
}
