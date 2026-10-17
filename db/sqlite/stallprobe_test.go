// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build stallprobe

package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/db/sqlite"
)

const (
	defaultProbeIterations = 300
	maxProbeIterations     = 10000
	defaultProbeWorkers    = 1
	maxProbeWorkers        = 64
	probeContextTimeout    = 60 * time.Second
	openStallThreshold     = 5 * time.Second
)

type probeSample struct {
	conn     time.Duration
	pragma   time.Duration
	ping     time.Duration
	write    time.Duration
	closeDur time.Duration
}

func TestStallProbe(t *testing.T) {
	t.Parallel()
	iterations := envBoundedInt(t, "STALLPROBE_N", defaultProbeIterations, maxProbeIterations)
	workers := envBoundedInt(t, "STALLPROBE_P", defaultProbeWorkers, maxProbeWorkers)
	clk := clockwork.NewRealClock()
	rootDir := probeRootDir(t, clk)

	samples, errs := runProbeWorkers(t.Context(), clk, rootDir, iterations, workers)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}

	reportProbeResults(t, samples, openStallThreshold)
}

func envBoundedInt(t *testing.T, key string, defVal, maxVal int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defVal
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 || v > maxVal {
		t.Fatalf("%s=%q: want integer in 1..%d", key, raw, maxVal)
	}
	return v
}

func probeRootDir(t *testing.T, clk clockwork.Clock) string {
	t.Helper()
	base := strings.TrimSpace(os.Getenv("STALLPROBE_DIR"))
	if base == "" {
		return t.TempDir()
	}
	dir := filepath.Join(base, "stallprobe-"+strconv.FormatInt(clk.Now().UnixNano(), 10))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			t.Logf("cleanup %s: %v", dir, rmErr)
		}
	})
	return dir
}

func runProbeWorkers(
	ctx context.Context,
	clk clockwork.Clock,
	rootDir string,
	iterations, workers int,
) ([]probeSample, []error) {
	samples := make([]probeSample, iterations)
	errs := make([]error, iterations)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for w := range workers {
		sem <- struct{}{}
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			defer func() { <-sem }()
			for i := workerID; i < iterations; i += workers {
				samples[i], errs[i] = runProbeIteration(ctx, clk, rootDir, i)
			}
		}(w)
	}
	wg.Wait()
	return samples, errs
}

func runProbeIteration(
	parentCtx context.Context,
	clk clockwork.Clock,
	rootDir string,
	index int,
) (probeSample, error) {
	iterDir := filepath.Join(rootDir, strconv.Itoa(index))
	if err := os.MkdirAll(iterDir, 0o700); err != nil {
		return probeSample{}, fmt.Errorf("mkdir %s: %w", iterDir, err)
	}
	dbPath := filepath.Join(iterDir, "probe.db")
	ctx, cancel := context.WithTimeout(parentCtx, probeContextTimeout)
	defer cancel()

	dsn := (sqlite.Options{Path: dbPath}).DSN()
	db, err := sql.Open(sqlite.DriverName, dsn)
	if err != nil {
		return probeSample{}, fmt.Errorf("open %s: %w", dbPath, err)
	}

	var sample probeSample
	tConn := clk.Now()
	conn, err := db.Conn(ctx)
	sample.conn = clk.Since(tConn)
	if err != nil {
		return sample, errors.Join(fmt.Errorf("conn %s: %w", dbPath, err), db.Close())
	}

	phaseErr := runConnPhases(ctx, clk, conn, &sample)
	tClose := clk.Now()
	connCloseErr := conn.Close()
	dbCloseErr := db.Close()
	sample.closeDur = clk.Since(tClose)

	return sample, errors.Join(phaseErr, connCloseErr, dbCloseErr)
}

func runConnPhases(
	ctx context.Context,
	clk clockwork.Clock,
	conn *sql.Conn,
	sample *probeSample,
) error {
	tPragma := clk.Now()
	pragmaErr := verifyPragmas(ctx, conn)
	sample.pragma = clk.Since(tPragma)
	if pragmaErr != nil {
		return pragmaErr
	}

	tPing := clk.Now()
	pingErr := conn.PingContext(ctx)
	sample.ping = clk.Since(tPing)
	if pingErr != nil {
		return fmt.Errorf("ping: %w", pingErr)
	}

	tWrite := clk.Now()
	writeErr := runFirstWrite(ctx, conn)
	sample.write = clk.Since(tWrite)
	return writeErr
}

func verifyPragmas(ctx context.Context, conn *sql.Conn) error {
	var mode string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("pragma journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("journal_mode = %q, want wal", mode)
	}
	var busyTimeout int
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		return fmt.Errorf("pragma busy_timeout: %w", err)
	}
	if busyTimeout != int(sqlite.DefaultOptions().BusyTimeout.Milliseconds()) {
		return fmt.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}
	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("pragma foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}
	return nil
}

func runFirstWrite(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "CREATE TABLE probe (id INTEGER PRIMARY KEY, val TEXT NOT NULL)"); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO probe (val) VALUES ('x')"); err != nil {
		return fmt.Errorf("insert row: %w", err)
	}
	return nil
}

func reportProbeResults(t *testing.T, samples []probeSample, threshold time.Duration) {
	t.Helper()
	phases := []struct {
		name string
		pick func(probeSample) time.Duration
	}{
		{name: "conn", pick: func(s probeSample) time.Duration { return s.conn }},
		{name: "pragma", pick: func(s probeSample) time.Duration { return s.pragma }},
		{name: "ping", pick: func(s probeSample) time.Duration { return s.ping }},
		{name: "write", pick: func(s probeSample) time.Duration { return s.write }},
		{name: "close", pick: func(s probeSample) time.Duration { return s.closeDur }},
	}

	vals := make([]time.Duration, len(samples))
	for _, p := range phases {
		over := 0
		for i, s := range samples {
			d := p.pick(s)
			vals[i] = d
			if d > threshold {
				over++
			}
		}
		slices.Sort(vals)
		p50 := vals[(len(vals)-1)*50/100]
		p99 := vals[(len(vals)-1)*99/100]
		maxVal := vals[len(vals)-1]
		t.Logf("STALLPROBE phase=%s n=%d p50=%s p99=%s max=%s over5s=%d", p.name, len(vals), p50, p99, maxVal, over)
	}

	totalOver := 0
	for _, s := range samples {
		if s.conn+s.ping > threshold {
			totalOver++
		}
	}
	t.Logf("STALLPROBE total over5s=%d", totalOver)
	if totalOver > 0 {
		t.Errorf("db/sqlite stallprobe: %d/%d opens (conn+ping) exceeded %s", totalOver, len(samples), threshold)
	}
}
