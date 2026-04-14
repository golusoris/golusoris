// Package unsub implements RFC 8058 one-click unsubscribe and a
// per-email suppression list.
//
// Apps generate a signed unsubscribe URL, include it in the
// List-Unsubscribe header, and mount the handler at the same path.
// On click the email is added to the suppression list; the Notifier
// checks suppression before sending.
//
// Usage:
//
//	svc := unsub.New(store, []byte(secret))
//
//	// When building an email:
//	url := svc.URL("https://app.example.com/unsubscribe", "user@example.com")
//	// Set header: List-Unsubscribe: <url>, <mailto:unsubscribe@app.example.com>
//	// Set header: List-Unsubscribe-Post: List-Unsubscribe=One-Click
//
//	// Mount the handler:
//	mux.Handle("/unsubscribe", svc.Handler())
package unsub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Store persists the suppression list.
type Store interface {
	// Add adds email to the suppression list.
	Add(ctx context.Context, email string) error
	// IsSuppressed returns true if email is suppressed.
	IsSuppressed(ctx context.Context, email string) (bool, error)
	// Remove removes email from the suppression list (re-subscribe).
	Remove(ctx context.Context, email string) error
}

// Service generates unsubscribe URLs and handles click-through.
type Service struct {
	store  Store
	secret []byte
}

// New returns a Service. secret must be kept stable — changing it
// invalidates all existing unsubscribe URLs.
func New(store Store, secret []byte) *Service {
	return &Service{store: store, secret: secret}
}

// URL returns a signed one-click unsubscribe URL for email.
// baseURL is the mounted path of [Handler] (e.g.
// "https://app.example.com/unsubscribe").
func (s *Service) URL(baseURL, email string) string {
	sig := s.sign(email)
	return baseURL + "?email=" + url.QueryEscape(email) + "&sig=" + url.QueryEscape(sig)
}

// Handler handles GET (browser click) and POST (RFC 8058 one-click
// from MUA). On valid request the email is suppressed and a 200
// response is returned.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email := r.FormValue("email")
		sig := r.FormValue("sig")
		if email == "" || sig == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if !hmac.Equal([]byte(s.sign(email)), []byte(sig)) {
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}
		if err := s.store.Add(r.Context(), strings.ToLower(email)); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = fmt.Fprintf(w, "You have been unsubscribed from %s.", email)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// IsSuppressed returns true if email is on the suppression list.
// Use before sending to avoid re-emailing unsubscribed users.
func (s *Service) IsSuppressed(ctx context.Context, email string) (bool, error) {
	ok, err := s.store.IsSuppressed(ctx, strings.ToLower(email))
	if err != nil {
		return false, fmt.Errorf("unsub: check: %w", err)
	}
	return ok, nil
}

func (s *Service) sign(email string) string {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(strings.ToLower(email)))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
