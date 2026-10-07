// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"time"

	"github.com/golusoris/golusoris/core/clock"
	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/media/img"
)

// minSecretLen is the smallest accepted HMAC key. 16 bytes (128 bits) is the
// SEI CERT / NIST floor for a keyed-MAC secret.
const minSecretLen = 16

// ErrNoSecret is returned by [New] when Options.Secret is shorter than the
// minimum. An app must configure a real secret; there is no insecure default.
var ErrNoSecret = errors.New("pipeline: signing secret missing or too short (need >=16 bytes)")

// ErrInvalidDependency is returned when a required runtime dependency is nil.
var ErrInvalidDependency = errors.New("pipeline: required dependency is nil")

var errSourceTooLarge = errors.New("pipeline: source exceeds byte limit")

// Source opens an object by key for reading. Use [SourceFromBucket] to adapt a
// storage.Bucket, whose Get method also returns object metadata. The narrow
// interface keeps custom byte sources small.
type Source interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// Pipeline signs/verifies variant tokens and renders resized variants. It is
// immutable after [New] and safe for concurrent use.
type Pipeline struct {
	opts   Options
	secret []byte
	proc   img.Processor
	src    Source
	clk    clock.Clock
	log    *slog.Logger
	// formats is the allowlist as a set for O(1) membership.
	formats map[string]struct{}
}

// New builds a Pipeline. proc is an application-provided image backend. src
// fetches source bytes by key. clk supplies "now" for expiry checks. Returns
// [ErrNoSecret] when the secret is missing or too short.
func New(opts Options, proc img.Processor, src Source, clk clock.Clock, log *slog.Logger) (*Pipeline, error) {
	opts = opts.withDefaults()
	if len(opts.Secret) < minSecretLen {
		return nil, ErrNoSecret
	}
	if validate.IsNil(proc) {
		return nil, fmt.Errorf("pipeline: processor: %w", ErrInvalidDependency)
	}
	if validate.IsNil(src) {
		return nil, fmt.Errorf("pipeline: source: %w", ErrInvalidDependency)
	}
	if validate.IsNil(clk) {
		return nil, fmt.Errorf("pipeline: clock: %w", ErrInvalidDependency)
	}
	if log == nil {
		return nil, fmt.Errorf("pipeline: logger: %w", ErrInvalidDependency)
	}
	formats := make(map[string]struct{}, len(opts.AllowedFormats))
	for _, f := range opts.AllowedFormats {
		formats[f] = struct{}{}
	}
	return &Pipeline{
		opts:    opts,
		secret:  []byte(opts.Secret),
		proc:    proc,
		src:     src,
		clk:     clk,
		log:     log,
		formats: formats,
	}, nil
}

// Sign returns a URL-safe token authenticating key+transform with an expiry of
// now+ttl. ttl<=0 uses Options.DefaultTTL. The transform is validated against
// the configured bounds first so a caller cannot mint a token the handler will
// later reject.
func (p *Pipeline) Sign(key string, t Transform, ttl time.Duration) (string, error) {
	if key == "" {
		return "", fmt.Errorf("pipeline: sign: %w", ErrInvalidParams)
	}
	if err := p.validate(t); err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = p.opts.DefaultTTL
	}
	exp := p.clk.Now().Add(ttl)
	payload := canonical(key, t, exp)
	mac := sign(p.secret, payload)
	return encodeToken(payload, mac), nil
}

// Verify authenticates a token and returns the embedded key+transform. It
// rejects malformed tokens ([ErrBadToken]), bad signatures ([ErrBadSignature]),
// expired tokens ([ErrExpired]), and transforms outside bounds
// ([ErrInvalidParams]). Signature comparison is constant-time.
func (p *Pipeline) Verify(token string) (key string, t Transform, err error) {
	key, t, _, err = p.verify(token)
	return key, t, err
}

// verify retains the authenticated expiry for internal callers that must bind
// response caching to the token lifetime.
func (p *Pipeline) verify(token string) (key string, t Transform, exp time.Time, err error) {
	payload, mac, err := decodeToken(token)
	if err != nil {
		return "", Transform{}, time.Time{}, err
	}
	want := sign(p.secret, payload)
	// Constant-time compare to avoid leaking how many leading bytes matched.
	if !hmacEqual(want, mac) {
		return "", Transform{}, time.Time{}, ErrBadSignature
	}
	key, t, exp, err = parsePayload(payload)
	if err != nil {
		return "", Transform{}, time.Time{}, err
	}
	if !p.clk.Now().Before(exp) {
		return "", Transform{}, time.Time{}, ErrExpired
	}
	// Defense in depth: re-validate bounds in case Options tightened since the
	// token was minted.
	if err = p.validate(t); err != nil {
		return "", Transform{}, time.Time{}, err
	}
	return key, t, exp, nil
}

// validate enforces the [Options] bounds on a transform: non-negative
// dimensions within MaxWidth/MaxHeight, total pixels within MaxPixels, quality
// in 0..100, and format in the allowlist. Power-of-10 rule 7: validate every
// untrusted parameter at the boundary.
func (p *Pipeline) validate(t Transform) error {
	if err := p.validateDimensions(t); err != nil {
		return err
	}
	if t.Quality < 0 || t.Quality > 100 {
		return fmt.Errorf("pipeline: quality out of range: %w", ErrInvalidParams)
	}
	if t.Format == "" {
		return nil
	}
	if _, ok := p.formats[t.Format]; !ok {
		return fmt.Errorf("pipeline: format %q not allowed: %w", t.Format, ErrInvalidParams)
	}
	return nil
}

func (p *Pipeline) validateDimensions(t Transform) error {
	if t.Width < 0 || t.Height < 0 {
		return fmt.Errorf("pipeline: negative dimension: %w", ErrInvalidParams)
	}
	if t.Width > p.opts.MaxWidth || t.Height > p.opts.MaxHeight {
		return fmt.Errorf("pipeline: dimension exceeds max (%dx%d): %w",
			p.opts.MaxWidth, p.opts.MaxHeight, ErrInvalidParams)
	}
	effectiveWidth, effectiveHeight := t.Width, t.Height
	if effectiveWidth == 0 {
		effectiveWidth = p.opts.MaxWidth
	}
	if effectiveHeight == 0 {
		effectiveHeight = p.opts.MaxHeight
	}
	if effectiveWidth > p.opts.MaxPixels/effectiveHeight {
		return fmt.Errorf("pipeline: pixel budget exceeded (%d): %w", p.opts.MaxPixels, ErrInvalidParams)
	}
	return nil
}

// rendered is the output of Render: the encoded variant bytes plus the wire
// Content-Type the handler should set.
type rendered struct {
	body        []byte
	contentType string
}

// render fetches the source by key, resizes/re-encodes it per t, and returns the
// variant bytes + Content-Type. The transform is assumed validated (Verify or
// Sign did so). The actual resize delegates to the injected img.Processor.
func (p *Pipeline) render(ctx context.Context, key string, t Transform) (res rendered, err error) {
	rc, err := p.src.Get(ctx, key)
	if err != nil {
		return rendered{}, fmt.Errorf("pipeline: fetch source: %w", err)
	}
	defer gerr.CloseInto(rc, &err, "pipeline: close source "+key)

	srcBytes, err := readSource(ctx, rc, p.opts.MaxSourceBytes)
	if err != nil {
		return rendered{}, fmt.Errorf("pipeline: read source: %w", err)
	}

	out, format, err := p.transform(ctx, srcBytes, t)
	if err != nil {
		return rendered{}, err
	}
	return rendered{body: out, contentType: contentTypeFor(format)}, nil
}

type contextReader struct {
	check func() error
	src   io.Reader
}

func (r contextReader) Read(dst []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, fmt.Errorf("pipeline: source context before read: %w", err)
	}
	n, err := r.src.Read(dst)
	if ctxErr := r.check(); ctxErr != nil {
		return n, fmt.Errorf("pipeline: source context after read: %w", ctxErr)
	}
	if err == nil {
		return n, nil
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	return n, fmt.Errorf("pipeline: source read: %w", err)
}

func readSource(ctx context.Context, r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return nil, fmt.Errorf("pipeline: invalid source byte limit %d", maxBytes)
	}
	reader := contextReader{check: ctx.Err, src: r}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("pipeline: read bounded source: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, errSourceTooLarge
	}
	return data, nil
}

// transform applies the resize and/or format conversion via the processor and
// returns the encoded bytes plus the effective output format. Width/Height of 0
// expand to the configured max on that axis so the processor's fit-inside math
// has a finite box while preserving the unbounded-axis intent.
func (p *Pipeline) transform(ctx context.Context, src []byte, t Transform) ([]byte, img.Format, error) {
	width, height := t.Width, t.Height
	if width == 0 {
		width = p.opts.MaxWidth
	}
	if height == 0 {
		height = p.opts.MaxHeight
	}

	format := img.Format(t.Format)
	resizeOpts := img.ResizeOptions{Fit: "contain", Quality: t.Quality, StripMetadata: true}
	out, err := p.proc.Resize(ctx, src, width, height, resizeOpts)
	if err != nil {
		return nil, "", fmt.Errorf("pipeline: resize: %w", err)
	}

	// Re-encode only when a format different from the source was requested.
	if t.Format != "" {
		out, err = p.proc.Convert(ctx, out, format, t.Quality)
		if err != nil {
			return nil, "", fmt.Errorf("pipeline: convert: %w", err)
		}
	} else {
		_, _, format, err = p.proc.Info(ctx, out)
		if err != nil {
			return nil, "", fmt.Errorf("pipeline: inspect resized output: %w", err)
		}
	}
	return out, format, nil
}
