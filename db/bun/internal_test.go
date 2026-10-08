// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bun

import (
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx/fxtest"
)

func TestRegisterLifecycleClosesAdapter(t *testing.T) {
	t.Parallel()

	config, err := pgxpool.ParseConfig("postgres://user:pass@127.0.0.1:1/database?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	db, err := New(pool, Options{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lifecycle := fxtest.NewLifecycle(t)
	if err := registerLifecycle(lifecycle, db); err != nil {
		t.Fatalf("registerLifecycle: %v", err)
	}
	lifecycle.RequireStart()
	lifecycle.RequireStop()
	if err := db.PingContext(t.Context()); err == nil {
		t.Fatal("PingContext succeeded after lifecycle stop")
	}
}

func TestRegisterLifecycleRejectsNilDatabase(t *testing.T) {
	t.Parallel()

	err := registerLifecycle(fxtest.NewLifecycle(t), nil)
	if err == nil {
		t.Fatal("registerLifecycle accepted nil database")
	}
}
