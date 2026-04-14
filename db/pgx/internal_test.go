package pgx

import (
	"testing"
	"time"

	"github.com/golusoris/golusoris/config"
)

func TestWithDefaults_zeroFilled(t *testing.T) {
	t.Parallel()
	got := Options{}.withDefaults()
	d := DefaultOptions()
	if got.Pool.Max != d.Pool.Max {
		t.Errorf("Pool.Max = %d, want %d", got.Pool.Max, d.Pool.Max)
	}
	if got.Pool.Lifetime != d.Pool.Lifetime {
		t.Errorf("Pool.Lifetime = %v, want %v", got.Pool.Lifetime, d.Pool.Lifetime)
	}
	if got.Pool.Idle != d.Pool.Idle {
		t.Errorf("Pool.Idle = %v, want %v", got.Pool.Idle, d.Pool.Idle)
	}
	if got.Pool.Healthcheck != d.Pool.Healthcheck {
		t.Errorf("Pool.Healthcheck = %v, want %v", got.Pool.Healthcheck, d.Pool.Healthcheck)
	}
	if got.ConnectTimeout != d.ConnectTimeout {
		t.Errorf("ConnectTimeout = %v, want %v", got.ConnectTimeout, d.ConnectTimeout)
	}
	if got.Retry.Attempts != d.Retry.Attempts {
		t.Errorf("Retry.Attempts = %d, want %d", got.Retry.Attempts, d.Retry.Attempts)
	}
	if got.Retry.Initial != d.Retry.Initial {
		t.Errorf("Retry.Initial = %v, want %v", got.Retry.Initial, d.Retry.Initial)
	}
	if got.Retry.Max != d.Retry.Max {
		t.Errorf("Retry.Max = %v, want %v", got.Retry.Max, d.Retry.Max)
	}
}

func TestWithDefaults_preservesNonZero(t *testing.T) {
	t.Parallel()
	in := Options{
		DSN: "postgres://localhost/test",
		Pool: PoolOptions{
			Max:         5,
			Lifetime:    30 * time.Minute,
			Idle:        10 * time.Minute,
			Healthcheck: 30 * time.Second,
		},
		ConnectTimeout: 2 * time.Second,
		Retry: RetryOptions{
			Attempts: 3,
			Initial:  100 * time.Millisecond,
			Max:      2 * time.Second,
		},
	}
	got := in.withDefaults()
	if got.Pool.Max != 5 {
		t.Errorf("Pool.Max = %d, want 5", got.Pool.Max)
	}
	if got.ConnectTimeout != 2*time.Second {
		t.Errorf("ConnectTimeout = %v, want 2s", got.ConnectTimeout)
	}
	if got.Retry.Attempts != 3 {
		t.Errorf("Retry.Attempts = %d, want 3", got.Retry.Attempts)
	}
}

func TestWithDefaults_slowZeroNotOverridden(t *testing.T) {
	t.Parallel()
	// Tracing.Slow == 0 means "disabled" and must not be filled by withDefaults.
	got := Options{}.withDefaults()
	if got.Tracing.Slow != 0 {
		t.Errorf("Tracing.Slow = %v, want 0 (disabled is not overridden)", got.Tracing.Slow)
	}
}

func TestLoadOptions_missingDSN(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = loadOptions(cfg)
	if err == nil {
		t.Error("expected error for missing DSN, got nil")
	}
}
