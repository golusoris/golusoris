// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package redis boots a real Redis container via testcontainers-go and
// returns a connected [rueidis.Client]. Used by tests that need to
// exercise actual Redis behaviour (TTL, pub/sub, distributed locks).
//
// Usage:
//
//	func TestMyHandler(t *testing.T) {
//	    c := redistest.Start(t)
//	    // c is a rueidis.Client connected to the container
//	}
//
// Each call spins a fresh container — tests are isolated.
// Docker is required (testutil/pg contract).
package redis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/golusoris/golusoris/internal/testimages"
	"github.com/golusoris/golusoris/testutil/internal/startgate"
)

const (
	// startTimeout bounds one container start including a cold image pull;
	// same value as testutil/pg (see the rationale there: cold CI runners).
	startTimeout = 3 * time.Minute
)

// Options tweak the container. Zero value uses defaults.
type Options struct {
	// Image is an immutable Redis image reference (tag@sha256:<digest>), for
	// example to stay on another major. Empty uses the repository pin.
	Image string
}

// image returns the reference to boot: the repository pin, or the explicit immutable one.
func (o Options) image() (string, error) {
	if o.Image == "" {
		return testimages.Redis, nil
	}
	if err := testimages.Validate(o.Image); err != nil {
		return "", fmt.Errorf("testutil/redis: validate image: %w", err)
	}
	return o.Image, nil
}

// Start boots a Redis container and returns a connected rueidis.Client.
// The container and client are stopped/closed via t.Cleanup.
func Start(t *testing.T) rueidis.Client {
	t.Helper()
	return StartWith(t, Options{})
}

// StartWith is Start with opts.
func StartWith(t *testing.T, opts Options) rueidis.Client {
	t.Helper()
	client, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{boot(t, opts)},
	})
	if err != nil {
		t.Fatalf("testutil/redis: new client: %v", err)
	}
	t.Cleanup(client.Close)

	return client
}

// Addr boots a Redis container and returns its "host:port" address (scheme
// stripped). Use this when you need to drive your own client constructor
// against a real Redis instead of the ready-made rueidis.Client from Start.
// The container is terminated via t.Cleanup.
func Addr(t *testing.T) string {
	t.Helper()
	return boot(t, Options{})
}

// AddrWith is Addr with opts.
func AddrWith(t *testing.T, opts Options) string {
	t.Helper()
	return boot(t, opts)
}

// boot starts the container and returns its "host:port" address.
func boot(t *testing.T, opts Options) string {
	t.Helper()
	if testing.Short() {
		t.Skip("testutil/redis: container-backed; skipped under -short")
	}
	image, err := opts.image()
	if err != nil {
		t.Fatal(err)
	}
	testcontainers.SkipIfProviderIsNotHealthy(t) // skip cleanly when Docker is unavailable (e.g. macOS CI) instead of failing

	// Queue for a boot slot first, so startTimeout only counts the boot itself.
	defer startgate.Acquire(t)()
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	ctr, err := tcredis.Run(ctx, image, testimages.WithPinnedReaper())
	if err != nil {
		t.Fatalf("testutil/redis: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if terr := ctr.Terminate(stopCtx); terr != nil {
			t.Logf("testutil/redis: terminate container: %v", terr)
		}
	})

	addr, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("testutil/redis: connection string: %v", err)
	}
	// Strip the "redis://" scheme: rueidis expects "host:port".
	return stripScheme(addr)
}

// stripScheme removes "redis://" or any "scheme://" prefix.
func stripScheme(addr string) string {
	if idx := indexOf(addr, "://"); idx >= 0 {
		return addr[idx+3:]
	}
	return addr
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
