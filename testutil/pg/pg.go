// Package pg starts a real PostgreSQL container via testcontainers-go and
// returns a connected [*pgxpool.Pool]. Tests using this helper need Docker
// reachable on the host; CI runners (ubuntu-latest) ship Docker by default.
//
// Usage:
//
//	func TestQuery(t *testing.T) {
//	    pool := pg.Start(t)
//	    // ... run tests against pool ...
//	}
//
// The container is terminated automatically via t.Cleanup. Each call gets a
// fresh container, so tests are isolated. For suites needing many tests
// against the same instance, share a pool via TestMain or a sync.Once helper.
package pg

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Defaults for the spawned container — kept small + deterministic.
const (
	defaultImage    = "postgres:17-alpine"
	defaultDB       = "test"
	defaultUser     = "test"
	defaultPassword = "test"
	startTimeout    = 60 * time.Second
)

// Options tweak the container. Zero value uses defaults.
type Options struct {
	// Image is the postgres image tag. Default "postgres:17-alpine".
	Image string
	// Database is the initial database name. Default "test".
	Database string
	// User is the superuser. Default "test".
	User string
	// Password is the superuser password. Default "test".
	Password string
	// Customizers are additional testcontainers options forwarded verbatim.
	Customizers []testcontainers.ContainerCustomizer
}

func (o Options) withDefaults() Options {
	if o.Image == "" {
		o.Image = defaultImage
	}
	if o.Database == "" {
		o.Database = defaultDB
	}
	if o.User == "" {
		o.User = defaultUser
	}
	if o.Password == "" {
		o.Password = defaultPassword
	}
	return o
}

// Start boots a Postgres container and returns a connected *pgxpool.Pool.
// The container + pool are torn down via t.Cleanup. Fatals (not skips) on
// failure — Docker is a hard requirement per the testutil/pg contract.
func Start(t *testing.T, opts ...Options) *pgxpool.Pool {
	t.Helper()
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	o = o.withDefaults()

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	customizers := append([]testcontainers.ContainerCustomizer{
		tcpostgres.WithDatabase(o.Database),
		tcpostgres.WithUsername(o.User),
		tcpostgres.WithPassword(o.Password),
		tcpostgres.BasicWaitStrategies(),
	}, o.Customizers...)

	container, err := tcpostgres.Run(ctx, o.Image, customizers...)
	if err != nil {
		t.Fatalf("testutil/pg: start container: %v", err)
	}
	t.Cleanup(func() {
		// Use background ctx — the test ctx may be cancelled already.
		if termErr := container.Terminate(context.Background()); termErr != nil {
			t.Logf("testutil/pg: terminate container: %v", termErr)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("testutil/pg: connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("testutil/pg: open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("testutil/pg: ping: %v", err)
	}
	return pool
}

// DSN starts a container and returns its DSN (sslmode=disable). Use this when
// you need the raw connection string (e.g. to drive db/migrate). The container
// is cleaned up via t.Cleanup.
func DSN(t *testing.T, opts ...Options) string {
	t.Helper()
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	o = o.withDefaults()

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	customizers := append([]testcontainers.ContainerCustomizer{
		tcpostgres.WithDatabase(o.Database),
		tcpostgres.WithUsername(o.User),
		tcpostgres.WithPassword(o.Password),
		tcpostgres.BasicWaitStrategies(),
	}, o.Customizers...)

	container, err := tcpostgres.Run(ctx, o.Image, customizers...)
	if err != nil {
		t.Fatalf("testutil/pg: start container: %v", err)
	}
	t.Cleanup(func() {
		if termErr := container.Terminate(context.Background()); termErr != nil {
			t.Logf("testutil/pg: terminate container: %v", termErr)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("testutil/pg: connection string: %v", err)
	}
	return dsn
}
