package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Notification is the plaintext the SPA service worker will receive and
// pass to showNotification. URL is opened on notificationclick.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Icon  string `json:"icon,omitempty"`
	Badge string `json:"badge,omitempty"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"` // e.g. "nodeId" — collapse duplicates
	Data  any    `json:"data,omitempty"`
}

// Subscription is the receiver-side state stored in push_subscriptions.
type Subscription struct {
	Endpoint string
	P256DH   string
	Auth     string
}

// DeliveryError tells the caller why a push failed. Permanent errors
// (404/410) mean the subscription is dead and should be revoked.
type DeliveryError struct {
	Permanent bool
	Err       error
}

func (e *DeliveryError) Error() string { return e.Err.Error() }
func (e *DeliveryError) Unwrap() error { return e.Err }

// Sender sends Web Push notifications to a single subscription.
type Sender struct {
	priv      *ecdsa.PrivateKey
	Subject   string
	Subscript Subscription
	HTTP      *http.Client
	Log       *slog.Logger
}

// NewSender builds a Sender. ttl caps the JWT lifetime (RFC 8292
// suggests 12h; we use an hour for forward secrecy).
func NewSender(vapidPriv *ecdsa.PrivateKey, sub Subject, s2 Subscription, log *slog.Logger) *Sender {
	if log == nil {
		log = slog.Default()
	}
	return &Sender{
		priv:      vapidPriv,
		Subject:   string(sub),
		Subscript: s2,
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		Log:       log,
	}
}

// Send delivers a notification. Plaintext is encrypted with aes128gcm
// (RFC 8291) and the request is signed with a VAPID JWT (RFC 8292).
func (s *Sender) Send(ctx context.Context, n Notification, ttl time.Duration) error {
	body, err := json.Marshal(n)
	if err != nil {
		return &DeliveryError{Err: fmt.Errorf("marshal: %w", err)}
	}
	enc, err := EncryptPayload(body, s.Subscript.P256DH, s.Subscript.Auth)
	if err != nil {
		return &DeliveryError{Err: err}
	}
	aud, err := audienceFor(s.Subscript.Endpoint)
	if err != nil {
		return &DeliveryError{Err: err}
	}
	jwt, err := SignVAPIDJWT(s.priv, Subject(s.Subject), aud, ttl)
	if err != nil {
		return &DeliveryError{Err: fmt.Errorf("vapid jwt: %w", err)}
	}
	pub := publicKeyB64FromECDSA(s.priv)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Subscript.Endpoint,
		bytes.NewReader(enc))
	if err != nil {
		return &DeliveryError{Err: err}
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(int(ttl.Seconds())))
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+pub)

	resp, err := s.HTTP.Do(req)
	if err != nil {
		return &DeliveryError{Err: fmt.Errorf("post: %w", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	// 404/410 = endpoint gone. Mark permanent so caller can revoke.
	permanent := resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return &DeliveryError{
		Permanent: permanent,
		Err:       fmt.Errorf("push service returned %d: %s", resp.StatusCode, string(respBody)),
	}
}

// audienceFor extracts the Web Push service origin from the endpoint URL.
func audienceFor(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", errors.New("push: endpoint must be absolute")
	}
	return u.Scheme + "://" + u.Host, nil
}
