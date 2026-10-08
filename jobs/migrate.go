// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/golusoris/golusoris/core/validate"
)

// migrateLockKey is a fixed Postgres advisory-lock key ("river" in hex) so all
// pods serialize river schema migration on the same lock.
const migrateLockKey int64 = 0x7269766572

const migrationUnlockTimeout = 5 * time.Second

// Migrate applies river's schema migrations (DirectionUp) under a Postgres
// session advisory lock, so multiple pods booting together apply the schema
// exactly once — the holder migrates while the rest wait, then see a no-op.
// Run it as a one-shot init step (or fx.Invoke) before the jobs client starts;
// it is safe to call on every pod.
func Migrate(ctx context.Context, pool *pgxpool.Pool) (err error) {
	if validate.IsNil(ctx) {
		return errors.New("jobs: migrate: nil context")
	}
	if pool == nil {
		return errors.New("jobs: migrate: nil pool")
	}
	lockPool, err := migrationLockPool(ctx, pool)
	if err != nil {
		return err
	}
	conn, err := lockPool.Acquire(ctx)
	if err != nil {
		lockPool.Close()
		return fmt.Errorf("jobs: migrate: acquire conn: %w", err)
	}
	defer func() {
		conn.Release()
		lockPool.Close()
	}()

	if _, lockErr := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateLockKey); lockErr != nil {
		return fmt.Errorf("jobs: migrate: advisory lock: %w", lockErr)
	}
	defer func() {
		// Unlock even if ctx is already cancelled, else the lock lingers on the
		// pooled session until it is closed.
		unlockCtx, unlockCancel := migrationUnlockContext(ctx)
		defer unlockCancel()
		if _, unlockErr := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", migrateLockKey); unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("jobs: migrate: advisory unlock: %w", unlockErr))
		}
	}()

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), migrationConfig())
	if err != nil {
		return fmt.Errorf("jobs: migrate: build migrator: %w", err)
	}
	if _, migErr := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); migErr != nil {
		return fmt.Errorf("jobs: migrate: apply: %w", migErr)
	}
	return nil
}

func migrationUnlockContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), migrationUnlockTimeout)
}

func migrationLockPool(ctx context.Context, pool *pgxpool.Pool) (*pgxpool.Pool, error) {
	config, err := migrationPoolConfig(pool)
	if err != nil {
		return nil, err
	}
	config.MinConns = 0
	config.MaxConns = 1
	lockPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("jobs: migrate: build lock pool: %w", err)
	}
	return lockPool, nil
}

func migrationPoolConfig(pool *pgxpool.Pool) (config *pgxpool.Config, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			config = nil
			err = fmt.Errorf("jobs: migrate: invalid pool: %v", recovered)
		}
	}()
	config = pool.Config()
	if config == nil {
		return nil, errors.New("jobs: migrate: invalid pool")
	}
	return config, nil
}

func migrationConfig() *rivermigrate.Config {
	return &rivermigrate.Config{Logger: slog.New(slog.DiscardHandler)}
}
