package auth

// HashSecret y VerifySecret exponen el argon2id del store para otros secretos que no
// son contraseñas de cuentas (contraseñas de enlaces públicos, protocol/README.md §15).
func HashSecret(secret string) string { return hashPassword(secret) }

// VerifySecret compara secret con un hash de HashSecret en tiempo constante.
func VerifySecret(encoded, secret string) bool { return verifyPassword(encoded, secret) }
