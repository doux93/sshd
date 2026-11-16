package push

import (
	"crypto/hmac"
	"crypto/sha256"
)

// hmacSHA256 is the canonical HMAC-SHA-256 wrapper.
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
