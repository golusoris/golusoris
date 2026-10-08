// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	dbsqlite "github.com/golusoris/golusoris/db/sqlite"
	"github.com/golusoris/golusoris/jobs"
	"github.com/golusoris/golusoris/jobs/sqlite"
)

type encodeArgs struct {
	Asset string `json:"asset"`
}

func (encodeArgs) Kind() string { return "sqlite-encode" }

type encodeWorker struct {
	jobs.WorkerDefaults[encodeArgs]
	done chan string
}

func (w *encodeWorker) Work(_ context.Context, job *jobs.Job[encodeArgs]) error {
	w.done <- job.Args.Asset
	return nil
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func openDB(t *testing.T, maxConns int) *sql.DB {
	t.Helper()
	db, err := dbsqlite.Open(context.Background(), dbsqlite.Options{
		Path:         filepath.Join(t.TempDir(), "jobs.db"),
		MaxOpenConns: maxConns,
	}, discard())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func waitAsset(t *testing.T, done <-chan string, want string) {
	t.Helper()
	select {
	case got := <-done:
		if got != want {
			t.Fatalf("worked asset = %q, want %q", got, want)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("job %q was never worked", want)
	}
}

// TestStandalone_insertWorkDrain is the single-binary path: SQLite file,
// migrations, insert through jobs.Inserter, work, depth read, drain.
func TestStandalone_insertWorkDrain(t *testing.T) {
	t.Parallel()
	db := openDB(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate (idempotent): %v", err)
	}
	w := &encodeWorker{done: make(chan string, 1)}
	workers := jobs.NewWorkers()
	if err := jobs.Register(workers, w); err != nil {
		t.Fatalf("Register: %v", err)
	}
	opts := jobs.DefaultOptions()
	opts.FetchPollInterval = 100 * time.Millisecond
	client, err := sqlite.New(db, opts, workers, discard())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var ins jobs.Inserter = client
	if _, err := ins.Insert(ctx, encodeArgs{Asset: "a.mkv"}, nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	waitAsset(t, w.done, "a.mkv")
	if err := jobs.Drain(ctx, client, jobs.StopOptions{Soft: 5 * time.Second, Hard: 5 * time.Second}, discard()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
}

func TestDepthQuerier_groupsByQueueStateTenant(t *testing.T) {
	t.Parallel()
	db := openDB(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := sqlite.NewDepthQuerier(db)
	if err != nil {
		t.Fatalf("NewDepthQuerier: %v", err)
	}
	empty, err := q.QueryDepth(ctx, "tenant")
	if err != nil || len(empty) != 0 {
		t.Fatalf("QueryDepth(empty) = %v, %v; want no rows", empty, err)
	}

	producer, err := sqlite.New(db, jobs.Options{Enabled: true}, nil, discard())
	if err != nil {
		t.Fatalf("New producer: %v", err)
	}
	for _, tenant := range []string{"acme", "acme", "globex"} {
		meta, merr := json.Marshal(map[string]string{"tenant": tenant})
		if merr != nil {
			t.Fatal(merr)
		}
		if _, ierr := producer.Insert(ctx, encodeArgs{Asset: tenant}, &jobs.InsertOpts{Queue: "transcode", Metadata: meta}); ierr != nil {
			t.Fatalf("Insert: %v", ierr)
		}
	}
	rows, err := q.QueryDepth(ctx, "tenant")
	if err != nil {
		t.Fatalf("QueryDepth: %v", err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Queue+"/"+r.State+"/"+r.Tenant] = r.Count
		if r.OldestAge < 0 || r.OldestAge > time.Minute {
			t.Errorf("OldestAge = %v, want fresh non-negative age", r.OldestAge)
		}
	}
	if got["transcode/available/acme"] != 2 || got["transcode/available/globex"] != 1 || len(got) != 2 {
		t.Fatalf("depth rows = %v, want acme=2 globex=1", got)
	}

	untenanted, err := q.QueryDepth(ctx, "")
	if err != nil || len(untenanted) != 1 || untenanted[0].Tenant != "" || untenanted[0].Count != 3 {
		t.Fatalf("QueryDepth(no tenant key) = %+v, %v; want one group of 3", untenanted, err)
	}
}

// TestModule_fxLifecycle wires db/sqlite's *sql.DB into jobs/sqlite.Module
// plus jobs.MetricsModule — the standalone fx graph — and drains on Stop.
func TestModule_fxLifecycle(t *testing.T) {
	t.Parallel()
	db := openDB(t, 4) // db/sqlite default pool: WAL + busy_timeout must keep River working
	cfg, err := config.New(config.Options{EnvPrefix: "JOBSSQLITE_TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	w := &encodeWorker{done: make(chan string, 1)}
	var (
		ins       jobs.Inserter
		collector *jobs.DepthCollector
	)
	app := fxtest.New(t,
		fx.Supply(db, cfg, discard(), prometheus.NewRegistry()),
		sqlite.Module,
		jobs.MetricsModule,
		fx.Invoke(func(ws *jobs.Workers) error { return jobs.Register(ws, w) }),
		fx.Populate(&ins, &collector),
	)
	app.RequireStart()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ins.Insert(ctx, encodeArgs{Asset: "b.mkv"}, nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	waitAsset(t, w.done, "b.mkv")
	if depth, err := collector.QueueDepth(ctx, jobs.DefaultQueue); err != nil || depth < 0 {
		t.Fatalf("QueueDepth = %d, %v", depth, err)
	}
	app.RequireStop()
}

func TestRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := sqlite.New(nil, jobs.Options{}, nil, discard()); err == nil {
		t.Error("New accepted nil db")
	}
	if err := sqlite.Migrate(ctx, nil); err == nil {
		t.Error("Migrate accepted nil db")
	}
	if _, err := sqlite.NewDepthQuerier(nil); err == nil {
		t.Error("NewDepthQuerier accepted nil db")
	}
	db := openDB(t, 1)
	if _, err := sqlite.New(db, jobs.Options{}, nil, nil); err == nil {
		t.Error("New accepted nil logger")
	}
	q, err := sqlite.NewDepthQuerier(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.QueryDepth(ctx, `bad"key`); err == nil || !strings.Contains(err.Error(), "JSON path") {
		t.Errorf("QueryDepth(bad key) error = %v, want JSON path rejection", err)
	}
	if _, err := q.QueryDepth(ctx, "tenant"); err == nil {
		t.Error("QueryDepth succeeded before migrations created river_job")
	}
}
