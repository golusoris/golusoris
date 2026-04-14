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
	"testing"
	"time"

	"github.com/redis/rueidis"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

const (
	defaultImage = "redis:7-alpine"
	startTimeout = 60 * time.Second
)

// Start boots a Redis container and returns a connected rueidis.Client.
// The container and client are stopped/closed via t.Cleanup.
func Start(t *testing.T) rueidis.Client {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	ctr, err := tcredis.Run(ctx, defaultImage)
	if err != nil {
		t.Fatalf("testutil/redis: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = ctr.Terminate(stopCtx)
	})

	addr, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("testutil/redis: connection string: %v", err)
	}
	// Strip the "redis://" scheme — rueidis expects "host:port".
	addr = stripScheme(addr)

	client, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{addr},
	})
	if err != nil {
		t.Fatalf("testutil/redis: new client: %v", err)
	}
	t.Cleanup(client.Close)

	return client
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
