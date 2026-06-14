package crypto

import (
	"crypto/sha256"
	"encoding/hex"
)

type TokenHasher struct {
	Pepper string
}

func NewTokenHasher(pepper string) *TokenHasher {
	return &TokenHasher{Pepper: pepper}
}

func (h *TokenHasher) Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw + h.Pepper))
	return hex.EncodeToString(sum[:])
}
