package cdc

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

// TestConsumer_StreamsWAL exercises the full decode path against a real
// logical-replication Postgres and asserts the decoded [Event] stream for
// INSERT/UPDATE/DELETE: connect → runSetup → ensureSlot → StartReplication →
// handleMessage → dispatch → Parse → tupleToMap, none of which carry unit
// coverage.
//
// The slot is created and all DML committed BEFORE replication starts, so the
// changes are durably retained; runSetup must resume from the slot's confirmed
// position (LSN 0) and replay them. The receive loop is driven directly with a
// single bounded deadline rather than via runLoop's keepalive timer — that
// keeps the test deterministic under -race instead of depending on the
// sub-second re-arm cadence.
func TestConsumer_StreamsWAL(t *testing.T) {
	t.Parallel()
	pool, replDSN := pgtest.StartReplication(t)
	ctx := context.Background()

	// App-owned schema: a table + the publication db/cdc subscribes to.
	mustExec(ctx, t, pool, `CREATE TABLE orders (id int PRIMARY KEY, sku text)`)
	// REPLICA IDENTITY FULL so UPDATE/DELETE carry Old column values.
	mustExec(ctx, t, pool, `ALTER TABLE orders REPLICA IDENTITY FULL`)
	mustExec(ctx, t, pool, `CREATE PUBLICATION golusoris FOR TABLE orders`)

	// Create the slot up-front via SQL so retained WAL covers the DML below.
	mustExec(ctx, t, pool,
		`SELECT pg_create_logical_replication_slot('golusoris', 'pgoutput')`)

	mustExec(ctx, t, pool, `INSERT INTO orders (id, sku) VALUES (1, 'abc')`)
	mustExec(ctx, t, pool, `UPDATE orders SET sku = 'xyz' WHERE id = 1`)
	mustExec(ctx, t, pool, `DELETE FROM orders WHERE id = 1`)

	var got []Event
	c := &Consumer{
		cfg:    Config{DSN: replDSN}.withDefaults(),
		clk:    clockwork.NewRealClock(),
		logger: slog.New(slog.NewTextHandler(testWriter{t}, nil)), // route consumer warnings/errors to t.Logf for CI diagnosis
		handler: func(_ context.Context, ev Event) error {
			got = append(got, ev)
			return nil
		},
	}

	drainEvents(ctx, t, c, 3)
	require.Len(t, got, 3)

	require.Equal(t, OpInsert, got[0].Op)
	require.Equal(t, "orders", got[0].Table)
	require.Equal(t, "public", got[0].Schema)
	require.Equal(t, "1", got[0].New["id"])
	require.Equal(t, "abc", got[0].New["sku"])

	require.Equal(t, OpUpdate, got[1].Op)
	require.Equal(t, "xyz", got[1].New["sku"])
	require.Equal(t, "abc", got[1].Old["sku"]) // REPLICA IDENTITY FULL carries old values

	require.Equal(t, OpDelete, got[2].Op)
	require.Equal(t, "1", got[2].Old["id"])
}

// drainEvents connects the consumer, runs setup, then reads raw WAL messages,
// feeding each through handleMessage until the handler has dispatched want
// events. It drives handleMessage directly (not runLoop) so the read is never
// interrupted on a sub-second cadence — keeping the test deterministic under
// -race. It wraps the consumer's handler to count dispatched events.
func drainEvents(ctx context.Context, t *testing.T, c *Consumer, want int) {
	t.Helper()
	conn, err := c.connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	_, ok := c.runSetup(ctx, conn)
	require.True(t, ok, "runSetup must succeed")

	relations := map[uint32]*pglogrepl.RelationMessage{}
	clientXLogPos := pglogrepl.LSN(0)
	nextStandby := c.clk.Now().Add(time.Hour) // never auto-send in this driver

	deadline := time.Now().Add(30 * time.Second)
	seen := 0
	wrapped := c.handler
	c.handler = func(hctx context.Context, ev Event) error {
		seen++
		return wrapped(hctx, ev)
	}

	for seen < want {
		require.False(t, time.Now().After(deadline), "cdc: timed out waiting for %d events (got %d)", want, seen)
		recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		raw, rerr := conn.ReceiveMessage(recvCtx)
		cancel()
		if rerr != nil {
			require.True(t, pgconn.Timeout(rerr), "cdc: receive: %v", rerr)
			continue
		}
		c.handleMessage(ctx, raw, relations, &clientXLogPos, &nextStandby)
	}
}

// TestConsumer_EnsureSlotIdempotent verifies ensureSlot tolerates a slot that
// already exists (SQLSTATE 42710), so restarts don't fail. Two runs against
// the same container must both succeed.
func TestConsumer_EnsureSlotIdempotent(t *testing.T) {
	t.Parallel()
	pool, replDSN := pgtest.StartReplication(t)
	ctx := context.Background()
	mustExec(ctx, t, pool, `CREATE TABLE t (id int PRIMARY KEY)`)
	mustExec(ctx, t, pool, `CREATE PUBLICATION golusoris FOR TABLE t`)

	c := &Consumer{
		cfg:     Config{DSN: replDSN}.withDefaults(),
		clk:     clockwork.NewRealClock(),
		logger:  slog.New(slog.DiscardHandler),
		handler: noopHandler,
	}

	conn, err := c.connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	// First ensureSlot creates it; second must be a no-op (42710 swallowed).
	require.NoError(t, c.ensureSlot(ctx, conn, 0))
	require.NoError(t, c.ensureSlot(ctx, conn, 0))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("consumer: %s", p)
	return len(p), nil
}

func mustExec(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	_, err := pool.Exec(ctx, sql)
	require.NoError(t, err, "exec: %s", sql)
}
