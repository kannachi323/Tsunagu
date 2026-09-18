// Package proxyheader keeps source credentials out of public content URLs.
package proxyheader

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Process-local authenticated encryption needs no growing token cache or worker.
// After app relaunch clients obtain new content URLs from GraphQL.
var codec = func() cipher.AEAD {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return aead
}()

func Encode(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	nonce := make([]byte, codec.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	return "v1." + base64.RawURLEncoding.EncodeToString(codec.Seal(nonce, nonce, body, nil))
}
func Decode(token string, value any) error {
	sealed := strings.HasPrefix(token, "v1.")
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "v1."))
	if err != nil {
		return err
	}
	if sealed {
		if len(raw) < codec.NonceSize() {
			return fmt.Errorf("invalid header token")
		}
		raw, err = codec.Open(nil, raw[:codec.NonceSize()], raw[codec.NonceSize():], nil)
		if err != nil {
			return fmt.Errorf("invalid header token")
		}
	}
	// Continue accepting existing desktop URLs; new URLs never contain clear headers.
	return json.Unmarshal(raw, value)
}
