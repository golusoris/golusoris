// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package timescale_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/golusoris/golusoris/db/timescale"
)

func TestNew_notNil(t *testing.T) {
	t.Parallel()
	db := timescale.New(nil)
	if db == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestNilPoolReturnsError(t *testing.T) {
	t.Parallel()
	db := timescale.New(nil)
	tests := map[string]func() error{
		"create hypertable":  func() error { return db.CreateHypertable(t.Context(), "metrics", "time") },
		"retention":          func() error { return db.SetRetention(t.Context(), "metrics", time.Hour) },
		"compression":        func() error { return db.EnableCompression(t.Context(), "metrics") },
		"compression policy": func() error { return db.AddCompressionPolicy(t.Context(), "metrics", time.Hour) },
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := run()
			if err == nil || !strings.Contains(err.Error(), "nil pool") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestPool_roundtrip(t *testing.T) {
	t.Parallel()
	db := timescale.New(nil)
	if db.Pool() != nil {
		t.Fatal("expected nil pool (passed nil)")
	}
}

func TestPoolNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var db *timescale.DB
	if pool := db.Pool(); pool != nil {
		t.Fatalf("Pool = %p, want nil", pool)
	}
}

func TestUninitializedPoolReturnsError(t *testing.T) {
	t.Parallel()

	db := timescale.New(new(pgxpool.Pool))
	err := db.CreateHypertable(t.Context(), "metrics", "time")
	if err == nil || !strings.Contains(err.Error(), "invalid pool") {
		t.Fatalf("CreateHypertable error = %v, want invalid pool", err)
	}
}

func TestPoliciesRejectInvalidDurationBeforeDatabaseAccess(t *testing.T) {
	t.Parallel()
	db := timescale.New(nil)
	if err := db.SetRetention(t.Context(), "metrics", 0); err == nil {
		t.Error("SetRetention accepted zero duration")
	}
	if err := db.AddCompressionPolicy(t.Context(), "metrics", -time.Second); err == nil {
		t.Error("AddCompressionPolicy accepted negative duration")
	}
}
