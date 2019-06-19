package secret

import (
	"crypto/rand"
	"encoding/base64"
	"math/big"
	mrand "math/rand"
	"time"
)

const (
	passwordMaxLen = 20
	passwordMinLen = 16
	passSymbols    = "ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"abcdefghijklmnopqrstuvwxyz" +
		"0123456789"
)

// GenerateKey1024 generates a mongodb key
// See: https://docs.mongodb.com/manual/core/security-internal-authentication/#keyfiles
func GenerateKey1024(ln int) ([]byte, error) {
	b := make([]byte, ln)
	_, err := rand.Read(b)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, base64.StdEncoding.EncodedLen(len(b)))
	base64.StdEncoding.Encode(buf, b)
	return buf, nil
}

// GeneratePassword generate password
func GeneratePassword() ([]byte, error) {
	mrand.Seed(time.Now().UnixNano())
	ln := mrand.Intn(passwordMaxLen-passwordMinLen) + passwordMinLen
	b := make([]byte, ln)
	for i := 0; i < ln; i++ {
		randInt, err := rand.Int(rand.Reader, big.NewInt(int64(len(passSymbols))))
		if err != nil {
			return nil, err
		}
		b[i] = passSymbols[randInt.Int64()]
	}
	buf := make([]byte, base64.StdEncoding.EncodedLen(len(b)))
	base64.StdEncoding.Encode(buf, b)

	return buf, nil
}
