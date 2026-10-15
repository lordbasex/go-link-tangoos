package connect

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// LinkProofContext es el prefijo del mensaje firmado (protocol/README.md §15).
const LinkProofContext = "tangoconnect link proof\n"

// LinkProof es la prueba con la que el NAS demuestra conocer un navegador vinculado
// antes de que el navegador revele su token ("el host prueba primero", como go-link):
//
//	proof = base64url(HMAC-SHA256(key = SHA-256(device_token), "tangoconnect link proof\n" + nonce)), sin padding
//
// El NAS guarda justamente SHA-256(device_token), así que puede calcularla sin el token.
func LinkProof(tokenHashHex, nonce string) (string, bool) {
	key, err := hex.DecodeString(tokenHashHex)
	if err != nil || len(key) != sha256.Size {
		return "", false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(LinkProofContext + nonce))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), true
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
