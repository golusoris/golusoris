// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pgxpoolstate_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/golusoris/golusoris/internal/pgxpoolstate"
)

func TestInitializedRejectsNilPool(t *testing.T) {
	t.Parallel()

	if pgxpoolstate.Initialized(nil) {
		t.Fatal("Initialized(nil) = true, want false")
	}
}

func TestInitializedRejectsZeroPool(t *testing.T) {
	t.Parallel()

	if pgxpoolstate.Initialized(new(pgxpool.Pool)) {
		t.Fatal("Initialized(zero pool) = true, want false")
	}
}

func TestInitializedAcceptsConstructedPool(t *testing.T) {
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
	if !pgxpoolstate.Initialized(pool) {
		t.Fatal("Initialized(constructed pool) = false, want true")
	}
}
