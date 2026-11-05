package crypto

// OpenWrapped exposes unwrap to the fuzz test.
func OpenWrapped(k *Keyring, id string, b []byte) ([]byte, error) {
	return k.unwrap(id, b, []byte("fuzz"))
}

// SealForTest seals with a fixed key (size checks).
func SealForTest(plain []byte) ([]byte, error) {
	a, err := newAEAD(make([]byte, KeySize))
	if err != nil {
		return nil, err
	}
	return seal(a, plain, nil)
}
