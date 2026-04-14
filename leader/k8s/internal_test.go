package k8s

import (
	"testing"
	"time"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if opts.Namespace != "default" {
		t.Fatalf("want Namespace=%q, got %q", "default", opts.Namespace)
	}
	if opts.Lease.Duration != 15*time.Second {
		t.Fatalf("want Lease.Duration=15s, got %v", opts.Lease.Duration)
	}
	if opts.Lease.Renew != 10*time.Second {
		t.Fatalf("want Lease.Renew=10s, got %v", opts.Lease.Renew)
	}
	if opts.Lease.Retry != 2*time.Second {
		t.Fatalf("want Lease.Retry=2s, got %v", opts.Lease.Retry)
	}
}

func TestWithDefaults_zero(t *testing.T) {
	t.Parallel()
	opts := Options{}.withDefaults()
	d := DefaultOptions()
	if opts.Namespace != d.Namespace {
		t.Fatalf("want Namespace=%q, got %q", d.Namespace, opts.Namespace)
	}
	if opts.Lease.Duration != d.Lease.Duration {
		t.Fatalf("want Lease.Duration=%v, got %v", d.Lease.Duration, opts.Lease.Duration)
	}
	if opts.Lease.Renew != d.Lease.Renew {
		t.Fatalf("want Lease.Renew=%v, got %v", d.Lease.Renew, opts.Lease.Renew)
	}
	if opts.Lease.Retry != d.Lease.Retry {
		t.Fatalf("want Lease.Retry=%v, got %v", d.Lease.Retry, opts.Lease.Retry)
	}
}

func TestWithDefaults_preserves(t *testing.T) {
	t.Parallel()
	opts := Options{Namespace: "mynamespace"}.withDefaults()
	if opts.Namespace != "mynamespace" {
		t.Fatalf("want Namespace preserved as %q, got %q", "mynamespace", opts.Namespace)
	}
}
