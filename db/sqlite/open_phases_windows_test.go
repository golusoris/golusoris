// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sqlite_test

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/golusoris/golusoris/db/sqlite"
)

type phase struct {
	name string
	run  func() error
}

type phaseTracker struct {
	ctx    context.Context
	path   string
	rawDB  *sql.DB
	conn   *sql.Conn
	fullDB *sql.DB
}

func (tr *phaseTracker) phases() []phase {
	return []phase{
		{"sql.Open", func() (err error) {
			tr.rawDB, err = sql.Open(sqlite.DriverName, "file:"+filepath.ToSlash(tr.path))
			return err
		}},
		{"db.Conn", func() (err error) {
			tr.conn, err = tr.rawDB.Conn(tr.ctx)
			return err
		}},
		{"PRAGMA busy_timeout = 30000", func() error {
			_, err := tr.conn.ExecContext(tr.ctx, "PRAGMA busy_timeout = 30000")
			return err
		}},
		{"PRAGMA journal_mode = WAL", func() error {
			_, err := tr.conn.ExecContext(tr.ctx, "PRAGMA journal_mode = WAL")
			return err
		}},
		{"PRAGMA foreign_keys = 1", func() error {
			_, err := tr.conn.ExecContext(tr.ctx, "PRAGMA foreign_keys = 1")
			return err
		}},
		{"conn.PingContext", func() error {
			return tr.conn.PingContext(tr.ctx)
		}},
		{"full open", func() (err error) {
			opts := sqlite.DefaultOptions()
			opts.Path = tr.path
			opts.BusyTimeout = 30 * time.Second
			tr.fullDB, err = sqlite.Open(tr.ctx, opts, slog.New(slog.DiscardHandler))
			return err
		}},
	}
}

func (tr *phaseTracker) close() {
	if tr.conn != nil {
		_ = tr.conn.Close()
	}
	if tr.rawDB != nil {
		_ = tr.rawDB.Close()
	}
	if tr.fullDB != nil {
		_ = tr.fullDB.Close()
	}
}

func TestOpenPhaseTimings(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("open-phase timing diagnoses a Windows-only flake (#778)")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	path := filepath.Join(t.TempDir(), "diagnostic.db")
	tracker := &phaseTracker{ctx: ctx, path: path}
	t.Cleanup(tracker.close)

	var slowestName string
	var maxDuration time.Duration
	for _, p := range tracker.phases() {
		start := time.Now()
		if err := p.run(); err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		duration := time.Since(start)
		t.Logf("%s: %v", p.name, duration)
		if duration > maxDuration {
			maxDuration = duration
			slowestName = p.name
		}
	}
	t.Logf("slowest phase: %s %v", slowestName, maxDuration)
}
