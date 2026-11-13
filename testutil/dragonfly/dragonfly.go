// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package dragonfly boots a real Dragonfly container via testcontainers-go and returns a connected [rueidis.Client].
//
// Start and Addr create isolated servers and register cleanup. Docker is required;
// container tests skip under testing.Short or an unavailable provider.
package dragonfly

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golusoris/golusoris/internal/testimages"
	"github.com/golusoris/golusoris/testutil/internal/startgate"
)

const startTimeout = 3 * time.Minute

// Options selects an immutable image; the zero value uses the repository pin.
type Options struct{ Image string }

func (o Options) image() (string, error) {
	if o.Image == "" {
		return testimages.Dragonfly, nil
	}
	if err := testimages.Validate(o.Image); err != nil {
		return "", fmt.Errorf("testutil/dragonfly: validate image: %w", err)
	}
	return o.Image, nil
}

// Start returns a client with client-side caching enabled and automatic cleanup.
func Start(t *testing.T) rueidis.Client { t.Helper(); return StartWith(t, Options{}) }

// StartWith returns a client connected to a new server configured by opts.
func StartWith(t *testing.T, opts Options) rueidis.Client {
	t.Helper()
	client, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{boot(t, opts)}})
	if err != nil {
		t.Fatalf("testutil/dragonfly: new client: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

// Addr returns a new server's host:port address with automatic cleanup.
func Addr(t *testing.T) string { t.Helper(); return AddrWith(t, Options{}) }

// AddrWith returns a new server's host:port address using opts.
func AddrWith(t *testing.T, opts Options) string { t.Helper(); return boot(t, opts) }

func boot(t *testing.T, opts Options) string {
	t.Helper()
	if testing.Short() {
		t.Skip("testutil/dragonfly: container-backed; skipped under -short")
	}
	image, err := opts.image()
	if err != nil {
		t.Fatal(err)
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)
	defer startgate.Acquire(t)()
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	req := testcontainers.GenericContainerRequest{
		Image: image, Cmd: []string{"--proactor_threads=2"}, ExposedPorts: []string{"6379/tcp"},
		WaitingFor: wait.ForListeningPort("6379/tcp"), Started: true,
	}
	if pinErr := testimages.WithPinnedReaper().Customize(&req); pinErr != nil {
		t.Fatalf("testutil/dragonfly: pin reaper: %v", pinErr)
	}
	ctr, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		t.Fatalf("testutil/dragonfly: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if termErr := ctr.Terminate(stopCtx); termErr != nil {
			t.Logf("testutil/dragonfly: terminate container: %v", termErr)
		}
	})
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("testutil/dragonfly: host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatalf("testutil/dragonfly: mapped port: %v", err)
	}
	return net.JoinHostPort(host, port.Port())
}
