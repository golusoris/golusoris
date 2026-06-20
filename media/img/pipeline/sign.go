// Package pipeline provides on-demand image resize + signed-URL serving on top
// of the media/img govips transforms.
//
// An app mounts [Handler] behind a route like "/img/{signed}". The signed token
// carries the source image key plus the requested transform (width, height,
// quality, format) and an expiry, authenticated by an HMAC-SHA256 over a stable
// canonical string. Verification is constant-time and rejects tampered, expired,
// or wrong-secret tokens, so the resize endpoint is not an open proxy
// (SSRF / decompression-bomb DoS guard). Output dimensions are bounded by
// [Options] before any decode.
//
// The signing, validation, and handler-routing logic is CGO-independent and
// lives in non-CGO files; only the actual resize delegates to media/img
// (libvips via govips, CGO). On a runner without libvips the resize path returns
// [img.ErrCGORequired] while signing/validation/routing still build and test.
//
// Usage:
//
//	p := pipeline.New(opts, processor, bucket, clk, logger)
//	tok, err := p.Sign("avatars/u42.png", pipeline.Transform{Width: 256, Format: "webp"}, 5*time.Minute)
//	mux.Handle("/img/{signed}", p.Handler())
package pipeline

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors. Callers match with errors.Is.
var (
	// ErrBadToken means the token is malformed (wrong field count, bad base64,
	// unparseable integers).
	ErrBadToken = errors.New("pipeline: malformed token")
	// ErrBadSignature means the HMAC did not verify (tampered or wrong secret).
	ErrBadSignature = errors.New("pipeline: signature mismatch")
	// ErrExpired means the token's expiry is in the past.
	ErrExpired = errors.New("pipeline: token expired")
	// ErrInvalidParams means the transform violates [Options] bounds (oversize
	// dimensions, disallowed format, bad quality).
	ErrInvalidParams = errors.New("pipeline: invalid transform params")
)

// Transform is the requested variant: a resize box plus output encoding. A zero
// Width or Height means "unbounded on that axis" (aspect ratio is preserved by
// the resize step). Format empty means "keep source format".
type Transform struct {
	Width   int    // target max width in pixels; 0 = unbounded
	Height  int    // target max height in pixels; 0 = unbounded
	Quality int    // lossy quality 1..100; 0 = processor default
	Format  string // output format: jpeg|png|webp|avif|gif|tiff; "" = source
}

// canonical returns the stable, signing-input representation of a key+transform
// +expiry. Field order and formatting are fixed so the same inputs always hash
// to the same bytes across processes. The key is URL-escaped so the field
// separator can never appear inside it.
func canonical(key string, t Transform, exp time.Time) string {
	var b strings.Builder
	b.WriteString(url.QueryEscape(key))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(t.Width))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(t.Height))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(t.Quality))
	b.WriteByte('|')
	b.WriteString(t.Format)
	b.WriteByte('|')
	b.WriteString(strconv.FormatInt(exp.Unix(), 10))
	return b.String()
}

// sign computes the raw HMAC-SHA256 of the canonical payload under secret.
func sign(secret []byte, payload string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// encodeToken builds the URL-safe token: base64url(payload) + "." + base64url(mac).
// The payload is encoded so the handler can recover key/transform/expiry without
// a side channel, and the trailing mac authenticates all of it.
func encodeToken(payload string, mac []byte) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString(mac)
}

// decodeToken splits a token into its payload string and raw mac bytes.
func decodeToken(token string) (payload string, mac []byte, err error) {
	enc := base64.RawURLEncoding
	dot := strings.IndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return "", nil, ErrBadToken
	}
	pb, err := enc.DecodeString(token[:dot])
	if err != nil {
		return "", nil, fmt.Errorf("pipeline: decode payload: %w", ErrBadToken)
	}
	mac, err = enc.DecodeString(token[dot+1:])
	if err != nil {
		return "", nil, fmt.Errorf("pipeline: decode mac: %w", ErrBadToken)
	}
	return string(pb), mac, nil
}

// parsePayload reverses [canonical]. It returns the embedded key, transform, and
// expiry. A field-count or integer-parse error maps to [ErrBadToken].
func parsePayload(payload string) (key string, t Transform, exp time.Time, err error) {
	parts := strings.Split(payload, "|")
	const wantFields = 6
	if len(parts) != wantFields {
		return "", Transform{}, time.Time{}, ErrBadToken
	}
	key, err = url.QueryUnescape(parts[0])
	if err != nil {
		return "", Transform{}, time.Time{}, fmt.Errorf("pipeline: unescape key: %w", ErrBadToken)
	}
	width, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", Transform{}, time.Time{}, fmt.Errorf("pipeline: width: %w", ErrBadToken)
	}
	height, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", Transform{}, time.Time{}, fmt.Errorf("pipeline: height: %w", ErrBadToken)
	}
	quality, err := strconv.Atoi(parts[3])
	if err != nil {
		return "", Transform{}, time.Time{}, fmt.Errorf("pipeline: quality: %w", ErrBadToken)
	}
	unix, err := strconv.ParseInt(parts[5], 10, 64)
	if err != nil {
		return "", Transform{}, time.Time{}, fmt.Errorf("pipeline: expiry: %w", ErrBadToken)
	}
	t = Transform{Width: width, Height: height, Quality: quality, Format: parts[4]}
	return key, t, time.Unix(unix, 0), nil
}
