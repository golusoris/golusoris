// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/store/storetest at 6111de5a.

package pg

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	defaultTemplateDB = "template"
	opTimeout         = 30 * time.Second
	ryukLinger        = 5 * time.Minute
	ryukEnvShort      = "RYUK_RECONNECTION_TIMEOUT"
	ryukEnvLong       = "TESTCONTAINERS_RYUK_RECONNECTION_TIMEOUT"
)

// shared holds per-process container and admin pool state for TestMain template cloning.
var shared struct { // WHY: shared container lifecycle requires process-wide coordination across TestMain and tests.
	mu           sync.Mutex
	admin        *pgxpool.Pool
	cfg          *pgxpool.Config
	templateName string
	err          error
	serial       atomic.Int64
}

// MigrateFunc applies database migrations or schema setup to the template database.
type MigrateFunc func(ctx context.Context, dsn string) error

// Main runs the package's tests around one shared Postgres container.
// It creates a template database, runs migrate once against it, and terminates
// the container at exit. Tests clone the template with [Clone] or [CloneDSN].
// Call it from TestMain:
//
//	func TestMain(m *testing.M) {
//	    os.Exit(pg.Main(m, migrate))
//	}
func Main(m *testing.M, migrate MigrateFunc, opts ...Options) int {
	flag.Parse()
	if testing.Short() {
		return m.Run()
	}
	stop, err := startShared(migrate, opts...)
	shared.err = err
	code := m.Run()
	if stopErr := stop(); stopErr != nil {
		fmt.Fprintf(os.Stderr, "testutil/pg: stop shared container: %v\n", stopErr)
	}
	status, msg := exitStatus(code, err)
	if msg != "" {
		fmt.Fprint(os.Stderr, msg)
	}
	return status
}

// exitStatus names the shared-container start error, so a red binary shows its cause.
func exitStatus(code int, startErr error) (int, string) {
	if startErr == nil {
		return code, ""
	}
	msg := fmt.Sprintf("testutil/pg: start shared container: %v\n", startErr)
	if code == 0 {
		return 1, msg
	}
	return code, msg
}

// startShared boots the template container, applies migration, and establishes admin pool.
func startShared(migrate MigrateFunc, opts ...Options) (func() error, error) {
	if err := lingerReaper(); err != nil {
		return noopStop, err
	}
	o := resolveOptions(opts)
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	ctr, runErr := runContainer(ctx, o)
	if runErr != nil {
		return noopStop, runErr
	}
	stop := makeStop(ctr)

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return stop, fmt.Errorf("testutil/pg: connection string: %w", err)
	}
	if migrate != nil {
		if migrateErr := migrate(ctx, dsn); migrateErr != nil {
			return stop, fmt.Errorf("testutil/pg: migrate template: %w", migrateErr)
		}
	}
	if adminErr := setupAdmin(ctx, dsn, o.Database); adminErr != nil {
		return stop, adminErr
	}
	return stop, nil
}

func noopStop() error { return nil }

func resolveOptions(opts []Options) Options {
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Database == "" {
		o.Database = defaultTemplateDB
	}
	return o.withDefaults()
}

func makeStop(ctr *tcpostgres.PostgresContainer) func() error {
	return func() error {
		shared.mu.Lock()
		if shared.admin != nil {
			shared.admin.Close()
			shared.admin = nil
		}
		shared.mu.Unlock()
		if ctr == nil {
			return nil
		}
		// One container per binary: teardown at high load took the whole terminate bound.
		return terminate(ctr, startTimeout)
	}
}

func setupAdmin(ctx context.Context, dsn, templateName string) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("testutil/pg: parse dsn: %w", err)
	}
	adminCfg := cfg.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	adminPool, err := pgxpool.NewWithConfig(ctx, adminCfg)
	if err != nil {
		return fmt.Errorf("testutil/pg: admin pool: %w", err)
	}
	if err := disconnectDatabase(ctx, adminPool, templateName); err != nil {
		adminPool.Close()
		return err
	}
	shared.mu.Lock()
	shared.admin = adminPool
	shared.cfg = cfg
	shared.templateName = templateName
	shared.mu.Unlock()
	return nil
}

func disconnectDatabase(ctx context.Context, pool *pgxpool.Pool, name string) error {
	terminateSQL := `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`
	checkSQL := `SELECT count(*) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`
	for range 10 {
		if _, err := pool.Exec(ctx, terminateSQL, name); err != nil {
			return fmt.Errorf("testutil/pg: terminate template connections: %w", err)
		}
		var count int
		if err := pool.QueryRow(ctx, checkSQL, name).Scan(&count); err != nil {
			return fmt.Errorf("testutil/pg: count template connections: %w", err)
		}
		if count == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("testutil/pg: disconnect template: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	return fmt.Errorf("testutil/pg: template database %q has lingering connections", name)
}

func lingerReaper() error {
	if os.Getenv(ryukEnvShort) != "" || os.Getenv(ryukEnvLong) != "" {
		return nil
	}
	if err := os.Setenv(ryukEnvLong, ryukLinger.String()); err != nil {
		return fmt.Errorf("testutil/pg: set reaper reconnection timeout: %w", err)
	}
	return nil
}

// Clone creates a database cloned from the migrated template and returns a
// connected [*pgxpool.Pool]. The pool and the database go with t.Cleanup.
func Clone(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := cloneDatabase(t)
	ctx, cancel := context.WithTimeout(t.Context(), opTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("testutil/pg: open cloned pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// CloneDSN creates a database cloned from the migrated template and returns
// its connection string, for callers that open their own connection. The
// database goes with t.Cleanup.
func CloneDSN(t *testing.T) string {
	t.Helper()
	cfg := cloneDatabase(t)
	// Config.ConnString keeps the parsed template DSN, so the path is set here.
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatalf("testutil/pg: parse template dsn: %v", err)
	}
	u.Path = "/" + cfg.ConnConfig.Database
	return u.String()
}

func cloneDatabase(t *testing.T) *pgxpool.Config {
	t.Helper()
	if testing.Short() {
		t.Skip("testutil/pg: container-backed; skipped under -short")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	shared.mu.Lock()
	defer shared.mu.Unlock()

	if err := checkSharedReady(); err != nil {
		t.Fatalf("%v", err)
	}
	dbName := fmt.Sprintf("t%d_%d", os.Getpid(), shared.serial.Add(1))
	quotedDB := pgx.Identifier{dbName}.Sanitize()
	quotedTpl := pgx.Identifier{shared.templateName}.Sanitize()

	ctx, cancel := context.WithTimeout(t.Context(), opTimeout)
	defer cancel()
	createSQL := "CREATE DATABASE " + quotedDB + " TEMPLATE " + quotedTpl
	if _, err := shared.admin.Exec(ctx, createSQL); err != nil {
		t.Fatalf("testutil/pg: create clone database: %v", err)
	}
	t.Cleanup(func() { dropDatabase(t, dbName) })

	cfg := shared.cfg.Copy()
	cfg.ConnConfig.Database = dbName
	return cfg
}

func dropDatabase(t *testing.T, dbName string) {
	t.Helper()
	quoted := pgx.Identifier{dbName}.Sanitize()
	shared.mu.Lock()
	defer shared.mu.Unlock()
	// The bound starts once the lock is held: parallel cleanups queue on it under load.
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	if shared.admin != nil {
		if _, err := shared.admin.Exec(ctx, "DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)"); err != nil {
			t.Logf("testutil/pg: drop clone database %s: %v", dbName, err)
		}
	}
}

func checkSharedReady() error {
	return checkSharedReadyState(shared.admin, shared.err)
}

func checkSharedReadyState(admin *pgxpool.Pool, err error) error {
	if err != nil {
		return fmt.Errorf("testutil/pg: shared postgres failed to start: %w", err)
	}
	if admin == nil {
		return errors.New("testutil/pg: shared postgres unavailable (TestMain must call pg.Main)")
	}
	return nil
}
