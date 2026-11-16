// Package push implements Web Push (RFC 8030), including VAPID (RFC 8292)
// JWT authentication and the aes128gcm content encryption scheme (RFC 8291).
//
// The goal is zero-dependency: only crypto/ecdsa, crypto/ecdh, crypto/aes,
// crypto/hmac and crypto/rand. The math is small and well-tested; the
// value of pulling in a third-party library for this would mostly be
// saving us from the prose of RFC 8291.
package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// VAPIDKeyPair is one VAPID key as stored on auth_config.
type VAPIDKeyPair struct {
	// PrivateScalar is the EC P-256 private key scalar (32 bytes).
	PrivateScalar []byte
	// PublicUncompressed is the X9.62 uncompressed public point (65 bytes:
	// 0x04 || X || Y). This is the value pushed to the SPA so the browser
	// can build the PushSubscription.
	PublicUncompressed []byte
}

// GenerateVAPIDKey mints a fresh EC P-256 keypair suitable for VAPID.
func GenerateVAPIDKey() (*VAPIDKeyPair, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return encodeVAPID(priv)
}

func encodeVAPID(priv *ecdsa.PrivateKey) (*VAPIDKeyPair, error) {
	// x509.MarshalPKCS8PrivateKey would be cleaner, but the standard EC
	// private key encoding that every Web Push browser speaks is just
	// "big-endian scalar" (32 bytes) — matching what crypto/ecdsa puts
	// in priv.D. Saving the scalar directly avoids a marshal step on
	// every load.
	scalar := make([]byte, 32)
	priv.D.FillBytes(scalar)
	pub := elliptic.Marshal(elliptic.P256(), priv.X, priv.Y) // 65B uncompressed
	return &VAPIDKeyPair{PrivateScalar: scalar, PublicUncompressed: pub}, nil
}

// DecodeVAPIDPrivate returns the *ecdsa.PrivateKey for the stored scalar.
func DecodeVAPIDPrivate(scalar []byte) (*ecdsa.PrivateKey, error) {
	if len(scalar) != 32 {
		return nil, fmt.Errorf("vapid: private scalar must be 32 bytes, got %d", len(scalar))
	}
	priv := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: elliptic.P256()},
		D:         new(big.Int).SetBytes(scalar),
	}
	priv.PublicKey.X, priv.PublicKey.Y = priv.PublicKey.ScalarBaseMult(scalar)
	return priv, nil
}

// EncodePublicKey returns the base64url-encoded uncompressed public key
// that the SPA gives to PushManager.subscribe().
func (k *VAPIDKeyPair) EncodePublicKey() string {
	return base64.RawURLEncoding.EncodeToString(k.PublicUncompressed)
}

// Subject is the VAPID "sub" claim — typically "mailto:you@example.com".
// RFC 8292 leaves it free-form; we just embed it in the JWT.
type Subject string

// SignVAPIDJWT returns an ES256 JWT suitable for the `Authorization:
// vapid t=...` header on a Web Push request (RFC 8292 §2).
//
// aud is the origin of the push service host (e.g. "https://fcm.googleapis.com").
// sub is the operator contact, typically "mailto:you@example.com" —
// required by RFC 8292 §2 and validated by real push services; leaving
// it empty causes FCM/Mozilla to reject the JWT with 401.
//
// We build the header and claims JSON by hand to keep the field order
// stable across runs (which keeps the signing input stable for tests)
// and to avoid pulling in encoding/json, which sorts map keys
// alphabetically in its own way that varies across Go releases.
func SignVAPIDJWT(priv *ecdsa.PrivateKey, sub Subject, audience string, ttl time.Duration) (string, error) {
	if audience == "" {
		return "", errors.New("vapid: audience required")
	}
	if sub == "" {
		return "", errors.New("vapid: sub required")
	}
	now := time.Now().Unix()
	header := `{"typ":"JWT","alg":"ES256"}`
	claims := fmt.Sprintf(`{"aud":%q,"exp":%d,"sub":%q}`,
		audience, now+int64(ttl.Seconds()), string(sub))
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString([]byte(header)) + "." + enc.EncodeToString([]byte(claims))
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		return "", err
	}
	// ES256 in JWT encodes R||S each as 32-byte big-endian.
	curveBytes := 32
	sig := make([]byte, 2*curveBytes)
	rb := r.Bytes()
	sb := s.Bytes()
	copy(sig[curveBytes-len(rb):curveBytes], rb)
	copy(sig[2*curveBytes-len(sb):], sb)
	return signingInput + "." + enc.EncodeToString(sig), nil
}

// EncodePrivateScalar is a convenience wrapper used by the store layer
// when persisting a freshly minted key.
func (k *VAPIDKeyPair) EncodePrivateScalar() []byte {
	out := make([]byte, len(k.PrivateScalar))
	copy(out, k.PrivateScalar)
	return out
}

// EncodePublic returns a copy of the public point bytes for storage.
func (k *VAPIDKeyPair) EncodePublic() []byte {
	out := make([]byte, len(k.PublicUncompressed))
	copy(out, k.PublicUncompressed)
	return out
}

// publicKeyB64FromECDSA returns the base64url-encoded X9.62 uncompressed
// public point for a given ECDSA private key.
func publicKeyB64FromECDSA(priv *ecdsa.PrivateKey) string {
	pub := elliptic.Marshal(priv.Curve, priv.X, priv.Y)
	return base64.RawURLEncoding.EncodeToString(pub)
}
