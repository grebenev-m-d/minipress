package service

import (
	"crypto/rand"
	"math/big"
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomString(length int) string {
	max := big.NewInt(int64(len(charset)))
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {

			panic("crypto/rand: " + err.Error())
		}
		b[i] = charset[n.Int64()]
	}
	return string(b)
}

func GeneratePublicID() string { return randomString(10) }

func GenerateEditToken() string { return randomString(32) }
