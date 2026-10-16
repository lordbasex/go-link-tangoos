//go:build linux

package main

// Llave USB de los volúmenes ZFS cifrados (ADR 0016): el pendrive guarda una clave
// privada X25519 y el volumen la frase sellada para ella (tangoos:keyseal). La frase
// abierta solo existe en memoria del agente y va a zfs load-key por stdin.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/zfs"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// zfsGetProp lee una propiedad (vacío si no está puesta).
func zfsGetProp(ctx context.Context, ds, prop string) (string, error) {
	out, err := zrun(ctx, zfs.ZfsBin, nil, "get", "-H", "-p", "-o", "value", prop, ds)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	if v == "-" {
		v = ""
	}
	return v, nil
}

// zfsCheckPass verifica la frase sin cargar la clave (load-key -n). Con la clave ya
// cargada ZFS no la verifica: entonces se acepta (quien la cargó ya la sabía y el pedido
// viene de un admin de TangoOS).
func zfsCheckPass(ctx context.Context, ds, pass string) *agentproto.Error {
	if _, err := zrun(ctx, zfs.ZfsBin, []byte(pass+"\n"), "load-key", "-n", ds); err != nil && !strings.Contains(err.Error(), "already loaded") {
		return agentproto.Errf(agentproto.CodeWrongPassphrase, "la frase no es correcta")
	}
	return nil
}

// zfsKeyDevice guarda una llave nueva en el pendrive y sella la frase, o la quita.
func zfsKeyDevice(ctx context.Context, a *agentproto.ZfsKeyDeviceArgs) (any, *agentproto.Error) {
	ds := zfs.VolumeDataset(a.Pool, a.Name)
	pi, e := zfsPool(ctx, a.Pool)
	if e != nil {
		return nil, e
	}
	d := dataset(pi, ds)
	if d == nil {
		return nil, agentproto.Errf(agentproto.CodeNotFound, "no existe el volumen %s", a.Name)
	}
	if !d.Encrypted {
		return nil, agentproto.Errf(agentproto.CodeNotSupported, "%s no está cifrado", a.Name)
	}
	if e := zfsCheckPass(ctx, ds, a.Passphrase); e != nil {
		return nil, e
	}
	if a.Device == "" {
		for _, p := range []string{zfs.KeySealProp, zfs.KeyDevProp} {
			if _, err := zrun(ctx, zfs.ZfsBin, nil, "inherit", p, ds); err != nil {
				return nil, zerr(err)
			}
		}
		zfsInvalidate()
		return struct{}{}, nil
	}
	p := poolsInst
	p.luks.Lock() // el punto de montaje de la llave es uno solo
	defer p.luks.Unlock()
	kd := p.keyDeviceByUUID(a.Device)
	if kd == nil {
		return nil, agentproto.Errf(agentproto.CodeKeyDeviceMissing, "no está conectado el dispositivo %s", a.Device)
	}
	pub, priv, err := zfs.NewKeyPair()
	if err != nil {
		return nil, agentproto.Errf(agentproto.CodeInternal, "sin aleatoriedad")
	}
	seal, err := zfs.Seal(pub, a.Passphrase)
	if err != nil {
		return nil, agentproto.Errf(agentproto.CodeInternal, "no se pudo sellar la frase")
	}
	if err := diskops.MountKeyDevice(ctx, a.Device, kd.FS, true); err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo montar el dispositivo: %v", err)
	}
	path := filepath.Join(diskops.KeyMountDir, zfs.KeyDir, zfs.KeyFileName(pub))
	err = os.MkdirAll(filepath.Dir(path), 0o700)
	if err == nil {
		err = writeSynced(path, priv[:])
	}
	diskops.UnmountKeyDevice(context.Background())
	if err != nil {
		return nil, agentproto.Errf(agentproto.CodeFailed, "no se pudo escribir el archivo de clave: %v", err)
	}
	// El sello va después de escribir el archivo: si el pendrive falla, la llave vieja sigue.
	if _, err := zrun(ctx, zfs.ZfsBin, nil, "set", zfs.KeySealProp+"="+seal, zfs.KeyDevProp+"="+a.Device, ds); err != nil {
		return nil, zerr(err)
	}
	zfsInvalidate()
	return struct{}{}, nil
}

// zfsKeyPassphrase abre el sello del volumen con el archivo del pendrive.
func zfsKeyPassphrase(ctx context.Context, ds string) (string, *agentproto.Error) {
	seal, err := zfsGetProp(ctx, ds, zfs.KeySealProp)
	if err != nil {
		return "", zerr(err)
	}
	dev, _ := zfsGetProp(ctx, ds, zfs.KeyDevProp)
	pub, perr := zfs.SealPub(seal)
	if seal == "" || dev == "" || perr != nil {
		return "", agentproto.Errf(agentproto.CodeKeyDeviceMissing, "el volumen no tiene una llave USB")
	}
	p := poolsInst
	p.luks.Lock()
	defer p.luks.Unlock()
	kd := p.keyDeviceByUUID(dev)
	if kd == nil {
		return "", agentproto.Errf(agentproto.CodeKeyDeviceMissing, "conectá el dispositivo con el archivo de clave")
	}
	if err := diskops.MountKeyDevice(ctx, dev, kd.FS, false); err != nil {
		return "", agentproto.Errf(agentproto.CodeFailed, "no se pudo montar el dispositivo: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(diskops.KeyMountDir, zfs.KeyDir, zfs.KeyFileName(pub)))
	diskops.UnmountKeyDevice(context.Background())
	if err != nil || len(b) != 32 {
		return "", agentproto.Errf(agentproto.CodeKeyDeviceMissing, "el dispositivo no tiene el archivo de clave de este volumen")
	}
	var priv [32]byte
	copy(priv[:], b)
	pass, err := zfs.Open(seal, &priv)
	if err != nil {
		return "", agentproto.Errf(agentproto.CodeKeyDeviceMissing, "%v", err)
	}
	return pass, nil
}

// zfsReseal vuelve a sellar la frase nueva para la llave que ya tiene el volumen (sin
// el pendrive). Si falla, se quita la llave: un sello con la frase vieja no abriría.
func zfsReseal(ctx context.Context, ds, pass string) error {
	seal, _ := zfsGetProp(ctx, ds, zfs.KeySealProp)
	if seal == "" {
		return nil
	}
	pub, err := zfs.SealPub(seal)
	var next string
	if err == nil {
		next, err = zfs.Seal(pub, pass)
	}
	if err == nil {
		_, err = zrun(ctx, zfs.ZfsBin, nil, "set", zfs.KeySealProp+"="+next, ds)
	}
	if err != nil {
		_, _ = zrun(ctx, zfs.ZfsBin, nil, "inherit", zfs.KeySealProp, ds)
		_, _ = zrun(ctx, zfs.ZfsBin, nil, "inherit", zfs.KeyDevProp, ds)
	}
	zfsInvalidate()
	return err
}
