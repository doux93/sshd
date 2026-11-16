package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// EncryptPayload applies the aes128gcm content encryption defined in
// RFC 8291. The plaintext is a UTF-8 string — typically a JSON
// notification payload. The receiver (browser service worker) reverses
// the construction to recover the plaintext.
//
// p256dh and auth are base64url strings as supplied by
// PushSubscription.getKey('p256dh') / PushSubscription.getKey('auth').
func EncryptPayload(plaintext []byte, p256dh, auth string) (ciphertext []byte, err error) {
	if len(plaintext) == 0 {
		return nil, errors.New("push: empty plaintext")
	}
	if len(p256dh) == 0 || len(auth) == 0 {
		return nil, errors.New("push: missing p256dh or auth")
	}

	// 1. Decode the receiver's static public key (raw X9.62 form, 65B).
	receiverPubBytes, err := base64.RawURLEncoding.DecodeString(p256dh)
	if err != nil {
		return nil, fmt.Errorf("push: bad p256dh b64: %w", err)
	}
	receiverPub, err := ecdh.P256().NewPublicKey(receiverPubBytes)
	if err != nil {
		return nil, fmt.Errorf("push: bad p256dh point: %w", err)
	}

	// 2. Generate an ephemeral key pair. The browser will recover this
	//    from the headers and derive the same shared secret.
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	// 3. ECDH(local, receiver) -> IKM -> HKDF-SHA-256(salt=auth, info)
	//    -> PRK -> HKDF-Expand("aes128gcm"|0x01, 16) = CEK
	//                  -> HKDF-Expand("nonce"|0x01, 12)  = nonce
	ikm, err := eph.ECDH(receiverPub)
	if err != nil {
		return nil, fmt.Errorf("push: ecdh: %w", err)
	}
	authBytes, err := base64.RawURLEncoding.DecodeString(auth)
	if err != nil {
		return nil, fmt.Errorf("push: bad auth b64: %w", err)
	}
	salt := authBytes // RFC 8291 §3.2 specifies "auth" (16 bytes) as the salt
	cekInfo := []byte("Content-Encoding: aes128gcm\x00")
	nonceInfo := []byte("Content-Encoding: nonce\x00")
	prk := hkdfExtract(salt, ikm)
	cek, err := hkdfExpand(prk, cekInfo, 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdfExpand(prk, nonceInfo, 12)
	if err != nil {
		return nil, err
	}

	// 4. Pad the plaintext per RFC 8291 §5: append 0x02 (or 0x01 for 2^16-1
	//    max), then zero bytes up to the next multiple of the pad length
	//    inclusive. The decoder strips trailing zeros until the trailing
	//    0x01 or 0x02 marker. We use the 2-byte-record form (marker 0x02)
	//    since Web Push payloads are tiny.
	padded := padRecord(plaintext, 4096)

	// 5. AES-128-GCM with the 12-byte nonce and a 16-byte tag. Output is
	//    ciphertext || tag. The browser reads the tag back via the GCM
	//    machinery.
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	enc := gcm.Seal(nil, nonce, padded, []byte("Content-Encoding: aes128gcm\x00"))

	// 6. Compose the aes128gcm body per RFC 8291 §5:
	//    salt (16B) | rs (4B) | idlen (1B) | ephemeralPublicKey (idlen B) | ciphertext
	rs := uint32(4096)
	ephPub := eph.PublicKey().Bytes()
	out := make([]byte, 0, 16+4+1+len(ephPub)+len(enc))
	out = append(out, salt...)           // 16B salt
	out = appendUint32BE(out, rs)        // 4B record size
	out = append(out, byte(len(ephPub))) // 1B idlen
	out = append(out, ephPub...)         // 65B public key
	out = append(out, enc...)            // ciphertext+tag
	return out, nil
}

// padRecord appends a 0x02 marker followed by zero bytes so the total
// payload length is a positive multiple of `rs`. If the plaintext is
// already aligned, the marker is still appended (zero-length records
// are not allowed).
func padRecord(p []byte, rs uint32) []byte {
	mod := uint32(len(p)+1) % rs
	padLen := rs - mod
	if padLen == rs {
		padLen = 0
	}
	out := make([]byte, 0, len(p)+1+int(padLen))
	out = append(out, p...)
	out = append(out, 0x02)
	for i := uint32(0); i < padLen; i++ {
		out = append(out, 0x00)
	}
	return out
}

func appendUint32BE(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// hkdfExtract is HKDF-Extract (RFC 5869) with SHA-256.
func hkdfExtract(salt, ikm []byte) []byte {
	if len(salt) == 0 {
		salt = make([]byte, 32)
	}
	return hmacSHA256(salt, ikm)
}

// hkdfExpand is HKDF-Expand (RFC 5869) with SHA-256. Returns length L.
func hkdfExpand(prk, info []byte, length int) ([]byte, error) {
	if length <= 0 || length > 255*32 {
		return nil, fmt.Errorf("hkdf: bad length %d", length)
	}
	var t []byte
	out := make([]byte, 0, length)
	for i := byte(1); len(out) < length; i++ {
		buf := append(append([]byte{}, t...), info...)
		buf = append(buf, i)
		t = hmacSHA256(prk, buf)
		out = append(out, t...)
	}
	return out[:length], nil
}
