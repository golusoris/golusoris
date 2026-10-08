// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package memory

import (
	"log/slog"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/config"
)

func TestDefaultOptions_maxSize(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	if opts.MaxSize != 10_000 {
		t.Errorf("MaxSize = %d, want 10000", opts.MaxSize)
	}
}

func TestDefaultOptions_ttl(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	if opts.TTL != 5*time.Minute {
		t.Errorf("TTL = %v, want 5m", opts.TTL)
	}
}

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_MEMORY_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.MaxSize != 10_000 {
		t.Errorf("MaxSize = %d, want 10000", opts.MaxSize)
	}
	if opts.TTL != 5*time.Minute {
		t.Errorf("TTL = %v, want 5m", opts.TTL)
	}
}

func TestNewCache_RejectsNilLogger(t *testing.T) {
	t.Parallel()
	c, err := newCache(defaultOptions(), nil)
	if err == nil {
		t.Fatal("newCache accepted a nil logger")
	}
	if c != nil {
		t.Fatal("newCache returned a cache with nil logger")
	}
}

func TestNewCacheRejectsUnboundedOrNegativeOptions(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	for name, opts := range map[string]Options{
		"zero maximum": {MaxSize: 0},
		"negative TTL": {MaxSize: 1, TTL: -time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if cache, err := newCache(opts, logger); err == nil || cache != nil {
				t.Fatalf("newCache() = (%v, %v), want validation error", cache, err)
			}
		})
	}
}

func TestZeroDefaultTTLStillSupportsPerEntryExpiry(t *testing.T) {
	t.Parallel()
	cache, err := NewForTest(10, 0)
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}
	cache.Set("key", 1)
	cache.SetExpiresAfter("key", 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	if _, ok := cache.GetIfPresent("key"); ok {
		t.Fatal("entry remained after explicit per-entry expiry")
	}
}
