package secret

import (
	"crypto/rand"
	"encoding/base64"
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
