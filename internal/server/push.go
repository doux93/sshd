package server

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"sshx/internal/push"
	"sshx/internal/store"
)

// pushDeps groups everything push-related the server needs to wire
// routes. nil for the whole struct = no Web Push support (local mode).
type PushDeps struct {
	Notifier *push.WebPushNotifier
	Store    *store.Store
}

// mountPushRoutes registers the Web Push HTTP handlers. Idempotent:
// if no deps were wired (local mode) the methods 404.
func (s *Server) mountPushRoutes(r chi.Router) {
	if s.push == nil {
		return
	}
	r.Route("/api/push", func(r chi.Router) {
		// public-key needs no auth so the SPA can request it before login.
		r.Get("/public-key", s.handlePushPublicKey)
		// The rest require an authenticated session.
		r.With(s.authn.Middleware).Post("/subscribe", s.handlePushSubscribe)
		r.With(s.authn.Middleware).Get("/subscriptions", s.handlePushList)
		r.With(s.authn.Middleware).Delete("/subscriptions/{id}", s.handlePushRevoke)
	})
}

func (s *Server) handlePushPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.push == nil || s.push.Notifier == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("push disabled"))
		return
	}
	pk, err := s.push.Notifier.PublicKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"publicKey": pk,
		"subject":   s.push.Notifier.Subject,
	})
}

// pushSubscribeRequest mirrors the JSON produced by
// PushSubscription.toJSON() in the SPA. We accept any endpoint shape \u2014
// we don't whitelist push services; FCM/Mozilla/Apple all speak RFC 8030.
type pushSubscribeRequest struct {
	Endpoint  string `json:"endpoint"`
	P256DH    string `json:"p256dh"`
	Auth      string `json:"auth"`
	UserAgent string `json:"userAgent,omitempty"`
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("push disabled"))
		return
	}
	var req pushSubscribeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("malformed json"))
		return
	}
	if req.Endpoint == "" || req.P256DH == "" || req.Auth == "" {
		writeError(w, http.StatusBadRequest, errors.New("endpoint, p256dh, auth required"))
		return
	}
	// Reuse an existing subscription if the same endpoint is already
	// registered (browser re-subscribe on permission re-grant). Update
	// the key/auth if they changed, otherwise we'd be sending to dead
	// credentials.
	subs, err := s.push.Store.ListActivePushSubscriptions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, existing := range subs {
		if existing.Endpoint == req.Endpoint {
			existing.P256DH = req.P256DH
			existing.Auth = req.Auth
			if req.UserAgent != "" {
				existing.UserAgent = req.UserAgent
			} else {
				existing.UserAgent = r.UserAgent()
			}
			if err := s.push.Store.UpdatePushSubscription(&existing); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"id": existing.ID})
			return
		}
	}
	id, err := store.NewPushSubscriptionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ua := req.UserAgent
	if ua == "" {
		ua = r.UserAgent()
	}
	sub := &store.PushSubscription{
		ID:        id,
		Endpoint:  req.Endpoint,
		P256DH:    req.P256DH,
		Auth:      req.Auth,
		UserAgent: ua,
	}
	if err := s.push.Store.CreatePushSubscription(sub); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// pushSubscriptionView is the row shape the SPA renders in the device
// list. We strip the keys — they're write-only.
type pushSubscriptionView struct {
	ID         string `json:"id"`
	Endpoint   string `json:"endpoint"`
	UserAgent  string `json:"userAgent"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
	Revoked    bool   `json:"revoked"`
}

func (s *Server) handlePushList(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("push disabled"))
		return
	}
	subs, err := s.push.Store.ListAllPushSubscriptions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]pushSubscriptionView, 0, len(subs))
	for _, sub := range subs {
		v := pushSubscriptionView{
			ID:         sub.ID,
			Endpoint:   sub.Endpoint,
			UserAgent:  sub.UserAgent,
			CreatedAt:  sub.CreatedAt.UnixMilli(),
			LastUsedAt: sub.LastUsedAt.UnixMilli(),
			Revoked:    sub.Revoked,
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePushRevoke(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("push disabled"))
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing id"))
		return
	}
	if err := s.push.Store.RevokePushSubscription(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// attachVAPIDFromStore wires loadVAPID/saveVAPID closures against the
// real store. Called from main.go when constructing the notifier.
func AttachVAPIDFromStore(s *store.Store) (
	load func() (*ecdsa.PrivateKey, []byte, error),
	save func([]byte, []byte) error,
) {
	load = func() (*ecdsa.PrivateKey, []byte, error) {
		cfg, err := s.LoadAuthConfig()
		if err != nil {
			return nil, nil, err
		}
		if len(cfg.VAPIDPrivate) == 0 || len(cfg.VAPIDPublic) == 0 {
			return nil, nil, nil // not generated yet
		}
		priv, err := push.DecodeVAPIDPrivate(cfg.VAPIDPrivate)
		if err != nil {
			return nil, nil, err
		}
		return priv, cfg.VAPIDPublic, nil
	}
	save = func(priv, pub []byte) error {
		return s.UpdateVAPIDKeys(priv, pub)
	}
	return load, save
}
