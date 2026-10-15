//go:build !linux

package diskops

// busy: fuera de Linux no hay dispositivos de bloque que abrir.
func (e Env) busy(string) bool { return false }
