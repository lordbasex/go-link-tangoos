//go:build linux

package diskops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// LUKS2 por cryptsetup (ADR 0011). Los secretos van por stdin o por un pipe heredado
// (/dev/fd/3): nunca en argv, nunca en un archivo temporal, nunca en el log (cryptsetup
// no los repite en su salida).

// ErrWrongKey indica que ninguna ranura aceptó la frase o el archivo de clave.
var ErrWrongKey = errors.New("la frase o el archivo de clave no abren este volumen")

func cryptExit(err error) int {
	var xe *ExecError
	var ee *exec.ExitError
	if errors.As(err, &xe) && errors.As(xe.Err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// LUKSFormat formatea dev con LUKS2 (argon2id) con la frase como primera ranura.
func LUKSFormat(ctx context.Context, dev, passphrase, label string) error {
	if err := validDevPath(dev); err != nil {
		return err
	}
	if err := ValidPassphrase(passphrase); err != nil {
		return err
	}
	_, err := runStdin(ctx, passphrase, "cryptsetup", "luksFormat", "--batch-mode", "--type", "luks2",
		"--pbkdf", "argon2id", "--key-file=-", "--label", label, dev)
	return err
}

// LUKSOpen abre dev como /dev/mapper/<name> con una frase o el contenido de un archivo
// de clave (key).
func LUKSOpen(ctx context.Context, dev, name string, key []byte) error {
	if err := validDevPath(dev); err != nil {
		return err
	}
	if len(key) == 0 {
		return ErrWrongKey
	}
	_, err := runStdin(ctx, string(key), "cryptsetup", "open", "--type", "luks2", "--key-file=-", dev, name)
	if err != nil && cryptExit(err) == 2 {
		return ErrWrongKey
	}
	return err
}

// LUKSClose cierra /dev/mapper/<name> (el filesystem ya desmontado).
func LUKSClose(ctx context.Context, name string) error {
	_, err := run(ctx, "cryptsetup", "close", name)
	return err
}

// LUKSIsOpen indica si /dev/mapper/<name> existe.
func LUKSIsOpen(name string) bool {
	_, err := os.Stat("/dev/mapper/" + name)
	return err == nil
}

// LUKSResize lleva el dispositivo abierto al tamaño de su LV (después de lvextend).
func LUKSResize(ctx context.Context, name string) error {
	_, err := run(ctx, "cryptsetup", "resize", name)
	return err
}

// LUKSChangeKey cambia la frase old por new (misma ranura).
func LUKSChangeKey(ctx context.Context, dev, old, newPass string) error {
	if err := validDevPath(dev); err != nil {
		return err
	}
	if err := ValidPassphrase(newPass); err != nil {
		return err
	}
	_, err := runSecret(ctx, old, []byte(newPass), "cryptsetup", "luksChangeKey", "--batch-mode", "--pbkdf", "argon2id", "--key-file=-", dev, "/dev/fd/3")
	if err != nil && cryptExit(err) == 2 {
		return ErrWrongKey
	}
	return err
}

var keyslotRe = regexp.MustCompile(`(?m)^\s+(\d+): luks2`)

// LUKSSlots devuelve las ranuras ocupadas.
func LUKSSlots(ctx context.Context, dev string) ([]int, error) {
	out, _, err := runSplit(ctx, "cryptsetup", "luksDump", dev)
	if err != nil {
		return nil, err
	}
	sec := out
	if i := strings.Index(out, "Keyslots:"); i >= 0 {
		sec = out[i:]
		if j := strings.Index(sec, "Tokens:"); j >= 0 {
			sec = sec[:j]
		}
	}
	var slots []int
	for _, m := range keyslotRe.FindAllStringSubmatch(sec, -1) {
		n, _ := strconv.Atoi(m[1])
		slots = append(slots, n)
	}
	return slots, nil
}

// LUKSAddKey agrega newKey en la primera ranura libre (desde la 1), autorizado con una
// clave existente; devuelve la ranura.
func LUKSAddKey(ctx context.Context, dev string, existing, newKey []byte) (int, error) {
	if err := validDevPath(dev); err != nil {
		return 0, err
	}
	used, err := LUKSSlots(ctx, dev)
	if err != nil {
		return 0, err
	}
	slot := -1
	for s := 1; s < 32; s++ {
		taken := false
		for _, u := range used {
			taken = taken || u == s
		}
		if !taken {
			slot = s
			break
		}
	}
	if slot < 0 {
		return 0, errors.New("no hay ranuras LUKS libres")
	}
	_, err = runSecret(ctx, string(existing), newKey, "cryptsetup", "luksAddKey", "--batch-mode", "--pbkdf", "argon2id",
		"--key-file=-", "--new-key-slot", strconv.Itoa(slot), dev, "/dev/fd/3")
	if err != nil && cryptExit(err) == 2 {
		return 0, ErrWrongKey
	}
	return slot, err
}

// LUKSKillSlot borra una ranura, autorizado con la clave de otra.
func LUKSKillSlot(ctx context.Context, dev string, slot int, existing []byte) error {
	if err := validDevPath(dev); err != nil {
		return err
	}
	if slot < 0 || slot > 31 {
		return errors.New("ranura inválida")
	}
	_, err := runStdin(ctx, string(existing), "cryptsetup", "luksKillSlot", "--batch-mode", "--key-file=-", dev, strconv.Itoa(slot))
	if err != nil && cryptExit(err) == 2 {
		return ErrWrongKey
	}
	return err
}
