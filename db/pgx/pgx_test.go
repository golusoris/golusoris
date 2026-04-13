package pgx_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/log"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	o := dbpgx.DefaultOptions()
	if o.Pool.Max != 10 {
		t.Errorf("Pool.Max = %d, want 10", o.Pool.Max)
	}
	if o.Retry.Attempts != 10 {
		t.Errorf("Retry.Attempts = %d, want 10", o.Retry.Attempts)
	}
	if o.Tracing.Slow != 200*time.Millisecond {
		t.Errorf("Tracing.Slow = %v, want 200ms", o.Tracing.Slow)
	}
	if o.ConnectTimeout != 5*time.Second {
		t.Errorf("ConnectTimeout = %v, want 5s", o.ConnectTimeout)
	}
}

// TestNewRequiresDSN asserts that an empty DSN fails fast without entering
// the retry loop.
func TestNewRequiresDSN(t *testing.T) {
	t.Parallel()
	logger := log.New(log.Options{})
	_, err := dbpgx.New(context.Background(), dbpgx.Options{}, logger, clock.NewFake())
	if err == nil {
		t.Fatal("expected error for empty DSN, got nil")
	}
	if !strings.Contains(err.Error(), "DSN is required") {
		t.Errorf("error %q missing %q", err, "DSN is required")
	}
}

// TestNewExhaustsRetries proves the exponential backoff loop runs the
// configured number of attempts and surfaces the final error. We point pgx
// at a definitely-unreachable port; ConnectTimeout keeps each attempt short.
func TestNewExhaustsRetries(t *testing.T) {
	t.Parallel()
	logger := log.New(log.Options{})
	opts := dbpgx.Options{
		// Port 1 is reserved + unprivileged; nothing listens.
		DSN: "postgres://nobody:nobody@127.0.0.1:1/none?sslmode=disable",
		Retry: dbpgx.RetryOptions{
			Attempts: 3,
			Initial:  1 * time.Millisecond,
			Max:      2 * time.Millisecond,
		},
		ConnectTimeout: 50 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Real clock — delays are sub-millisecond so total overhead is negligible.
	// FakeClock would block on After() until Advance() is called, hanging the test.
	_, err := dbpgx.New(ctx, opts, logger, clockwork.NewRealClock())
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Errorf("error %q missing attempt count", err)
	}
}

// TestLoadOptionsFromConfig verifies koanf-driven config wiring (env var →
// nested struct) and the new time.Duration decode hook end-to-end.
func TestLoadOptionsFromConfig(t *testing.T) {
	t.Setenv("APP_DB_DSN", "postgres://app@db/app")
	t.Setenv("APP_DB_POOL_MAX", "25")
	t.Setenv("APP_DB_TRACING_SLOW", "1s")
	t.Setenv("APP_DB_RETRY_ATTEMPTS", "4")

	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var opts dbpgx.Options
	if err := cfg.Unmarshal("db", &opts); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if opts.DSN != "postgres://app@db/app" {
		t.Errorf("DSN = %q", opts.DSN)
	}
	if opts.Pool.Max != 25 {
		t.Errorf("Pool.Max = %d, want 25", opts.Pool.Max)
	}
	if opts.Tracing.Slow != time.Second {
		t.Errorf("Tracing.Slow = %v, want 1s", opts.Tracing.Slow)
	}
	if opts.Retry.Attempts != 4 {
		t.Errorf("Retry.Attempts = %d, want 4", opts.Retry.Attempts)
	}
}
