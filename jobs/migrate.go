package jobs

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// migrateLockKey is a fixed Postgres advisory-lock key ("river" in hex) so all
// pods serialize river schema migration on the same lock.
const migrateLockKey int64 = 0x7269766572

// Migrate applies river's schema migrations (DirectionUp) under a Postgres
// session advisory lock, so multiple pods booting together apply the schema
// exactly once — the holder migrates while the rest wait, then see a no-op.
// Run it as a one-shot init step (or fx.Invoke) before the jobs client starts;
// it is safe to call on every pod.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("jobs: migrate: acquire conn: %w", err)
	}
	defer conn.Release()

	if _, lockErr := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateLockKey); lockErr != nil {
		return fmt.Errorf("jobs: migrate: advisory lock: %w", lockErr)
	}
	defer func() {
		// Unlock even if ctx is already cancelled, else the lock lingers on the
		// pooled session until it is closed.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrateLockKey)
	}()

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("jobs: migrate: build migrator: %w", err)
	}
	if _, migErr := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); migErr != nil {
		return fmt.Errorf("jobs: migrate: apply: %w", migErr)
	}
	return nil
}
