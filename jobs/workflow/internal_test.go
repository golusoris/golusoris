// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workflow

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
)

type typedNilTemporalClient struct{ client.Client }

func TestWithDefaults_zeroFilled(t *testing.T) {
	t.Parallel()
	got := Config{}.withDefaults()
	d := DefaultConfig()
	if got.Host != d.Host {
		t.Errorf("Host = %q, want %q", got.Host, d.Host)
	}
	if got.Namespace != d.Namespace {
		t.Errorf("Namespace = %q, want %q", got.Namespace, d.Namespace)
	}
	if got.ConnectTimeout != d.ConnectTimeout {
		t.Errorf("ConnectTimeout = %v, want %v", got.ConnectTimeout, d.ConnectTimeout)
	}
}

func TestWithDefaults_preservesNonZero(t *testing.T) {
	t.Parallel()
	in := Config{
		Host:           "temporal.example.com:7233",
		Namespace:      "prod",
		TaskQueue:      "my-queue",
		ConnectTimeout: 2 * time.Second,
	}
	got := in.withDefaults()
	if got.Host != "temporal.example.com:7233" {
		t.Errorf("Host = %q, want temporal.example.com:7233", got.Host)
	}
	if got.Namespace != "prod" {
		t.Errorf("Namespace = %q, want prod", got.Namespace)
	}
	if got.TaskQueue != "my-queue" {
		t.Errorf("TaskQueue = %q, want my-queue", got.TaskQueue)
	}
	if got.ConnectTimeout != 2*time.Second {
		t.Errorf("ConnectTimeout = %v, want 2s", got.ConnectTimeout)
	}
}

func TestLoadConfig_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "localhost:7233" {
		t.Errorf("Host = %q, want localhost:7233", c.Host)
	}
	if c.Namespace != "default" {
		t.Errorf("Namespace = %q, want default", c.Namespace)
	}
}

func TestNewWorker_TypedNilClientFailsClosed(t *testing.T) {
	t.Parallel()

	lifecycle := fxtest.NewLifecycle(t)
	var temporalClient *typedNilTemporalClient
	_, err := newWorker(lifecycle, Config{TaskQueue: "tasks"}, temporalClient)
	if err == nil || !strings.Contains(err.Error(), "nil client") {
		t.Fatalf("newWorker: got %v", err)
	}
}

func TestNewClient_NilLoggerFailsClosed(t *testing.T) {
	t.Parallel()

	lifecycle := fxtest.NewLifecycle(t)
	temporalClient, err := newClient(lifecycle, Config{Host: "127.0.0.1:1"}, nil)
	if temporalClient != nil {
		temporalClient.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "nil logger") {
		t.Fatalf("newClient: got %v", err)
	}
}

func TestNewClientUsesBoundedDialContext(t *testing.T) {
	t.Parallel()

	lifecycle := fxtest.NewLifecycle(t)
	wantErr := errors.New("dial probe")
	const timeout = 250 * time.Millisecond
	var deadline time.Time
	_, err := newClientWithDial(
		lifecycle,
		Config{ConnectTimeout: timeout},
		slog.New(slog.DiscardHandler),
		func(ctx context.Context, _ client.Options) (client.Client, error) {
			var ok bool
			deadline, ok = ctx.Deadline()
			if !ok {
				t.Fatal("dial context has no deadline")
			}
			return nil, wantErr
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("newClientWithDial error = %v, want %v", err, wantErr)
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > timeout {
		t.Fatalf("dial deadline remaining = %v, want (0, %v]", remaining, timeout)
	}
}

func TestNewClientRejectsNegativeConnectTimeout(t *testing.T) {
	t.Parallel()

	lifecycle := fxtest.NewLifecycle(t)
	called := false
	_, err := newClientWithDial(
		lifecycle,
		Config{ConnectTimeout: -time.Second},
		slog.New(slog.DiscardHandler),
		func(context.Context, client.Options) (client.Client, error) {
			called = true
			return nil, errors.New("unexpected dial")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "connect timeout") {
		t.Fatalf("newClientWithDial error = %v, want connect timeout", err)
	}
	if called {
		t.Fatal("dial called with invalid connect timeout")
	}
}
