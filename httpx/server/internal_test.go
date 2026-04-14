package server

import (
	"testing"
	"time"

	"github.com/golusoris/golusoris/config"
)

func TestWithDefaults_zeroFilled(t *testing.T) {
	t.Parallel()
	got := Options{}.withDefaults()
	d := DefaultOptions()
	if got.Addr != d.Addr {
		t.Errorf("Addr = %q, want %q", got.Addr, d.Addr)
	}
	if got.Timeouts.Read != d.Timeouts.Read {
		t.Errorf("Timeouts.Read = %v, want %v", got.Timeouts.Read, d.Timeouts.Read)
	}
	if got.Timeouts.Header != d.Timeouts.Header {
		t.Errorf("Timeouts.Header = %v, want %v", got.Timeouts.Header, d.Timeouts.Header)
	}
	if got.Timeouts.Write != d.Timeouts.Write {
		t.Errorf("Timeouts.Write = %v, want %v", got.Timeouts.Write, d.Timeouts.Write)
	}
	if got.Timeouts.Idle != d.Timeouts.Idle {
		t.Errorf("Timeouts.Idle = %v, want %v", got.Timeouts.Idle, d.Timeouts.Idle)
	}
	if got.Timeouts.Shutdown != d.Timeouts.Shutdown {
		t.Errorf("Timeouts.Shutdown = %v, want %v", got.Timeouts.Shutdown, d.Timeouts.Shutdown)
	}
	if got.Limits.Header != d.Limits.Header {
		t.Errorf("Limits.Header = %d, want %d", got.Limits.Header, d.Limits.Header)
	}
}

func TestWithDefaults_preservesNonZero(t *testing.T) {
	t.Parallel()
	in := Options{
		Addr: ":9090",
		Timeouts: TimeoutOptions{
			Read:     10 * time.Second,
			Header:   2 * time.Second,
			Write:    20 * time.Second,
			Idle:     60 * time.Second,
			Shutdown: 5 * time.Second,
		},
		Limits: LimitOptions{Header: 512 * 1024, Body: 1 << 20},
	}
	got := in.withDefaults()
	if got.Addr != ":9090" {
		t.Errorf("Addr = %q, want :9090", got.Addr)
	}
	if got.Timeouts.Read != 10*time.Second {
		t.Errorf("Timeouts.Read = %v, want 10s", got.Timeouts.Read)
	}
	if got.Limits.Header != 512*1024 {
		t.Errorf("Limits.Header = %d, want 524288", got.Limits.Header)
	}
}

func TestWithDefaults_bodyZeroNotOverridden(t *testing.T) {
	t.Parallel()
	// Body == 0 means "disabled" and must not be filled by withDefaults.
	got := Options{}.withDefaults()
	if got.Limits.Body != 0 {
		t.Errorf("Limits.Body = %d, want 0 (disabled is not overridden)", got.Limits.Body)
	}
}

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", opts.Addr)
	}
	if opts.Timeouts.Read != 30*time.Second {
		t.Errorf("Timeouts.Read = %v, want 30s", opts.Timeouts.Read)
	}
}
