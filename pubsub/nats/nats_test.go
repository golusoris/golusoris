package nats_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/pubsub/nats"
	natstestutil "github.com/golusoris/golusoris/testutil/nats"
)

// TestPackage_compiles verifies the package exports are reachable without Docker.
func TestPackage_compiles(t *testing.T) {
	t.Parallel()
	_ = nats.Module
}

// bootClient starts a *nats.Client via the fx lifecycle wired to url.
// The app is stopped via t.Cleanup.
func bootClient(t *testing.T, url string) *nats.Client {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("nats:\n  url: "+url+"\n  name: test\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.New(config.Options{Files: []string{cfgPath}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	var client *nats.Client
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		nats.Module,
		fx.Populate(&client),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	return client
}

// TestIntegration_ConnectAndPing confirms the Module wires up and connects.
func TestIntegration_ConnectAndPing(t *testing.T) {
	t.Parallel()

	url := natstestutil.Start(t)
	c := bootClient(t, url)
	if !c.Conn().IsConnected() {
		t.Fatal("expected NATS connection to be connected")
	}
}

// TestIntegration_PublishSubscribe exercises core pub/sub delivery end-to-end.
func TestIntegration_PublishSubscribe(t *testing.T) {
	t.Parallel()

	url := natstestutil.Start(t)
	c := bootClient(t, url)

	ch := make(chan []byte, 1)
	sub, err := c.Subscribe("test.subject", func(msg *natsgo.Msg) {
		ch <- msg.Data
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	want := []byte("hello-nats")
	if err := c.Publish("test.subject", want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-ch:
		if string(got) != string(want) {
			t.Errorf("got %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

// TestIntegration_JetStreamAvailable confirms the JetStream context is usable.
func TestIntegration_JetStreamAvailable(t *testing.T) {
	t.Parallel()

	url := natstestutil.Start(t)
	c := bootClient(t, url)
	if c.JetStream() == nil {
		t.Fatal("expected non-nil JetStream context")
	}
}
