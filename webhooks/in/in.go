// Package in provides HTTP middleware for verifying inbound webhook
// signatures from common providers. Each provider uses a different
// scheme; the middleware reads the raw body, verifies the signature,
// then replaces the request body so downstream handlers can re-read it.
//
// Usage:
//
//	mux.Handle("/webhooks/stripe",
//	    in.Stripe(secret)(yourStripeHandler))
//
//	mux.Handle("/webhooks/github",
//	    in.GitHub(secret)(yourGitHubHandler))
//
// Body is buffered into memory (up to [MaxBodyBytes]) so the HMAC can
// be computed. Reject oversized payloads before mounting these handlers
// using httpx/middleware body-limit.
package in

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // SHA-1 required by GitHub webhook spec // #nosec G505
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxBodyBytes is the maximum body size read for signature verification.
const MaxBodyBytes = 1 << 20 // 1 MiB

// ErrInvalidSignature is returned when the signature does not match.
var ErrInvalidSignature = errors.New("webhooks/in: invalid signature")

func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("webhooks/in: read body: %w", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func reject(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusUnauthorized)
}

// Stripe returns middleware that verifies the Stripe-Signature header
// using the webhook endpoint secret. Tolerates a 5-minute clock skew.
func Stripe(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := verifyStripe(body, r.Header.Get("Stripe-Signature"), secret); err != nil {
				reject(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func verifyStripe(body []byte, header, secret string) error {
	// Stripe-Signature: t=<timestamp>,v1=<hmac1>[,v1=<hmac2>]
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == "" || len(sigs) == 0 {
		return ErrInvalidSignature
	}
	// Reject timestamps older than 5 minutes.
	if t, err := parseUnix(ts); err != nil || time.Since(t) > 5*time.Minute {
		return fmt.Errorf("%w: timestamp too old or invalid", ErrInvalidSignature)
	}
	payload := ts + "." + string(body)
	mac := hmacSHA256([]byte(secret), []byte(payload))
	for _, sig := range sigs {
		if hmac.Equal([]byte(mac), []byte(sig)) {
			return nil
		}
	}
	return ErrInvalidSignature
}

// GitHub returns middleware that verifies the X-Hub-Signature-256
// header (SHA-256 HMAC) from GitHub webhooks.
func GitHub(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sig := r.Header.Get("X-Hub-Signature-256")
			if err := verifyGitHub256(body, sig, secret); err != nil {
				reject(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func verifyGitHub256(body []byte, header, secret string) error {
	_, digest, ok := strings.Cut(header, "=")
	if !ok {
		return ErrInvalidSignature
	}
	got, err := hex.DecodeString(digest)
	if err != nil {
		return ErrInvalidSignature
	}
	want := hmacRaw(sha256.New, []byte(secret), body)
	if !hmac.Equal(got, want) {
		return ErrInvalidSignature
	}
	return nil
}

// GitHubLegacy verifies the older X-Hub-Signature (SHA-1) header.
// Prefer [GitHub] (SHA-256) for new integrations.
func GitHubLegacy(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sig := r.Header.Get("X-Hub-Signature")
			_, digest, ok := strings.Cut(sig, "=")
			if !ok {
				reject(w, ErrInvalidSignature)
				return
			}
			got, decErr := hex.DecodeString(digest)
			if decErr != nil {
				reject(w, ErrInvalidSignature)
				return
			}
			want := hmacRaw(sha1.New, []byte(secret), body)
			if !hmac.Equal(got, want) {
				reject(w, ErrInvalidSignature)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Slack verifies the X-Slack-Signature header (v0 HMAC-SHA256).
func Slack(signingSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ts := r.Header.Get("X-Slack-Request-Timestamp")
			sig := r.Header.Get("X-Slack-Signature")
			if err := verifySlack(body, ts, sig, signingSecret); err != nil {
				reject(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func verifySlack(body []byte, ts, sig, secret string) error {
	if ts == "" || sig == "" {
		return ErrInvalidSignature
	}
	if t, err := parseUnix(ts); err != nil || time.Since(t) > 5*time.Minute {
		return fmt.Errorf("%w: timestamp too old", ErrInvalidSignature)
	}
	base := "v0:" + ts + ":" + string(body)
	want := "v0=" + hmacSHA256([]byte(secret), []byte(base))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return ErrInvalidSignature
	}
	return nil
}

// HMAC returns a generic HMAC-SHA256 middleware. header is the request
// header name that carries "sha256=<hex>".
func HMAC(secret, header string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			h := r.Header.Get(header)
			_, digest, ok := strings.Cut(h, "=")
			if !ok {
				reject(w, ErrInvalidSignature)
				return
			}
			got, decErr := hex.DecodeString(digest)
			if decErr != nil {
				reject(w, ErrInvalidSignature)
				return
			}
			want := hmacRaw(sha256.New, []byte(secret), body)
			if !hmac.Equal(got, want) {
				reject(w, ErrInvalidSignature)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- helpers ---

func hmacRaw(newHash func() hash.Hash, key, data []byte) []byte {
	h := hmac.New(newHash, key)
	h.Write(data)
	return h.Sum(nil)
}

func hmacSHA256(key, data []byte) string {
	return hex.EncodeToString(hmacRaw(sha256.New, key, data))
}

func parseUnix(s string) (time.Time, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return time.Time{}, errors.New("invalid unix timestamp")
		}
		n = n*10 + int64(c-'0')
	}
	return time.Unix(n, 0), nil
}
