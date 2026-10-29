// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package in provides HTTP middleware for verifying inbound webhook
// signatures from common providers. Each provider uses a different
// scheme; the middleware reads the raw body, verifies the signature,
// then replaces the request body so downstream handlers can re-read it.
//
// Usage:
//
//	stripe, err := in.Stripe(secret)
//	if err != nil { return err }
//	mux.Handle("/webhooks/stripe", stripe(yourStripeHandler))
//
//	github, err := in.GitHub(secret)
//	if err != nil { return err }
//	mux.Handle("/webhooks/github", github(yourGitHubHandler))
//
// Body is buffered into memory (up to [MaxBodyBytes]) so the HMAC can be
// computed. Larger payloads are rejected with HTTP 413 before verification.
package in

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- SHA-1 required by GitHub webhook spec
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v87"

	"github.com/golusoris/golusoris/core/validate"
)

// MaxBodyBytes is the maximum body size read for signature verification.
const MaxBodyBytes = 1 << 20 // 1 MiB

// ErrInvalidSignature is returned when the signature does not match.
var ErrInvalidSignature = errors.New("webhooks/in: invalid signature")

var errBodyTooLarge = errors.New("webhooks/in: body exceeds 1 MiB limit")

func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("webhooks/in: read body: %w", err)
	}
	if len(body) > MaxBodyBytes {
		return nil, errBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func rejectBody(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, errBodyTooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	http.Error(w, err.Error(), status)
}

func reject(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusUnauthorized)
}

// Stripe returns middleware that verifies the Stripe-Signature header
// using the webhook endpoint secret. Tolerates a 5-minute clock skew.
func Stripe(secret string) (func(http.Handler) http.Handler, error) {
	if err := validateSecret(secret); err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return withDownstream(next, func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(r)
			if err != nil {
				rejectBody(w, err)
				return
			}
			if err := verifyStripe(body, r.Header.Get("Stripe-Signature"), secret); err != nil {
				reject(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func verifyStripe(body []byte, header, secret string) error {
	// Delegate payload validation and past-timestamp check to official Stripe SDK.
	if err := stripe.ValidatePayload(body, header, secret, stripe.WithTolerance(5*time.Minute)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSignature, err)
	}
	return checkStripeFuture(header)
}

// checkStripeFuture rejects timestamps more than 5 minutes in the future.
func checkStripeFuture(header string) error {
	var ts string
	for part := range strings.SplitSeq(header, ",") {
		k, v, ok := strings.Cut(part, "=")
		if ok && k == "t" {
			ts = v
		}
	}
	if ts == "" {
		return ErrInvalidSignature
	}
	t, err := parseUnix(ts)
	if err != nil {
		return fmt.Errorf("%w: timestamp invalid", ErrInvalidSignature)
	}
	if time.Since(t) < -5*time.Minute {
		return fmt.Errorf("%w: timestamp outside replay window", ErrInvalidSignature)
	}
	return nil
}

// GitHub returns middleware that verifies the X-Hub-Signature-256
// header (SHA-256 HMAC) from GitHub webhooks.
func GitHub(secret string) (func(http.Handler) http.Handler, error) {
	if err := validateSecret(secret); err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return withDownstream(next, func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(r)
			if err != nil {
				rejectBody(w, err)
				return
			}
			sig := r.Header.Get("X-Hub-Signature-256")
			if err := verifyGitHub256(body, sig, secret); err != nil {
				reject(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func verifyGitHub256(body []byte, header, secret string) error {
	got, ok := decodeSignature(header, "sha256")
	if !ok {
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
func GitHubLegacy(secret string) (func(http.Handler) http.Handler, error) {
	if err := validateSecret(secret); err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return withDownstream(next, func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(r)
			if err != nil {
				rejectBody(w, err)
				return
			}
			sig := r.Header.Get("X-Hub-Signature")
			got, ok := decodeSignature(sig, "sha1")
			if !ok {
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
	}, nil
}

// Slack verifies the X-Slack-Signature header (v0 HMAC-SHA256).
func Slack(signingSecret string) (func(http.Handler) http.Handler, error) {
	if err := validateSecret(signingSecret); err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return withDownstream(next, func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(r)
			if err != nil {
				rejectBody(w, err)
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
	}, nil
}

func verifySlack(body []byte, ts, sig, secret string) error {
	if ts == "" || sig == "" {
		return ErrInvalidSignature
	}
	t, err := parseUnix(ts)
	if err != nil {
		return fmt.Errorf("%w: timestamp invalid", ErrInvalidSignature)
	}
	age := time.Since(t)
	if age < -5*time.Minute || age > 5*time.Minute {
		return fmt.Errorf("%w: timestamp outside replay window", ErrInvalidSignature)
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
func HMAC(secret, header string) (func(http.Handler) http.Handler, error) {
	if err := validateSecret(secret); err != nil {
		return nil, err
	}
	if strings.TrimSpace(header) == "" {
		return nil, errors.New("webhooks/in: signature header must not be empty")
	}
	return func(next http.Handler) http.Handler {
		return withDownstream(next, func(w http.ResponseWriter, r *http.Request) {
			body, err := readBody(r)
			if err != nil {
				rejectBody(w, err)
				return
			}
			h := r.Header.Get(header)
			got, ok := decodeSignature(h, "sha256")
			if !ok {
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
	}, nil
}

// --- helpers ---

func withDownstream(next http.Handler, handler http.HandlerFunc) http.Handler {
	if validate.IsNil(next) {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "downstream handler unavailable", http.StatusInternalServerError)
		})
	}
	return handler
}

func hmacRaw(newHash func() hash.Hash, key, data []byte) []byte {
	h := hmac.New(newHash, key)
	h.Write(data)
	return h.Sum(nil)
}

func hmacSHA256(key, data []byte) string {
	return hex.EncodeToString(hmacRaw(sha256.New, key, data))
}

func decodeSignature(header, algorithm string) ([]byte, bool) {
	prefix, digest, ok := strings.Cut(header, "=")
	if !ok || prefix != algorithm {
		return nil, false
	}
	decoded, err := hex.DecodeString(digest)
	return decoded, err == nil
}

func validateSecret(secret string) error {
	if secret == "" {
		return errors.New("webhooks/in: signing secret must not be empty")
	}
	return nil
}

func parseUnix(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("invalid unix timestamp")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, errors.New("invalid unix timestamp")
	}
	return time.Unix(n, 0), nil
}
