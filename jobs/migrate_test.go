package jobs_test

import (
	"context"
	"sync"
	"testing"

	"github.com/golusoris/golusoris/jobs"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

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
