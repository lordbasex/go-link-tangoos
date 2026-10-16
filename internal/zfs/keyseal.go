package zfs

// Llave USB de un volumen ZFS cifrado (ADR 0016). ZFS tiene una sola clave de envoltura
// por raíz de cifrado (no hay ranuras como en LUKS), así que la frase sigue siendo la
// clave de ZFS y el pendrive guarda una clave privada X25519: el volumen lleva en la
// propiedad tangoos:keyseal la clave pública y la frase sellada para ella (caja
// anónima de NaCl). Con el pendrive se abre la caja y se hace load-key con la frase;
// sin él, la frase sola sigue abriendo el volumen en cualquier equipo con ZFS. Cambiar
// la frase vuelve a sellar con la misma clave pública: no hace falta el pendrive.

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"golang.org/x/crypto/nacl/box"
)

const (
	// KeyDevProp es el UUID del filesystem del pendrive (para el desbloqueo al arrancar).
	KeyDevProp = "tangoos:keydev"
	// KeySealProp es "v1." + base64url(clave pública ‖ frase sellada).
	KeySealProp = "tangoos:keyseal"
	// KeyDir es la carpeta del pendrive donde van los archivos de clave (la misma que LUKS).
	KeyDir    = "tangoos-keys"
	keySealV1 = "v1."
)

var errSeal = errors.New("sello de llave inválido")

// NewKeyPair genera la clave del pendrive.
func NewKeyPair() (pub, priv *[32]byte, err error) {
	return box.GenerateKey(rand.Reader)
}

// Seal sella la frase para la clave pública y arma el valor de KeySealProp.
func Seal(pub *[32]byte, passphrase string) (string, error) {
	sealed, err := box.SealAnonymous(nil, []byte(passphrase), pub, rand.Reader)
	if err != nil {
		return "", err
	}
	return keySealV1 + base64.RawURLEncoding.EncodeToString(append(append([]byte{}, pub[:]...), sealed...)), nil
}

// SealPub es la clave pública de un valor de KeySealProp.
func SealPub(v string) (*[32]byte, error) {
	b, err := sealBytes(v)
	if err != nil {
		return nil, err
	}
	var pub [32]byte
	copy(pub[:], b[:32])
	return &pub, nil
}

func sealBytes(v string) ([]byte, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(v), keySealV1)
	if !ok {
		return nil, errSeal
	}
	b, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil || len(b) <= 32+box.AnonymousOverhead {
		return nil, errSeal
	}
	return b, nil
}

// Open abre el sello con la clave privada del pendrive y devuelve la frase.
func Open(v string, priv *[32]byte) (string, error) {
	b, err := sealBytes(v)
	if err != nil {
		return "", err
	}
	var pub [32]byte
	copy(pub[:], b[:32])
	if derived := PubOf(priv); derived == nil || *derived != pub {
		return "", errors.New("el archivo de clave no corresponde a este volumen")
	}
	out, ok := box.OpenAnonymous(nil, b[32:], &pub, priv)
	if !ok {
		return "", errSeal
	}
	return string(out), nil
}

// PubOf deriva la clave pública de la privada.
func PubOf(priv *[32]byte) *[32]byte {
	k, err := ecdh.X25519().NewPrivateKey(priv[:])
	if err != nil {
		return nil
	}
	var pub [32]byte
	copy(pub[:], k.PublicKey().Bytes())
	return &pub
}

// KeyFileName es el nombre del archivo de clave en el pendrive: depende de la clave
// pública, no del dataset, así que sigue sirviendo después de replicar el volumen.
func KeyFileName(pub *[32]byte) string {
	h := sha256.Sum256(pub[:])
	return "zfs-" + hex.EncodeToString(h[:8]) + ".key"
}
