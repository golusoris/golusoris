// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package kafka_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/pubsub/cloudevents"
	"github.com/golusoris/golusoris/pubsub/kafka"
	kafkatest "github.com/golusoris/golusoris/testutil/kafka"
)

// pollRecords polls until n records arrived or ctx ends.
func pollRecords(ctx context.Context, t *testing.T, c *kafka.Client, n int) []*kafka.Record {
	t.Helper()
	got := make([]*kafka.Record, 0, n)
	for len(got) < n && ctx.Err() == nil {
		batch, err := c.Poll(ctx, n-len(got))
		require.NoError(t, err)
		got = append(got, batch...)
	}
	require.Len(t, got, n)
	return got
}

func TestIntegration_CloudEventRoundTrip(t *testing.T) {
	t.Parallel()
	addr := kafkatest.Addr(t)
	topic := uniqueTopic(t)
	producer := newTestClient(t, addr, "")
	consumer := newTestClient(t, addr, "group-"+topic)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	want := map[string]cloudevents.Event{"binary": sampleEvent("evt-binary"), "structured": sampleEvent("evt-structured")}
	binary, err := kafka.NewCloudEventRecord(topic, []byte("job/42"), want["binary"], cloudevents.ModeBinary)
	require.NoError(t, err)
	structured, err := kafka.NewCloudEventRecord(topic, []byte("job/42"), want["structured"], cloudevents.ModeStructured)
	require.NoError(t, err)
	require.NoError(t, producer.Produce(ctx, binary, structured))

	consumer.Subscribe(topic)
	for _, rec := range pollRecords(ctx, t, consumer, 2) {
		ev, decErr := kafka.DecodeCloudEventRecord(rec)
		require.NoError(t, decErr)
		mode := "binary"
		if ev.ID == "evt-structured" {
			mode = "structured"
		}
		requireSameEvent(t, want[mode], ev)
		require.Equal(t, []byte("job/42"), rec.Key)
	}
}

// bootModule starts kafka.Module with yaml config and returns the start error.
func bootModule(t *testing.T, yaml string) (*kafka.Client, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	var client *kafka.Client
	app := fx.New(
		fx.NopLogger,
		fx.Supply(cfg, slog.New(slog.DiscardHandler)),
		kafka.Module,
		fx.Populate(&client),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if startErr := app.Start(ctx); startErr != nil {
		return nil, startErr
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		require.NoError(t, app.Stop(stopCtx))
	})
	return client, nil
}

func TestIntegration_SASLSCRAM(t *testing.T) {
	t.Parallel()
	addr := kafkatest.AddrSASL(t, "svc", "s3cret-pass")
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	bad := filepath.Join(dir, "bad")
	require.NoError(t, os.WriteFile(good, []byte("s3cret-pass\n"), 0o600))
	require.NoError(t, os.WriteFile(bad, []byte("wrong-pass\n"), 0o600))
	const tmpl = "kafka:\n  brokers: [%q]\n  sasl:\n    mechanism: SCRAM-SHA-256\n    user: svc\n    passwordfile: %q\n"

	client, err := bootModule(t, fmt.Sprintf(tmpl, addr, good))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Ping sends a metadata request, which the broker answers only on an authenticated connection.
	require.NoError(t, client.Kgo().Ping(ctx))

	_, err = bootModule(t, fmt.Sprintf(tmpl, addr, bad))
	require.Error(t, err, "a wrong SCRAM password must fail the startup ping")
}
