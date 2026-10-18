// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/store/storetest at 6111de5a.

package pg_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"

	"github.com/golusoris/golusoris/testutil/pg"
)

const scenarioEnv = "TESTUTIL_PG_SCENARIO"

func TestMain(m *testing.M) {
	if scenario := os.Getenv(scenarioEnv); scenario != "" {
		runChildScenarioMode(m, scenario)
		return
	}
	os.Exit(pg.Main(m, migrateTemplateForTest))
}

func runChildScenarioMode(m *testing.M, scenario string) {
	switch scenario {
	case "migrate-fails":
		code := pg.Main(m, func(context.Context, string) error {
			return errors.New("boom migration failed")
		})
		os.Exit(code)
	case "no-main":
		os.Exit(m.Run())
	default:
		os.Exit(1)
	}
}

func migrateTemplateForTest(ctx context.Context, dsn string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect template: %w", err)
	}
	defer pool.Close()
	const ddl = `
		CREATE TABLE test_items (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			val INT NOT NULL DEFAULT 0
		);
		INSERT INTO test_items (name, val) VALUES ('initial', 100);
	`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("seed template: %w", err)
	}
	return nil
}

func runChildScenario(t *testing.T, scenario, runTest string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	//nolint:gosec // G204: re-executes current test binary for negative scenario verification
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+runTest+"$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), scenarioEnv+"="+scenario)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected child %s to exit with error, but exited 0\nOutput:\n%s", scenario, out)
	}
	return string(out)
}

//nolint:paralleltest // child scenario executed in subprocess
func TestSubprocessCallClone(t *testing.T) {
	if os.Getenv(scenarioEnv) == "" {
		t.Skip("runs only as child scenario")
	}
	_ = pg.Clone(t)
}

func TestMainLingersReaper(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Main sets the reaper timeout only when it starts the container")
	}
	if got := testcontainers.ReadConfig().Config.RyukReconnectionTimeout; got < time.Minute {
		t.Errorf("reaper reconnection timeout = %s, want at least 1m", got)
	}
	pool := pg.Clone(t)
	var one int
	if err := pool.QueryRow(t.Context(), "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Errorf("SELECT 1 = %d, %v", one, err)
	}
}

func TestClone_Isolation(t *testing.T) {
	t.Parallel()
	poolA := pg.Clone(t)
	poolB := pg.Clone(t)

	// Write in clone A
	_, err := poolA.Exec(t.Context(), "INSERT INTO test_items (name, val) VALUES ('item-A', 1)")
	if err != nil {
		t.Fatalf("insert clone A: %v", err)
	}

	// Verify absent in clone B
	var countB int
	err = poolB.QueryRow(t.Context(), "SELECT count(*) FROM test_items WHERE name = 'item-A'").Scan(&countB)
	if err != nil {
		t.Fatalf("query clone B: %v", err)
	}
	if countB != 0 {
		t.Errorf("clone B saw item-A from clone A; got count %d, want 0", countB)
	}

	// Write in clone B
	_, err = poolB.Exec(t.Context(), "INSERT INTO test_items (name, val) VALUES ('item-B', 2)")
	if err != nil {
		t.Fatalf("insert clone B: %v", err)
	}

	// Verify absent in clone A
	var countA int
	err = poolA.QueryRow(t.Context(), "SELECT count(*) FROM test_items WHERE name = 'item-B'").Scan(&countA)
	if err != nil {
		t.Fatalf("query clone A: %v", err)
	}
	if countA != 0 {
		t.Errorf("clone A saw item-B from clone B; got count %d, want 0", countA)
	}

	// Update in clone A does not affect clone B
	_, err = poolA.Exec(t.Context(), "UPDATE test_items SET val = 999 WHERE name = 'initial'")
	if err != nil {
		t.Fatalf("update clone A: %v", err)
	}
	var valB int
	err = poolB.QueryRow(t.Context(), "SELECT val FROM test_items WHERE name = 'initial'").Scan(&valB)
	if err != nil {
		t.Fatalf("query val clone B: %v", err)
	}
	if valB != 100 {
		t.Errorf("clone B initial val modified by clone A; got %d, want 100", valB)
	}
}

func TestClone_MigrationApplied(t *testing.T) {
	t.Parallel()
	pool := pg.Clone(t)
	var count int
	err := pool.QueryRow(t.Context(), "SELECT count(*) FROM test_items WHERE name = 'initial'").Scan(&count)
	if err != nil {
		t.Fatalf("query test_items: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 initial item from template migration, got %d", count)
	}
}

func TestCloneDSN(t *testing.T) {
	t.Parallel()
	dsn := pg.CloneDSN(t)
	if dsn == "" {
		t.Fatal("empty DSN returned by CloneDSN")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect via CloneDSN: %v", err)
	}
	defer pool.Close()

	var db string
	if err := pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&db); err != nil {
		t.Fatalf("current_database via CloneDSN: %v", err)
	}
	if db == "template" || db == "postgres" {
		t.Fatalf("CloneDSN connected to %q, want the clone", db)
	}

	var val int
	if err := pool.QueryRow(t.Context(), "SELECT val FROM test_items WHERE name = 'initial'").Scan(&val); err != nil {
		t.Fatalf("query via CloneDSN: %v", err)
	}
	if val != 100 {
		t.Errorf("CloneDSN initial val = %d, want 100", val)
	}
}

func TestNegative_MigrateErrorSurfaces(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping subprocess test under -short")
	}
	out := runChildScenario(t, "migrate-fails", "TestSubprocessCallClone")
	if !strings.Contains(out, "boom migration failed") {
		t.Fatalf("expected 'boom migration failed' in output, got:\n%s", out)
	}
}

func TestNegative_CloneWithoutMain(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping subprocess test under -short")
	}
	out := runChildScenario(t, "no-main", "TestSubprocessCallClone")
	if !strings.Contains(out, "TestMain must call pg.Main") {
		t.Fatalf("expected 'TestMain must call pg.Main' in output, got:\n%s", out)
	}
}

func TestBoundary_SequentialClones(t *testing.T) {
	t.Parallel()
	const cloneCount = 20
	for i := range cloneCount {
		pool := pg.Clone(t)
		var n int
		if err := pool.QueryRow(t.Context(), "SELECT 1").Scan(&n); err != nil || n != 1 {
			t.Fatalf("clone %d: SELECT 1 = %d, err = %v", i, n, err)
		}
	}
}
