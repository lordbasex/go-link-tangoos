//go:build linux

package diskops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// IsRotational indica si un disco es rotacional (HDD) según el kernel.
func IsRotational(disk string) bool {
	b, err := os.ReadFile(filepath.Join("/sys/block", disk, "queue", "rotational"))
	return err != nil || strings.TrimSpace(string(b)) != "0"
}

// MDGrowLevel cambia el nivel de un array (RAID 1 de 2 discos → RAID 5 de 2 discos, el
// paso previo a sumarle un tercero en un grupo híbrido).
func MDGrowLevel(ctx context.Context, name, level string) error {
	if err := ValidMDName(name); err != nil {
		return err
	}
	if level != "5" {
		return errors.New("conversión de nivel no permitida")
	}
	_, err := run(ctx, "mdadm", "--grow", "/dev/md/"+name, "--level="+level)
	return err
}

// FSLabel lee la etiqueta del filesystem fs en dev.
func FSLabel(dev, fs string) string {
	f, err := os.Open(dev)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, HeadSize)
	n, _ := f.ReadAt(head, 0)
	return FSLabelFrom(head[:n], fs)
}
