// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/golusoris/golusoris/core/config"
)

// ---------------------------------------------------------------------------
// outbox.go — marshalPayload
// ---------------------------------------------------------------------------

func TestMarshalPayload_string(t *testing.T) {
	t.Parallel()
	raw, err := marshalPayload("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == nil {
		t.Fatal("expected non-nil bytes")
	}
}

func TestMarshalPayload_struct(t *testing.T) {
	t.Parallel()
	raw, err := marshalPayload(struct{ X int }{3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == nil {
		t.Fatal("expected non-nil bytes")
	}
}

func TestMarshalPayload_unmarshalable(t *testing.T) {
	t.Parallel()
	_, err := marshalPayload(make(chan int))
	if err == nil {
		t.Fatal("expected error for channel payload, got nil")
	}
}

type executorFunc func(context.Context, string, ...any) (pgconn.CommandTag, error)

func (f executorFunc) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return f(ctx, query, args...)
}

func TestMarkDispatchedRequiresExactlyOneRow(t *testing.T) {
	t.Parallel()
	for _, rows := range []int64{0, 2} {
		t.Run(fmt.Sprintf("rows_%d", rows), func(t *testing.T) {
			t.Parallel()
			executor := executorFunc(func(context.Context, string, ...any) (pgconn.CommandTag, error) {
				return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", rows)), nil
			})
			err := markDispatched(context.Background(), executor, 42)
			if rows == 0 && !errors.Is(err, ErrEventNotFound) {
				t.Fatalf("markDispatched() error = %v, want ErrEventNotFound", err)
			}
			if rows == 2 && !errors.Is(err, ErrUnexpectedRowsAffected) {
				t.Fatalf("markDispatched() error = %v, want ErrUnexpectedRowsAffected", err)
			}
		})
	}
	executor := executorFunc(func(context.Context, string, ...any) (pgconn.CommandTag, error) {
		return pgconn.NewCommandTag("UPDATE 1"), nil
	})
	if err := markDispatched(context.Background(), executor, 42); err != nil {
		t.Fatalf("markDispatched() error = %v", err)
	}
}

func TestMarkDispatchedInQuotesConfiguredRelation(t *testing.T) {
	t.Parallel()
	var query string
	var args []any
	executor := executorFunc(func(_ context.Context, gotQuery string, gotArgs ...any) (pgconn.CommandTag, error) {
		query = gotQuery
		args = gotArgs
		return pgconn.NewCommandTag("UPDATE 1"), nil
	})
	if err := MarkDispatchedIn(
		context.Background(), executor, "Tenant One", `Order"Events`, 42,
	); err != nil {
		t.Fatalf("MarkDispatchedIn() error = %v", err)
	}
	const want = `UPDATE "Tenant One"."Order""Events" SET dispatched_at = now() WHERE id = $1`
	if query != want {
		t.Fatalf("query = %q, want %q", query, want)
	}
	if len(args) != 1 || args[0] != int64(42) {
		t.Fatalf("args = %#v, want [42]", args)
	}
}

func TestMarkDispatchedInRejectsInvalidRelationBeforeSQL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		schema string
		table  string
	}{
		{name: "empty schema", table: DefaultTable},
		{name: "empty table", schema: DefaultSchema},
		{name: "schema NUL", schema: "bad\x00schema", table: DefaultTable},
		{name: "table NUL", schema: DefaultSchema, table: "bad\x00table"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			called := false
			executor := executorFunc(func(context.Context, string, ...any) (pgconn.CommandTag, error) {
				called = true
				return pgconn.NewCommandTag("UPDATE 1"), nil
			})
			err := MarkDispatchedIn(context.Background(), executor, test.schema, test.table, 42)
			if !errors.Is(err, ErrInvalidRelationIdentifier) {
				t.Fatalf("MarkDispatchedIn() error = %v, want ErrInvalidRelationIdentifier", err)
			}
			if called {
				t.Fatal("invalid relation reached executor")
			}
		})
	}
}

func TestValidateRelation(t *testing.T) {
	t.Parallel()
	if err := ValidateRelation(DefaultSchema, DefaultTable); err != nil {
		t.Fatalf("ValidateRelation(defaults): %v", err)
	}
	for _, test := range []struct {
		name   string
		schema string
		table  string
	}{
		{name: "empty schema", table: DefaultTable},
		{name: "empty table", schema: DefaultSchema},
		{name: "schema NUL", schema: "bad\x00schema", table: DefaultTable},
		{name: "table NUL", schema: DefaultSchema, table: "bad\x00table"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateRelation(test.schema, test.table); !errors.Is(err, ErrInvalidRelationIdentifier) {
				t.Fatalf("ValidateRelation() error = %v, want ErrInvalidRelationIdentifier", err)
			}
		})
	}
}

func TestMarkFailedValidatesCauseAndAffectedRow(t *testing.T) {
	t.Parallel()
	called := false
	executor := executorFunc(func(context.Context, string, ...any) (pgconn.CommandTag, error) {
		called = true
		return pgconn.NewCommandTag("UPDATE 1"), nil
	})
	if err := markFailed(
		context.Background(), executor, 42, nil, DefaultRetryDelay,
	); !errors.Is(err, ErrNilDispatchError) {
		t.Fatalf("markFailed() error = %v, want ErrNilDispatchError", err)
	}
	if called {
		t.Fatal("markFailed() executed SQL for nil cause")
	}

	for _, rows := range []int64{0, 2} {
		t.Run(fmt.Sprintf("rows_%d", rows), func(t *testing.T) {
			t.Parallel()
			rowExecutor := executorFunc(func(context.Context, string, ...any) (pgconn.CommandTag, error) {
				return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", rows)), nil
			})
			err := markFailed(
				context.Background(), rowExecutor, 42, errors.New("dispatch"), DefaultRetryDelay,
			)
			if rows == 0 && !errors.Is(err, ErrEventNotFound) {
				t.Fatalf("markFailed() error = %v, want ErrEventNotFound", err)
			}
			if rows == 2 && !errors.Is(err, ErrUnexpectedRowsAffected) {
				t.Fatalf("markFailed() error = %v, want ErrUnexpectedRowsAffected", err)
			}
		})
	}
	if err := markFailed(
		context.Background(), executor, 42, errors.New("dispatch"), DefaultRetryDelay,
	); err != nil {
		t.Fatalf("markFailed() error = %v", err)
	}
}

// ---------------------------------------------------------------------------
// drainer.go — DefaultDrainerOptions / loadDrainerOptions
// ---------------------------------------------------------------------------

func TestDefaultDrainerOptions(t *testing.T) {
	t.Parallel()
	opts := DefaultDrainerOptions()
	if opts.Interval != time.Second {
		t.Errorf("expected Interval=1s, got %v", opts.Interval)
	}
	if opts.Batch != 100 {
		t.Errorf("expected Batch=100, got %d", opts.Batch)
	}
	if opts.RetryDelay != DefaultRetryDelay {
		t.Errorf("expected RetryDelay=%v, got %v", DefaultRetryDelay, opts.RetryDelay)
	}
	if opts.DrainTimeout != 30*time.Second {
		t.Errorf("expected DrainTimeout=30s, got %v", opts.DrainTimeout)
	}
	if opts.RollbackTimeout != 5*time.Second {
		t.Errorf("expected RollbackTimeout=5s, got %v", opts.RollbackTimeout)
	}
}

func TestWithDrainerDefaultsCapsBatch(t *testing.T) {
	t.Parallel()
	if got := withDrainerDefaults(DrainerOptions{Batch: MaxPendingLimit + 1}).Batch; got != MaxPendingLimit {
		t.Fatalf("Batch = %d, want %d", got, MaxPendingLimit)
	}
}

func TestWithDrainerDefaultsBoundsRetryDelay(t *testing.T) {
	t.Parallel()
	if got := withDrainerDefaults(DrainerOptions{RetryDelay: -1}).RetryDelay; got != DefaultRetryDelay {
		t.Fatalf("negative RetryDelay = %v, want default %v", got, DefaultRetryDelay)
	}
	if got := withDrainerDefaults(DrainerOptions{RetryDelay: MaxRetryDelay + 1}).RetryDelay; got != MaxRetryDelay {
		t.Fatalf("large RetryDelay = %v, want cap %v", got, MaxRetryDelay)
	}
}

func TestLoadDrainerOptions(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts, err := loadDrainerOptions(cfg)
	if err != nil {
		t.Fatalf("loadDrainerOptions: %v", err)
	}
	// Empty config should return defaults.
	if opts.Interval != time.Second {
		t.Errorf("expected Interval=1s, got %v", opts.Interval)
	}
	if opts.Batch != 100 {
		t.Errorf("expected Batch=100, got %d", opts.Batch)
	}
	if opts.RetryDelay != DefaultRetryDelay {
		t.Errorf("expected RetryDelay=%v, got %v", DefaultRetryDelay, opts.RetryDelay)
	}
}

type beginFunc func(context.Context) (pgx.Tx, error)

func (f beginFunc) Begin(ctx context.Context) (pgx.Tx, error) { return f(ctx) }

type rollbackFunc func(context.Context) error

func (f rollbackFunc) Rollback(ctx context.Context) error { return f(ctx) }

func TestDrainerDrainOnceHasDeadline(t *testing.T) {
	t.Parallel()
	opts := DefaultDrainerOptions()
	opts.DrainTimeout = 10 * time.Millisecond
	d := &Drainer{
		pool: beginFunc(func(ctx context.Context) (pgx.Tx, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
		opts: opts,
	}
	started := time.Now()
	err := d.drainOnce(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drainOnce error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("drain timeout took %v", elapsed)
	}
}

func TestDrainerRollbackUsesDetachedDeadline(t *testing.T) {
	t.Parallel()
	opts := DefaultDrainerOptions()
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	d := &Drainer{opts: opts, logger: slog.New(slog.DiscardHandler)}
	d.rollback(parent, rollbackFunc(func(ctx context.Context) error {
		called = true
		if err := ctx.Err(); err != nil {
			t.Fatalf("rollback context already canceled: %v", err)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("rollback context has no deadline")
		}
		return pgx.ErrTxClosed
	}))
	if !called {
		t.Fatal("rollback was not called")
	}
}
