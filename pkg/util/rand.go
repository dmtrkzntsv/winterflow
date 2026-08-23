package util

import (
	"crypto/rand"
	"math/big"
)

// GenerateRandomCode generates a random code of the given length
func GenerateRandomCode(length int) string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			panic(err) // This should never happen in practice
		}
		b[i] = charset[n.Int64()]
	}
	return string(b)
}
