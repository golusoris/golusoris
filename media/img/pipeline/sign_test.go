package pipeline_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/media/img/pipeline"
)

const testSecret = "test-secret-0123456789abcdef" // >=16 bytes

func newTestPipeline(t *testing.T, opts pipeline.Options) (*pipeline.Pipeline, *clockwork.FakeClock) {
	t.Helper()
	if opts.Secret == "" {
		opts.Secret = testSecret
	}
	fc := clockwork.NewFakeClock()
	p, err := pipeline.New(opts, stubProcessor{}, emptySource{}, fc, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, fc
}

func TestNew_rejectsShortSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		secret string
		wantOK bool
	}{
		{"empty", "", false},
		{"too short", "short", false},
		{"exactly 16", "0123456789abcdef", true},
		{"long", testSecret, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pipeline.New(
				pipeline.Options{Secret: tc.secret},
				stubProcessor{}, emptySource{}, clockwork.NewFakeClock(), slog.New(slog.DiscardHandler),
			)
			if tc.wantOK && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.wantOK && !errors.Is(err, pipeline.ErrNoSecret) {
				t.Fatalf("want ErrNoSecret, got %v", err)
			}
		})
	}
}

func TestSignVerify_roundTrip(t *testing.T) {
	t.Parallel()
	p, _ := newTestPipeline(t, pipeline.Options{})
	want := pipeline.Transform{Width: 256, Height: 128, Quality: 80, Format: "webp"}

	tok, err := p.Sign("avatars/u42.png", want, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	key, got, err := p.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if key != "avatars/u42.png" {
		t.Errorf("key = %q, want avatars/u42.png", key)
	}
	if got != want {
		t.Errorf("transform = %+v, want %+v", got, want)
	}
}

func TestVerify_tampered(t *testing.T) {
	t.Parallel()
	p, _ := newTestPipeline(t, pipeline.Options{})
	tok, err := p.Sign("k", pipeline.Transform{Width: 100}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Flip the byte just before the dot to tamper the payload.
	b := []byte(tok)
	for i := range b {
		if b[i] == '.' {
			b[i-1] ^= 0x01
			break
		}
	}
	if _, _, err := p.Verify(string(b)); !errors.Is(err, pipeline.ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestVerify_wrongSecret(t *testing.T) {
	t.Parallel()
	signer, _ := newTestPipeline(t, pipeline.Options{Secret: testSecret})
	verifier, _ := newTestPipeline(t, pipeline.Options{Secret: "different-secret-abcdef0123456789"})

	tok, err := signer.Sign("k", pipeline.Transform{Width: 100}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, _, err := verifier.Verify(tok); !errors.Is(err, pipeline.ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestVerify_expired(t *testing.T) {
	t.Parallel()
	p, fc := newTestPipeline(t, pipeline.Options{})
	tok, err := p.Sign("k", pipeline.Transform{Width: 100}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	fc.Advance(2 * time.Minute) // past expiry
	if _, _, err := p.Verify(tok); !errors.Is(err, pipeline.ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestVerify_malformed(t *testing.T) {
	t.Parallel()
	p, _ := newTestPipeline(t, pipeline.Options{})
	tests := []string{
		"",
		"no-dot-here",
		".",
		"onlyleft.",
		".onlyright",
		"!!!notbase64!!!.also@@@",
	}
	for _, tok := range tests {
		t.Run(tok, func(t *testing.T) {
			t.Parallel()
			_, _, err := p.Verify(tok)
			if err == nil {
				t.Fatalf("want error for %q", tok)
			}
			if !errors.Is(err, pipeline.ErrBadToken) && !errors.Is(err, pipeline.ErrBadSignature) {
				t.Fatalf("want ErrBadToken/ErrBadSignature, got %v", err)
			}
		})
	}
}

func TestSign_validation(t *testing.T) {
	t.Parallel()
	p, _ := newTestPipeline(t, pipeline.Options{MaxWidth: 1000, MaxHeight: 1000, MaxPixels: 500_000})
	tests := []struct {
		name string
		tr   pipeline.Transform
		ok   bool
	}{
		{"ok small", pipeline.Transform{Width: 100, Height: 100, Format: "webp"}, true},
		{"oversize width", pipeline.Transform{Width: 5000}, false},
		{"oversize height", pipeline.Transform{Height: 5000}, false},
		{"pixel bomb", pipeline.Transform{Width: 1000, Height: 1000}, false},
		{"negative", pipeline.Transform{Width: -1}, false},
		{"bad quality high", pipeline.Transform{Width: 10, Quality: 101}, false},
		{"bad quality neg", pipeline.Transform{Width: 10, Quality: -5}, false},
		{"disallowed format", pipeline.Transform{Width: 10, Format: "bmp"}, false},
		{"allowed format", pipeline.Transform{Width: 10, Format: "png"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := p.Sign("k", tc.tr, time.Minute)
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok && !errors.Is(err, pipeline.ErrInvalidParams) {
				t.Fatalf("want ErrInvalidParams, got %v", err)
			}
		})
	}
}

func TestSign_emptyKey(t *testing.T) {
	t.Parallel()
	p, _ := newTestPipeline(t, pipeline.Options{})
	if _, err := p.Sign("", pipeline.Transform{Width: 10}, time.Minute); !errors.Is(err, pipeline.ErrInvalidParams) {
		t.Fatalf("want ErrInvalidParams, got %v", err)
	}
}

// --- shared test doubles ---

type emptySource struct{}

func (emptySource) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, errNotImpl
}

var errNotImpl = errors.New("not implemented")
