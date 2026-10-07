// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pgx_test

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/golusoris/golusoris/core/clock"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

const testTimeout = 30 * time.Second

// TestPasswordFileRotation proves a rotated password file applies to new
// connections without rebuilding the pool, and a stale one fails closed.
func TestPasswordFileRotation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	admin, appDSN := withAppRole(ctx, t, pgtest.DSN(t), "first")

	passwordFile := filepath.Join(t.TempDir(), "password")
	writeFile(t, passwordFile, "first\n")
	opts := testOptions(appDSN)
	opts.PasswordFile = passwordFile
	pool, err := dbpgx.New(ctx, opts, slog.New(slog.DiscardHandler), clock.NewFake())
	if err != nil {
		t.Fatalf("New with password file: %v", err)
	}
	t.Cleanup(pool.Close)

	exec(ctx, t, admin, `ALTER ROLE app PASSWORD 'second'`)
	writeFile(t, passwordFile, "second\n")
	pool.Reset()
	if err = pool.Ping(ctx); err != nil {
		t.Fatalf("ping after rotation: %v", err)
	}

	writeFile(t, passwordFile, "stale\n")
	pool.Reset()
	if err = pool.Ping(ctx); err == nil {
		t.Fatal("ping succeeded with a wrong password file")
	}
}

// TestReadPoolIsReadOnly proves db.read_dsn yields a pool whose sessions
// reject writes while reads succeed.
func TestReadPoolIsReadOnly(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dsn := pgtest.DSN(t)
	opts := testOptions(dsn)
	opts.ReadDSN = dsn

	primary, err := dbpgx.New(ctx, opts, slog.New(slog.DiscardHandler), clock.NewFake())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(primary.Close)
	exec(ctx, t, primary, `CREATE TABLE notes (body text)`)

	read, err := dbpgx.NewReadPool(ctx, opts, slog.New(slog.DiscardHandler), clock.NewFake())
	if err != nil {
		t.Fatalf("NewReadPool: %v", err)
	}
	t.Cleanup(read.Close)
	var n int
	if err = read.QueryRow(ctx, `SELECT count(*) FROM notes`).Scan(&n); err != nil {
		t.Fatalf("read through ReadPool: %v", err)
	}
	_, err = read.Exec(ctx, `INSERT INTO notes VALUES ('x')`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "25006" {
		t.Fatalf("write through ReadPool error = %v, want SQLSTATE 25006 read_only_sql_transaction", err)
	}
}

func testOptions(dsn string) dbpgx.Options {
	opts := dbpgx.DefaultOptions()
	opts.DSN = dsn
	opts.Retry.Attempts = 1
	return opts
}

// withAppRole creates role app with password and returns an admin pool plus
// an app DSN that carries no password.
func withAppRole(ctx context.Context, t *testing.T, dsn, password string) (*pgxpool.Pool, string) {
	t.Helper()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	t.Cleanup(admin.Close)
	exec(ctx, t, admin, `CREATE ROLE app LOGIN PASSWORD '`+password+`'`)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.User = url.User("app")
	return admin, u.String()
}

func exec(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	execCtx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	if _, err := pool.Exec(execCtx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
