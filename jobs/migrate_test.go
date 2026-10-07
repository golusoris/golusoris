// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/golusoris/golusoris/jobs"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

type typedNilContext struct{ context.Context }

func TestMigrateRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		ctx  context.Context
		pool *pgxpool.Pool
		want string
	}{
		"context": {ctx: nil, pool: new(pgxpool.Pool), want: "nil context"},
		"typed nil context": {
			ctx:  (*typedNilContext)(nil),
			pool: new(pgxpool.Pool),
			want: "nil context",
		},
		"pool": {ctx: t.Context(), pool: nil, want: "nil pool"},
		"uninitialized pool": {
			ctx:  t.Context(),
			pool: new(pgxpool.Pool),
			want: "invalid pool",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := jobs.Migrate(test.ctx, test.pool)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Migrate error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestMigrateIsIdempotent applies the river schema twice and verifies the table
// exists — a second call is a no-op (not an error).
func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	ctx := context.Background()

	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate (1): %v", err)
	}
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate (2, idempotent): %v", err)
	}

	var exists bool
	if err := pool.QueryRow(
		ctx,
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'river_job')",
	).Scan(&exists); err != nil {
		t.Fatalf("check river_job: %v", err)
	}
	if !exists {
		t.Fatal("river_job table was not created")
	}
}

func TestMigrateWithOneConnectionPool(t *testing.T) {
	t.Parallel()
	basePool := pgtest.Start(t)
	config := basePool.Config()
	config.MinConns = 0
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate with MaxConns=1: %v", err)
	}
}

// TestMigrateConcurrent is the #164 race guard: concurrent pod starts must all
// succeed (the advisory lock serializes them), applying the schema once.
func TestMigrateConcurrent(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	ctx := context.Background()

	const n = 3
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = jobs.Migrate(ctx, pool)
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Errorf("concurrent migrate %d: %v", i, e)
		}
	}
}
