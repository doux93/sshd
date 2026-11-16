package auth

import (
	"net/http"
	"net/http/httptest"
	"sshx/internal/store"
	"testing"
	"time"
)

func TestReview_ExpiredSessionMustBeRejected(t *testing.T) {
	p := newTestPassword(t)
	now := time.Now()
	if err := p.st.CreateSession(&store.Session{ID: "review-expired", CreatedAt: now.Add(-40 * 24 * time.Hour), ExpiresAt: now.Add(-time.Hour), LastSeenAt: now.Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Auth-Token", "review-expired")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Errorf("expired token accepted: status=%d, want 401", rr.Code)
	}
}
