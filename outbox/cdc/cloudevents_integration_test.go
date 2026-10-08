// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cdc_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/outbox"
	outboxcdc "github.com/golusoris/golusoris/outbox/cdc"
	"github.com/golusoris/golusoris/pubsub/cloudevents"
	"github.com/golusoris/golusoris/pubsub/kafka"
	"github.com/golusoris/golusoris/pubsub/nats"
	kafkatest "github.com/golusoris/golusoris/testutil/kafka"
	natstest "github.com/golusoris/golusoris/testutil/nats"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

const defaultSource = "/vmafx/controller"

// pendingRow stores one event through AddEvent and reads it back as the
// drainers see it.
func pendingRow(t *testing.T) outbox.Event {
	t.Helper()
	pool := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	m, err := dbmigrate.New(
		dbmigrate.Options{Path: "migrations"}.WithFS(outbox.MigrationsFS),
		dbpgx.Options{DSN: pool.Config().ConnConfig.ConnString()},
		slog.New(slog.DiscardHandler),
	)
	require.NoError(t, err)
	require.NoError(t, m.Up())
	require.NoError(t, m.Close())

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, outbox.AddEvent(ctx, tx, "job.completed", map[string]int{"job": 42},
		outbox.Envelope{Subject: "job/42", Tenant: "acme"}))
	require.NoError(t, tx.Commit(ctx))
	events, err := outbox.Pending(ctx, pool, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	return events[0]
}

func requireRowEvent(t *testing.T, row outbox.Event, got cloudevents.Event) {
	t.Helper()
	require.NotEmpty(t, row.EventID)
	require.Equal(t, row.EventID, got.ID)
	require.Equal(t, defaultSource, got.Source)
	require.Equal(t, "job.completed", got.Type)
	require.Equal(t, "job/42", got.Subject)
	require.Equal(t, "acme", got.Extensions[outbox.TenantExtension])
	require.JSONEq(t, `{"job":42}`, string(got.Data))
	require.NoError(t, got.Validate())
}

func bootNATS(t *testing.T, url string) *nats.Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("nats:\n  url: "+url+"\n"), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	var client *nats.Client
	app := fxtest.New(t, fx.Supply(cfg, slog.New(slog.DiscardHandler)), nats.Module, fx.Populate(&client))
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	return client
}

// TestIntegration_OutboxRowToNATSCloudEvent replays one stored row through
// the sink, as the CDC drain does after a failed acknowledgement; JetStream
// keeps one valid CloudEvent with the row's event id.
func TestIntegration_OutboxRowToNATSCloudEvent(t *testing.T) {
	t.Parallel()
	row := pendingRow(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := bootNATS(t, natstest.Start(t))
	stream, err := client.JetStream().CreateStream(ctx, jetstream.StreamConfig{
		Name: "OUTBOX", Subjects: []string{"outbox.>"},
	})
	require.NoError(t, err)

	sink := outboxcdc.NewNATSCloudEventSink(client, "outbox.jobs", outboxcdc.CloudEventOptions{Source: defaultSource})
	require.NoError(t, sink.Send(ctx, row))
	require.NoError(t, sink.Send(ctx, row))

	info, err := stream.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), info.State.Msgs, "replayed row is deduplicated by Nats-Msg-Id")
	stored, err := stream.GetMsg(ctx, info.State.FirstSeq)
	require.NoError(t, err)
	got, err := nats.DecodeCloudEvent(stored.Header, stored.Data)
	require.NoError(t, err)
	requireRowEvent(t, row, got)
}

// TestIntegration_OutboxRowToKafkaCloudEvent produces one stored row twice;
// both records decode to valid CloudEvents with the same id.
func TestIntegration_OutboxRowToKafkaCloudEvent(t *testing.T) {
	t.Parallel()
	row := pendingRow(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	addr := kafkatest.Addr(t)
	const topic = "outbox-jobs"
	kc, err := kgo.NewClient(kgo.SeedBrokers(addr), kgo.AllowAutoTopicCreation(),
		kgo.ConsumeTopics(topic), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	require.NoError(t, err)
	t.Cleanup(kc.Close)
	client := kafka.ClientFromKgo(kc)

	sink := outboxcdc.NewKafkaCloudEventSink(client, topic, outboxcdc.CloudEventOptions{
		Source: defaultSource, Mode: cloudevents.ModeStructured,
	})
	require.NoError(t, sink.Send(ctx, row))
	require.NoError(t, sink.Send(ctx, row))

	var records []*kafka.Record
	for len(records) < 2 && ctx.Err() == nil {
		batch, pollErr := client.Poll(ctx, 2-len(records))
		require.NoError(t, pollErr)
		records = append(records, batch...)
	}
	require.Len(t, records, 2)
	for _, rec := range records {
		require.Equal(t, []byte("job/42"), rec.Key)
		got, decErr := kafka.DecodeCloudEventRecord(rec)
		require.NoError(t, decErr)
		requireRowEvent(t, row, got)
	}
}
