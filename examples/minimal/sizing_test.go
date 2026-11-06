//go:build sizing

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/testutil/pg"
)

// Defaults mirror taskCPU and taskMemory in deploy/pulumi/app.go; pulumi-sizing-budget.sh fails on drift.
const (
	defaultMemoryBudgetMiB = 512
	defaultCPUBudget       = 0.25
)

const (
	defaultRPS      = 200
	defaultDuration = 30 * time.Second
	// maxRequests bounds the dispatch loop and keeps the float-to-int conversion in range.
	maxRequests = 100_000_000
	// loadWorkers caps in-flight requests (HISS-06).
	loadWorkers    = 32
	requestTimeout = 2 * time.Second
	buildTimeout   = 5 * time.Minute
	readyTimeout   = 60 * time.Second
	readyPoll      = 250 * time.Millisecond
	readyAttempts  = 240
	stopTimeout    = 20 * time.Second
	// maxStartAttempts bounds the fresh-port retries (HISS-02).
	maxStartAttempts = 3
	// minRateFraction is the share of the configured rps the load must reach for the run to count.
	minRateFraction = 0.9
	// clockTicks is USER_HZ, the unit of utime/stime in /proc/<pid>/stat on every Linux ABI.
	clockTicks = 100
	// probePath answers without a DB round trip.
	probePath = "/livez"
)

const errPostgresUnavailable = "sizing: Postgres unavailable; set SIZING_DB_DSN or provide a container runtime"

//nolint:paralleltest // WHY: CPU and RSS are measured per process; a parallel test would skew them.
func TestSizing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sizing reads /proc/<pid>/status and /proc/<pid>/stat; Linux only (HISS-21)")
	}
	cfg := loadSizingConfig(t)
	clk := realClock()
	bin := buildApp(t)
	withDatabase(t, func(t *testing.T, dsn string) {
		t.Helper()
		measure(t, clk, cfg, bin, dsn)
	})
}

func realClock() clock.Clock { return clockwork.NewRealClock() }

// withDatabase runs fn inside the subtest that owns the container, so the database lives while fn measures.
func withDatabase(t *testing.T, fn func(t *testing.T, dsn string)) {
	t.Helper()
	dsn := os.Getenv("SIZING_DB_DSN")
	if dsn != "" {
		fn(t, dsn)
		return
	}
	t.Run("postgres", func(t *testing.T) {
		dsn = pg.Start(t).Config().ConnString()
		fn(t, dsn)
	})
	if dsn == "" {
		t.Fatal(errPostgresUnavailable)
	}
}

func measure(t *testing.T, clk clock.Clock, cfg sizingConfig, bin, dsn string) {
	t.Helper()
	app := startApp(t, clk, bin, dsn)
	pid := app.cmd.Process.Pid
	before := readProc(t, pid)
	res := runLoad(t, clk, "http://"+app.addr+probePath, cfg)
	after := readProc(t, pid)

	cores := avgCores(before, after, res.wall)
	rssMiB := float64(after.peakRSSKiB) / 1024
	t.Logf("sizing: peak RSS %.1f MiB (budget %.0f), avg CPU %.3f vCPU over %s (budget %.3f), %d requests at %.1f rps (configured %d), %d failed, GOARCH %s",
		rssMiB, cfg.memoryMiB, cores, res.wall.Round(time.Millisecond), cfg.cpuCores, res.sent, achievedRPS(res), cfg.rps, res.failed, runtime.GOARCH)

	if got := achievedRPS(res); got < minRateFraction*float64(cfg.rps) {
		t.Fatalf("sizing: achieved %.1f rps < %.0f%% of configured %d rps: the measurement is void; %s",
			got, minRateFraction*100, cfg.rps, res.errSummary())
	}
	for _, v := range budgetViolations(cfg, rssMiB, cores, res) {
		t.Error(v)
	}
}
