package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/media/img"
)

// minSecretLen is the smallest accepted HMAC key. 16 bytes (128 bits) is the
// SEI CERT / NIST floor for a keyed-MAC secret.
const minSecretLen = 16

// ErrNoSecret is returned by [New] when Options.Secret is shorter than the
// minimum. An app must configure a real secret; there is no insecure default.
var ErrNoSecret = errors.New("pipeline: signing secret missing or too short (need >=16 bytes)")

// Source is the minimal read side of storage.Bucket the pipeline needs: open an
// object by key for reading. storage.Bucket satisfies it. Kept narrow so the
// pipeline can be driven by any byte source in tests without the full Bucket.
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

// New builds a Pipeline. proc renders resizes (inject the media/img processor;
// on a no-libvips runner it returns img.ErrCGORequired). src fetches source
// bytes by key. clk supplies "now" for expiry checks. Returns [ErrNoSecret]
// when the secret is missing or too short.
func New(opts Options, proc img.Processor, src Source, clk clock.Clock, log *slog.Logger) (*Pipeline, error) {
	opts = opts.withDefaults()
	if len(opts.Secret) < minSecretLen {
		return nil, ErrNoSecret
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
	payload, mac, err := decodeToken(token)
	if err != nil {
		return "", Transform{}, err
	}
	want := sign(p.secret, payload)
	// Constant-time compare to avoid leaking how many leading bytes matched.
	if !hmacEqual(want, mac) {
		return "", Transform{}, ErrBadSignature
	}
	key, t, exp, err := parsePayload(payload)
	if err != nil {
		return "", Transform{}, err
	}
	if !p.clk.Now().Before(exp) {
		return "", Transform{}, ErrExpired
	}
	// Defense in depth: re-validate bounds in case Options tightened since the
	// token was minted.
	if err = p.validate(t); err != nil {
		return "", Transform{}, err
	}
	return key, t, nil
}

// validate enforces the [Options] bounds on a transform: non-negative
// dimensions within MaxWidth/MaxHeight, total pixels within MaxPixels, quality
// in 0..100, and format in the allowlist. Power-of-10 rule 7: validate every
// untrusted parameter at the boundary.
func (p *Pipeline) validate(t Transform) error {
	if t.Width < 0 || t.Height < 0 {
		return fmt.Errorf("pipeline: negative dimension: %w", ErrInvalidParams)
	}
	if t.Width > p.opts.MaxWidth || t.Height > p.opts.MaxHeight {
		return fmt.Errorf("pipeline: dimension exceeds max (%dx%d): %w",
			p.opts.MaxWidth, p.opts.MaxHeight, ErrInvalidParams)
	}
	if t.Width > 0 && t.Height > 0 && t.Width*t.Height > p.opts.MaxPixels {
		return fmt.Errorf("pipeline: pixel budget exceeded (%d): %w", p.opts.MaxPixels, ErrInvalidParams)
	}
	if t.Quality < 0 || t.Quality > 100 {
		return fmt.Errorf("pipeline: quality out of range: %w", ErrInvalidParams)
	}
	if t.Format != "" {
		if _, ok := p.formats[t.Format]; !ok {
			return fmt.Errorf("pipeline: format %q not allowed: %w", t.Format, ErrInvalidParams)
		}
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
// Sign did so). The actual resize delegates to the injected img.Processor; on a
// no-libvips build it surfaces img.ErrCGORequired.
func (p *Pipeline) render(ctx context.Context, key string, t Transform) (rendered, error) {
	rc, err := p.src.Get(ctx, key)
	if err != nil {
		return rendered{}, fmt.Errorf("pipeline: fetch source: %w", err)
	}
	defer func() { _ = rc.Close() }()

	srcBytes, err := io.ReadAll(rc)
	if err != nil {
		return rendered{}, fmt.Errorf("pipeline: read source: %w", err)
	}

	out, format, err := p.transform(ctx, srcBytes, t)
	if err != nil {
		return rendered{}, err
	}
	return rendered{body: out, contentType: contentTypeFor(format)}, nil
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
	}
	return out, format, nil
}
