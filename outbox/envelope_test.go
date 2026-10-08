// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package outbox_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/outbox"
	"github.com/golusoris/golusoris/pubsub/cloudevents"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

const sampleTraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// insert runs fn in one committed transaction.
func insert(t *testing.T, pool *pgxpool.Pool, fn func(pgx.Tx) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	if fnErr := fn(tx); fnErr != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("insert: %v", fnErr)
	}
	require.NoError(t, tx.Commit(ctx))
}

func TestAddEventRejectsBeforeInsert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	require.ErrorIs(t, outbox.AddEvent(ctx, nil, "", struct{}{}, outbox.Envelope{}), outbox.ErrKindRequired)
	require.ErrorIs(t, outbox.Add(ctx, nil, "", struct{}{}), outbox.ErrKindRequired)

	tests := map[string]struct {
		kind string
		env  outbox.Envelope
		attr string
	}{
		"event id not UUID":      {"job.done", outbox.Envelope{EventID: "job-42"}, ""},
		"tracestate alone":       {"job.done", outbox.Envelope{TraceState: "a=b"}, ""},
		"malformed traceparent":  {"job.done", outbox.Envelope{TraceParent: "00-zz-01"}, ""},
		"all-zero trace id":      {"job.done", outbox.Envelope{TraceParent: "00-00000000000000000000000000000000-00f067aa0ba902b7-01"}, ""},
		"relative dataschema":    {"job.done", outbox.Envelope{DataSchema: "/schemas/job.json"}, "dataschema"},
		"control char in tenant": {"job.done", outbox.Envelope{Tenant: "acme\n"}, "tenant"},
		"control char in kind":   {"job\x00done", outbox.Envelope{}, "type"},
		"source not URI":         {"job.done", outbox.Envelope{Source: "%zz"}, "source"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := outbox.AddEvent(ctx, nil, tt.kind, struct{}{}, tt.env)
			require.ErrorIs(t, err, outbox.ErrInvalidEnvelope)
			if tt.attr != "" {
				var attrErr *cloudevents.AttributeError
				require.ErrorAs(t, err, &attrErr)
				require.Equal(t, tt.attr, attrErr.Name)
			}
		})
	}
}

func TestAddEventRejectsMalformedTraceParentUnderSpan(t *testing.T) {
	t.Parallel()
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)
	err := outbox.AddEvent(ctx, nil, "job.done", struct{}{}, outbox.Envelope{TraceParent: "garbage"})
	require.ErrorIs(t, err, outbox.ErrInvalidEnvelope, "the caller's own span must not validate a bad traceparent")
}

func TestEventCloudEvent(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 10, 7, 14, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	ev := outbox.Event{
		ID: 7, Kind: "job.completed", Payload: []byte(`{"job":42}`), CreatedAt: created,
		EventID: "6e8bc430-9c3a-11d9-9669-0800200c9a66", Subject: "job/42",
		DataSchema: "https://schemas.example.com/job.json", Tenant: "acme",
		TraceParent: sampleTraceParent, TraceState: "vendor=1",
	}
	ce, err := ev.CloudEvent("/vmafx/controller")
	require.NoError(t, err)
	require.Equal(t, cloudevents.Event{
		ID: ev.EventID, Source: "/vmafx/controller", Type: "job.completed", Subject: "job/42",
		Time: created.UTC(), DataContentType: "application/json",
		DataSchema: ev.DataSchema, Data: ev.Payload,
		Extensions: map[string]string{"tenant": "acme", "traceparent": sampleTraceParent, "tracestate": "vendor=1"},
	}, ce)

	ev.Source = "/vmafx/node-7"
	ce, err = ev.CloudEvent("/vmafx/controller")
	require.NoError(t, err)
	require.Equal(t, "/vmafx/node-7", ce.Source, "row source wins over the default")

	minimal, err := outbox.Event{Kind: "k", EventID: "6e8bc430-9c3a-11d9-9669-0800200c9a66"}.CloudEvent("/src")
	require.NoError(t, err)
	require.Nil(t, minimal.Extensions)
	require.True(t, minimal.Time.IsZero())
}

func TestEventCloudEventRejectsMissingAttributes(t *testing.T) {
	t.Parallel()
	var attrErr *cloudevents.AttributeError
	_, err := outbox.Event{ID: 3, Kind: "k"}.CloudEvent("/src")
	require.ErrorAs(t, err, &attrErr)
	require.Equal(t, "id", attrErr.Name, "rows from before the migration have no event id")

	_, err = outbox.Event{Kind: "k", EventID: "e"}.CloudEvent("")
	require.ErrorAs(t, err, &attrErr)
	require.Equal(t, "source", attrErr.Name)
}

func TestAddEventPersistsEnvelope(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	applyOutboxMigration(t, pool.Config().ConnConfig.ConnString())
	ctx := context.Background()
	explicitID := uuid.NewString()

	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36},
		SpanID:     trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7},
		TraceFlags: trace.FlagsSampled,
	})
	tracedCtx := trace.ContextWithSpanContext(ctx, spanCtx)
	insert(t, pool, func(tx pgx.Tx) error {
		if err := outbox.AddEvent(ctx, tx, "job.completed", map[string]int{"job": 42}, outbox.Envelope{
			EventID: strings.ToUpper(explicitID), Source: "/vmafx/controller", Subject: "job/42",
			DataSchema: "https://schemas.example.com/job.json", Tenant: "acme",
		}); err != nil {
			return err
		}
		return outbox.Add(tracedCtx, tx, "job.started", map[string]int{"job": 43})
	})

	events, err := outbox.Pending(ctx, pool, 10)
	require.NoError(t, err)
	require.Len(t, events, 2)
	byKind := map[string]outbox.Event{events[0].Kind: events[0], events[1].Kind: events[1]}

	done := byKind["job.completed"]
	require.Equal(t, explicitID, done.EventID, "event id is stored in canonical form")
	require.Equal(t, "/vmafx/controller", done.Source)
	require.Equal(t, "job/42", done.Subject)
	require.Equal(t, "https://schemas.example.com/job.json", done.DataSchema)
	require.Equal(t, "acme", done.Tenant)
	require.Empty(t, done.TraceParent, "context without a span stores no trace")

	started := byKind["job.started"]
	_, err = uuid.Parse(started.EventID)
	require.NoError(t, err, "database assigns a UUID")
	require.Equal(t, sampleTraceParent, started.TraceParent)
	require.Empty(t, started.Source)
}

func TestEventIDStableAcrossRetries(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	applyOutboxMigration(t, pool.Config().ConnConfig.ConnString())
	ctx := context.Background()
	insert(t, pool, func(tx pgx.Tx) error { return outbox.Add(ctx, tx, "job.completed", struct{}{}) })

	first, err := outbox.Pending(ctx, pool, 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.NoError(t, outbox.MarkFailed(ctx, pool, first[0].ID, errors.New("broker down")))
	_, err = pool.Exec(ctx, `UPDATE golusoris_outbox SET next_attempt_at = now() - interval '1 second'`)
	require.NoError(t, err)

	retry, err := outbox.Pending(ctx, pool, 1)
	require.NoError(t, err)
	require.Len(t, retry, 1)
	require.Equal(t, 1, retry[0].Attempts)
	require.Equal(t, first[0].EventID, retry[0].EventID)

	ce1, err := first[0].CloudEvent("/vmafx/controller")
	require.NoError(t, err)
	ce2, err := retry[0].CloudEvent("/vmafx/controller")
	require.NoError(t, err)
	require.Equal(t, ce1.ID, ce2.ID)
	require.JSONEq(t, `{}`, string(ce2.Data))
}

func TestAddEventDuplicateIDViolatesUniqueIndex(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	applyOutboxMigration(t, pool.Config().ConnConfig.ConnString())
	ctx := context.Background()
	env := outbox.Envelope{EventID: uuid.NewString()}
	insert(t, pool, func(tx pgx.Tx) error { return outbox.AddEvent(ctx, tx, "k", struct{}{}, env) })

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	err = outbox.AddEvent(ctx, tx, "k", struct{}{}, env)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23505", pgErr.Code, "unique_violation")
}

func TestCloudEventsMigrationRollsBack(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	dsn := pool.Config().ConnConfig.ConnString()
	applyOutboxMigration(t, dsn)
	m, err := dbmigrate.New(
		dbmigrate.Options{Path: "migrations"}.WithFS(outbox.MigrationsFS),
		dbpgx.Options{DSN: dsn},
		slog.New(slog.DiscardHandler),
	)
	require.NoError(t, err)
	defer func() { _ = m.Close() }()

	require.NoError(t, m.Steps(-1))
	var columns int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'golusoris_outbox' AND column_name = 'event_id'`).Scan(&columns))
	require.Zero(t, columns)
	require.NoError(t, m.Up())
	version, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(20261007000003), version)
}
