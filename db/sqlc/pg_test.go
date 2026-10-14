// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sqlc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	gerr "github.com/golusoris/golusoris/core/errors"
	dbsqlc "github.com/golusoris/golusoris/db/sqlc"
	"github.com/golusoris/golusoris/testutil/pg"
)

func TestWithTx_RealPg(t *testing.T) {
	t.Parallel()

	pool := pg.Start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if _, err := pool.Exec(ctx, `
		CREATE TABLE test_tx (
			id int PRIMARY KEY,
			val text NOT NULL
		)
	`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	err := dbsqlc.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx, "INSERT INTO test_tx (id, val) VALUES ($1, $2)", 1, "alpha")
		return execErr
	})
	if err != nil {
		t.Fatalf("WithTx commit error: %v", err)
	}
	var val string
	if scanErr := pool.QueryRow(ctx, "SELECT val FROM test_tx WHERE id = $1", 1).Scan(&val); scanErr != nil || val != "alpha" {
		t.Fatalf("row not visible after commit: val=%q, err=%v", val, scanErr)
	}

	fnErr := errors.New("aborted")
	err = dbsqlc.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, execErr := tx.Exec(ctx, "INSERT INTO test_tx (id, val) VALUES ($1, $2)", 2, "beta"); execErr != nil {
			return execErr
		}
		return fnErr
	})
	if !errors.Is(err, fnErr) {
		t.Fatalf("WithTx error = %v, want %v", err, fnErr)
	}
	var count int
	if countErr := pool.QueryRow(ctx, "SELECT count(*) FROM test_tx WHERE id = $1", 2).Scan(&count); countErr != nil || count != 0 {
		t.Fatalf("row visible after rollback: count=%d, err=%v", count, countErr)
	}

	func() {
		defer func() {
			_ = recover()
		}()
		_ = dbsqlc.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			if _, execErr := tx.Exec(ctx, "INSERT INTO test_tx (id, val) VALUES ($1, $2)", 3, "gamma"); execErr != nil {
				return execErr
			}
			panic("something went wrong")
		})
	}()
	if panicErr := pool.QueryRow(ctx, "SELECT count(*) FROM test_tx WHERE id = $1", 3).Scan(&count); panicErr != nil || count != 0 {
		t.Fatalf("row visible after panic: count=%d, err=%v", count, panicErr)
	}
}

func TestMapError_RealPg(t *testing.T) {
	t.Parallel()

	pool := pg.Start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if _, err := pool.Exec(ctx, `
		CREATE TABLE test_parent (
			id int PRIMARY KEY
		);
		CREATE TABLE test_err (
			id int PRIMARY KEY,
			parent_id int REFERENCES test_parent(id),
			required text NOT NULL,
			checked int CHECK (checked > 0)
		);
	`); err != nil {
		t.Fatalf("create tables: %v", err)
	}

	if _, err := pool.Exec(ctx, "INSERT INTO test_parent (id) VALUES (1)"); err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	_, dupErr := pool.Exec(ctx, "INSERT INTO test_parent (id) VALUES (1)")
	if dupErr == nil {
		t.Fatal("expected unique violation error, got nil")
	}
	mappedDup := dbsqlc.MapError(dupErr)
	var ge *gerr.Error
	if !errors.As(mappedDup, &ge) || ge.Code != gerr.CodeConflict {
		t.Fatalf("unique violation mapped to %v, want CodeConflict", mappedDup)
	}

	_, fkErr := pool.Exec(ctx, "INSERT INTO test_err (id, parent_id, required, checked) VALUES (1, 999, 'req', 5)")
	if fkErr == nil {
		t.Fatal("expected foreign key violation error, got nil")
	}
	mappedFK := dbsqlc.MapError(fkErr)
	if !errors.As(mappedFK, &ge) || ge.Code != gerr.CodeBadRequest {
		t.Fatalf("fk violation mapped to %v, want CodeBadRequest", mappedFK)
	}

	_, nnErr := pool.Exec(ctx, "INSERT INTO test_err (id, parent_id, required, checked) VALUES (2, 1, NULL, 5)")
	if nnErr == nil {
		t.Fatal("expected not null violation error, got nil")
	}
	mappedNN := dbsqlc.MapError(nnErr)
	if !errors.As(mappedNN, &ge) || ge.Code != gerr.CodeBadRequest {
		t.Fatalf("not null violation mapped to %v, want CodeBadRequest", mappedNN)
	}

	_, chkErr := pool.Exec(ctx, "INSERT INTO test_err (id, parent_id, required, checked) VALUES (3, 1, 'req', -1)")
	if chkErr == nil {
		t.Fatal("expected check violation error, got nil")
	}
	mappedChk := dbsqlc.MapError(chkErr)
	if errors.As(mappedChk, &ge) {
		t.Fatalf("check violation unexpectedly mapped to *gerr.Error: %v", mappedChk)
	}
	if !errors.Is(mappedChk, chkErr) {
		t.Fatalf("check violation not passed through: got %v, want %v", mappedChk, chkErr)
	}

	genericErr := errors.New("generic non-pg error")
	mappedGen := dbsqlc.MapError(genericErr)
	if !errors.Is(mappedGen, genericErr) {
		t.Fatalf("non-pg error = %v, want %v", mappedGen, genericErr)
	}

	noRowsErr := pool.QueryRow(ctx, "SELECT id FROM test_parent WHERE id = 999").Scan(new(int))
	if !errors.Is(noRowsErr, pgx.ErrNoRows) {
		t.Fatalf("query row error = %v, want pgx.ErrNoRows", noRowsErr)
	}
	mappedNoRows := dbsqlc.MapError(noRowsErr)
	if !errors.As(mappedNoRows, &ge) || ge.Code != gerr.CodeNotFound {
		t.Fatalf("no rows mapped to %v, want CodeNotFound", mappedNoRows)
	}
}
