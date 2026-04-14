// Package tracking implements email open/click tracking via signed
// redirect URLs and a 1×1 tracking pixel.
//
// Apps rewrite links in outbound email bodies through [Service.ClickURL]
// and inject a tracking pixel via [Service.PixelURL]. On load, the
// handler records the event and either serves a 1×1 GIF (open) or 302-s
// to the original URL (click).
//
// Usage:
//
//	svc := tracking.New(store, []byte(secret))
//	mux.Handle("/t/open",  svc.PixelHandler())
//	mux.Handle("/t/click", svc.ClickHandler())
//
//	// While rendering the email:
//	openURL  := svc.PixelURL("https://app.example.com/t/open",  messageID, recipient)
//	clickURL := svc.ClickURL("https://app.example.com/t/click", messageID, recipient, target)
package tracking

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Event is a recorded open/click.
type Event struct {
	MessageID string
	Recipient string
	Kind      Kind
	// URL is populated for Kind=Click.
	URL string
	// UserAgent + IP captured at record time.
	UserAgent string
	IP        string
}

// Kind distinguishes open pixels from click redirects.
type Kind string

const (
	// KindOpen is a pixel load (email opened).
	KindOpen Kind = "open"
	// KindClick is a link click (redirect to original URL).
	KindClick Kind = "click"
)

// Store persists tracking events. Implementations should tolerate
// high-volume writes (opens fire on every mail render).
type Store interface {
	Record(ctx context.Context, ev Event) error
}

// Service generates signed tracking URLs and handles callbacks.
type Service struct {
	store  Store
	secret []byte
}

// New returns a Service. secret must be stable; rotating it
// invalidates outstanding tracking URLs.
func New(store Store, secret []byte) *Service {
	return &Service{store: store, secret: secret}
}

// PixelURL returns a signed URL for the 1×1 open-tracking pixel.
// baseURL is the mounted path of [PixelHandler].
func (s *Service) PixelURL(baseURL, messageID, recipient string) string {
	sig := s.sign(messageID, recipient, "")
	q := url.Values{}
	q.Set("m", messageID)
	q.Set("r", recipient)
	q.Set("sig", sig)
	return baseURL + "?" + q.Encode()
}

// ClickURL returns a signed redirect URL for click tracking. On load,
// the handler records the click and 302-s to target.
func (s *Service) ClickURL(baseURL, messageID, recipient, target string) string {
	sig := s.sign(messageID, recipient, target)
	q := url.Values{}
	q.Set("m", messageID)
	q.Set("r", recipient)
	q.Set("u", target)
	q.Set("sig", sig)
	return baseURL + "?" + q.Encode()
}

// PixelHandler serves a 1×1 transparent GIF after recording an open.
// On signature mismatch it still serves the pixel (to avoid broken
// rendering) but skips the record.
func (s *Service) PixelHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := r.URL.Query().Get("m")
		recipient := r.URL.Query().Get("r")
		sig := r.URL.Query().Get("sig")
		if m != "" && hmac.Equal([]byte(s.sign(m, recipient, "")), []byte(sig)) {
			_ = s.store.Record(r.Context(), Event{
				MessageID: m,
				Recipient: recipient,
				Kind:      KindOpen,
				UserAgent: r.UserAgent(),
				IP:        clientIP(r),
			})
		}
		w.Header().Set("Content-Type", "image/gif")
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(pixelGIF)
	})
}

// ClickHandler records a click and redirects to the target URL. On
// invalid signature it returns 403.
func (s *Service) ClickHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := r.URL.Query().Get("m")
		recipient := r.URL.Query().Get("r")
		target := r.URL.Query().Get("u")
		sig := r.URL.Query().Get("sig")
		if m == "" || target == "" || sig == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if !hmac.Equal([]byte(s.sign(m, recipient, target)), []byte(sig)) {
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}
		if err := validateRedirectURL(target); err != nil {
			http.Error(w, "invalid target", http.StatusBadRequest)
			return
		}
		_ = s.store.Record(r.Context(), Event{
			MessageID: m,
			Recipient: recipient,
			Kind:      KindClick,
			URL:       target,
			UserAgent: r.UserAgent(),
			IP:        clientIP(r),
		})
		http.Redirect(w, r, target, http.StatusFound)
	})
}

func (s *Service) sign(messageID, recipient, target string) string {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(messageID))
	h.Write([]byte{0})
	h.Write([]byte(recipient))
	h.Write([]byte{0})
	h.Write([]byte(target))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// validateRedirectURL guards against open-redirect abuse by requiring
// an absolute http(s) URL.
func validateRedirectURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("tracking: parse target: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("tracking: target must be http(s)")
	}
	if u.Host == "" {
		return errors.New("tracking: target missing host")
	}
	return nil
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First value in X-Forwarded-For is the original client.
		for i := range len(xff) {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	return r.RemoteAddr
}

// pixelGIF is a 43-byte 1×1 transparent GIF.
var pixelGIF = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00,
	0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0x21,
	0xf9, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x2c, 0x00, 0x00,
	0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x44,
	0x01, 0x00, 0x3b,
}
