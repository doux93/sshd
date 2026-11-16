package push

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"sshx/internal/alert"
	"sshx/internal/store"
)

// SubjectMailto is the default VAPID subject. RFC 8292 §2 requests a
// contact; mailto: is the cheapest valid form. Operators can override
// via configuration in a later revision.
const SubjectMailto = "mailto:admin@sshx.local"

// SubscriptionStore is the subset of *store.Store the notifier depends
// on. Defined as an interface so tests can wire a fake without
// touching SQLite.
type SubscriptionStore interface {
	ListActivePushSubscriptions() ([]store.PushSubscription, error)
	RevokePushSubscription(id string) error
	TouchPushSubscription(id string) error
}

// WebPushNotifier delivers alert.Alert values as Web Push messages.
// Constructed once at boot; passed into alert.MultiNotifier alongside
// any webhook/ntfy channels.
//
// If the VAPID keypair is not yet generated (legacy auth_config row,
// first boot of an upgrade), the notifier lazily generates and stores
// one on first Notify. This keeps "remote mode just works" without an
// explicit admin step.
type WebPushNotifier struct {
	Store     SubscriptionStore
	Subject   string
	priv      *ecdsa.PrivateKey
	pub       []byte
	loadVAPID func() (*ecdsa.PrivateKey, []byte, error)
	saveVAPID func(priv []byte, pub []byte) error
	HTTP      *http.Client
	Log       *slog.Logger

	// mintOnce guarantees we never spin more than one key generation
	// per process even if Notify is called concurrently while the key
	// is being persisted.
	mintOnce sync.Mutex
}

// NewWebPushNotifier wires the notifier against the store + VAPID
// loaders. loadVAPID returns (privScalar, pubPoint, error). saveVAPID
// persists both. Both are callables so the same notifier can be used
// against different backends (real store in prod, in-memory in tests).
func NewWebPushNotifier(s SubscriptionStore, loadVAPID func() (*ecdsa.PrivateKey, []byte, error),
	saveVAPID func([]byte, []byte) error, log *slog.Logger) *WebPushNotifier {
	if log == nil {
		log = slog.Default()
	}
	return &WebPushNotifier{
		Store:     s,
		Subject:   SubjectMailto,
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		Log:       log,
		loadVAPID: loadVAPID,
		saveVAPID: saveVAPID,
	}
}

// ensureKeys makes sure a VAPID keypair is loaded. If not, it generates
// one, persists it via saveVAPID, and caches the result for next time.
func (n *WebPushNotifier) ensureKeys() error {
	n.mintOnce.Lock()
	defer n.mintOnce.Unlock()
	if n.priv != nil {
		return nil
	}
	if n.loadVAPID != nil {
		priv, pub, err := n.loadVAPID()
		if err != nil {
			return fmt.Errorf("load vapid: %w", err)
		}
		if priv != nil && pub != nil {
			n.priv, n.pub = priv, pub
			return nil
		}
	}
	kp, err := GenerateVAPIDKey()
	if err != nil {
		return err
	}
	if n.saveVAPID != nil {
		if err := n.saveVAPID(kp.EncodePrivateScalar(), kp.EncodePublic()); err != nil {
			return fmt.Errorf("save vapid: %w", err)
		}
	}
	priv, err := DecodeVAPIDPrivate(kp.PrivateScalar)
	if err != nil {
		return err
	}
	n.priv = priv
	n.pub = kp.PublicUncompressed
	return nil
}

// PublicKey returns the base64url-encoded VAPID public key for the SPA.
// Safe to call before Notify; triggers ensureKeys.
func (n *WebPushNotifier) PublicKey() (string, error) {
	if err := n.ensureKeys(); err != nil {
		return "", err
	}
	return (&VAPIDKeyPair{PublicUncompressed: n.pub}).EncodePublicKey(), nil
}

// Notify fans out one notification per active subscription. Per RFC 8030
// a successful push returns 201; 404/410 marks the subscription dead
// and we revoke it. Other errors are logged and the row is kept (the
// device may come back online).
func (n *WebPushNotifier) Notify(ctx context.Context, a alert.Alert) error {
	if err := n.ensureKeys(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	subs, err := n.Store.ListActivePushSubscriptions()
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return nil
	}
	notif := alertToNotification(a)
	var firstErr error
	for _, sub := range subs {
		s := NewSender(n.priv, Subject(n.Subject),
			Subscription{Endpoint: sub.Endpoint, P256DH: sub.P256DH, Auth: sub.Auth},
			n.Log)
		err := s.Send(ctx, notif, 4*time.Hour)
		if err == nil {
			_ = n.Store.TouchPushSubscription(sub.ID)
			continue
		}
		var de *DeliveryError
		if e, ok := err.(*DeliveryError); ok {
			de = e
		}
		if de != nil && de.Permanent {
			if rerr := n.Store.RevokePushSubscription(sub.ID); rerr != nil {
				n.Log.Warn("revoke push sub failed", "id", sub.ID, "err", rerr)
			} else {
				n.Log.Info("revoked dead push subscription", "id", sub.ID)
			}
		} else {
			n.Log.Warn("push delivery failed", "id", sub.ID, "err", err)
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// alertToNotification maps the alert domain object to the JSON the
// service worker showNotification() will receive.
func alertToNotification(a alert.Alert) Notification {
	level := string(a.Level)
	if level == "" {
		level = string(alert.LevelWarning)
	}
	host := a.NodeLabel
	if host == "" {
		host = a.NodeID
	}
	return Notification{
		Title: fmt.Sprintf("[%s] %s", level, host),
		Body:  a.Message,
		Icon:  "/icons/icon-192.png",
		Badge: "/icons/icon-192.png",
		Tag:   "alert-" + a.NodeID,
		URL:   "/?panel=alerts&node=" + url.QueryEscape(a.NodeID),
		Data:  map[string]any{"nodeId": a.NodeID, "severity": level, "ts": a.TS.Unix()},
	}
}

// SubjectForTest is exported for tests that need to override the
// default VAPID subject.
func (n *WebPushNotifier) SubjectForTest(s string) {
	n.Subject = s
}
