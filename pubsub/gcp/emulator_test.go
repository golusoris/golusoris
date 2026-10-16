// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcp_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/internal/testimages"
	"github.com/golusoris/golusoris/pubsub/gcp"
)

func startEmulator(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("gcp: container-backed; skipped under -short")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	req := testcontainers.GenericContainerRequest{
		Image:        testimages.PubSubEmulator,
		ReaperImage:  testimages.Ryuk,
		ExposedPorts: []string{"8085/tcp"},
		Cmd: []string{
			"gcloud", "beta", "emulators", "pubsub", "start",
			"--host-port=0.0.0.0:8085",
			"--project=emulator-project",
		},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("8085/tcp"),
			wait.ForLog("[pubsub] INFO: Server started, listening on 8085"),
		).WithDeadline(4 * time.Minute),
		Started: true,
	}
	container, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		t.Fatalf("pubsub/gcp: start emulator container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		if termErr := container.Terminate(stopCtx); termErr != nil {
			t.Logf("pubsub/gcp: terminate emulator container: %v", termErr)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("pubsub/gcp: container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "8085/tcp")
	if err != nil {
		t.Fatalf("pubsub/gcp: mapped port: %v", err)
	}
	return net.JoinHostPort(host, port.Port())
}

func newConfig(t *testing.T, projectID string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "pubsub:\n  gcp:\n    project_id: \"" + projectID + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.New(config.Options{Files: []string{path}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

func newFxApp(t *testing.T, projectID string) (*gcp.Client, *fx.App) {
	t.Helper()
	cfg := newConfig(t, projectID)
	var client *gcp.Client
	app := fx.New(
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		gcp.Module,
		fx.Populate(&client),
		fx.NopLogger,
	)
	return client, app
}

func newFxClient(t *testing.T, projectID string) (*gcp.Client, *fx.App) {
	t.Helper()
	client, app := newFxApp(t, projectID)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New: %v", err)
	}
	return client, app
}

type receivedMessage struct {
	id    string
	data  []byte
	attrs map[string]string
}

func testPublishSubscribe(t *testing.T, client *gcp.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	recv := make(chan receivedMessage, 1)
	go func() {
		_ = client.Subscribe(subCtx, "events-sub", func(_ context.Context, m *gcp.Message) {
			recv <- receivedMessage{id: m.ID, data: m.Data, attrs: m.Attributes}
			m.Ack()
			subCancel()
		})
	}()

	wantData := []byte("hello-emulator")
	wantAttrs := map[string]string{"env": "test"}
	pubID, err := client.Publish(ctx, "events", wantData, wantAttrs)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case msg := <-recv:
		if string(msg.data) != string(wantData) {
			t.Errorf("data = %q, want %q", msg.data, wantData)
		}
		if msg.attrs["env"] != "test" {
			t.Errorf("attrs = %v, want env:test", msg.attrs)
		}
		if msg.id != pubID {
			t.Errorf("received message ID = %q, want %q", msg.id, pubID)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for message: %v", ctx.Err())
	}
}

func testNonExistentTopic(t *testing.T, client *gcp.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const missingTopic = "non-existent-topic"
	_, err := client.Publish(ctx, missingTopic, []byte("data"), nil)
	if err == nil {
		t.Fatal("expected error publishing to non-existent topic, got nil")
	}
	prefix := "gcp: publish to " + missingTopic + ": "
	if !strings.HasPrefix(err.Error(), prefix) {
		t.Fatalf("expected error prefix %q, got: %v", prefix, err)
	}
}

func testEmptyPayloadBoundary(t *testing.T, client *gcp.Client, project string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	createTopicAndSub(t, client.Pubsub(), project, "boundary-topic", "boundary-sub")
	// Google Pub/Sub requires data or attributes; emulator rejects empty messages with InvalidArgument (https://cloud.google.com/pubsub/docs/reference/rest/v1/PubsubMessage).
	if _, err := client.Publish(ctx, "boundary-topic", []byte{}, nil); err == nil {
		t.Fatal("expected empty payload without attributes to be rejected, got nil")
	} else if !strings.Contains(err.Error(), "InvalidArgument") {
		t.Fatalf("expected InvalidArgument error, got: %v", err)
	}
}

func TestEmulator_FxLifecycle(t *testing.T) {
	addr := startEmulator(t)
	t.Setenv("PUBSUB_EMULATOR_HOST", addr)

	const project = "emulator-lifecycle"
	client, app := newFxClient(t, project)

	startCtx, startCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("app.Start: %v", err)
	}

	createTopicAndSub(t, client.Pubsub(), project, "events", "events-sub")
	testPublishSubscribe(t, client)
	testNonExistentTopic(t, client)
	testEmptyPayloadBoundary(t, client, project)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatalf("app.Stop: %v", err)
	}

	if _, err := client.Publish(context.Background(), "events", []byte("after"), nil); !errors.Is(err, gcp.ErrClosed) {
		t.Fatalf("Publish after Close: got %v, want ErrClosed", err)
	}
}

func TestEmulator_Negative_EmptyProjectID(t *testing.T) {
	t.Parallel()
	_, app := newFxApp(t, "")
	err := app.Err()
	if err == nil {
		t.Fatal("expected error for empty project_id, got nil")
	}
	if !strings.Contains(err.Error(), "project_id required") {
		t.Fatalf("expected 'project_id required', got: %v", err)
	}
}

func TestEmulator_Negative_ClosedPortPing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("ln.Close: %v", err)
	}
	t.Setenv("PUBSUB_EMULATOR_HOST", addr)

	_, app := newFxClient(t, "proj-closed-port")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := app.Start(ctx); err == nil {
		t.Fatal("expected app.Start to fail when pinging closed port, got nil")
	}
}
