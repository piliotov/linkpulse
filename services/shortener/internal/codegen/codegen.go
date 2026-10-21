package codegen

import (
	"crypto/rand"
	"math/big"
	"regexp"
)

const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var validCode = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

func New(length int) (string, error) {
	max := big.NewInt(int64(len(alphabet)))
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

func Valid(code string) bool {
	return validCode.MatchString(code)
}
