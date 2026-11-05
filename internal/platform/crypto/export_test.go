package crypto

// OpenWrapped exposes unwrap to the fuzz test.
func OpenWrapped(k *Keyring, id string, b []byte) ([]byte, error) {
	return k.unwrap(id, b, []byte("fuzz"))
}
