// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package sqlc holds runtime helpers that complement sqlc-generated code:
// transaction wrappers, pgx-error → golusoris-error mapping, and a shared
// sqlc.yaml fragment template lives in tools/sqlc.yaml.fragment.
//
// Apps generate their typed query layer with sqlc and inject the generated
// *Queries type via fx. This package fills the gaps sqlc doesn't.
package sqlc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	gerr "github.com/golusoris/golusoris/core/errors"
)

const rollbackTimeout = 5 * time.Second

type txBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// TxFn is the function signature used with [WithTx].
type TxFn func(ctx context.Context, tx pgx.Tx) error

// WithTx runs fn inside a Postgres transaction. The tx is committed if fn
// returns nil; otherwise it's rolled back. fn errors flow through so callers
// can use [errors.Is] / [errors.As] on the original cause; if the rollback
// itself fails (other than [pgx.ErrTxClosed]) the returned error is
// [errors.Join] of the fn error and the rollback error, so compare with
// [errors.Is], not ==.
//
//	err := sqlc.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
//	    return queries.WithTx(tx).InsertOrder(ctx, args)
//	})
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn TxFn) error {
	if pool == nil {
		return errors.New("db/sqlc: pool is required")
	}
	return withTx(ctx, pool, fn)
}

func withTx(ctx context.Context, beginner txBeginner, fn TxFn) (retErr error) {
	if ctx == nil {
		return errors.New("db/sqlc: context is required")
	}
	if fn == nil {
		return errors.New("db/sqlc: transaction function is required")
	}

	tx, err := beginner.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("db/sqlc: begin tx: %w", err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		if rbErr := tx.Rollback(cleanupCtx); rbErr != nil &&
			!errors.Is(rbErr, pgx.ErrTxClosed) && retErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("db/sqlc: rollback: %w", rbErr))
		}
	}()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db/sqlc: commit: %w", err)
	}
	committed = true
	return nil
}

// MapError converts common pgx errors to golusoris error codes:
//   - pgx.ErrNoRows                  → CodeNotFound
//   - PostgreSQL 23505 (unique_violation), 23P01 (exclusion_violation)
//     → CodeConflict
//   - 40001 (serialization_failure), 40P01 (deadlock_detected)
//     → CodeUnavailable
//   - 23503 (foreign_key_violation), 23502 (not_null_violation)
//     → CodeBadRequest
//
// All other errors pass through unchanged.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return gerr.Wrap(err, gerr.CodeNotFound, "not found")
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "23505", "23P01":
			return gerr.Wrap(err, gerr.CodeConflict, "constraint violation")
		case "40001", "40P01":
			return gerr.Wrap(err, gerr.CodeUnavailable, "transient db error, retry")
		case "23503", "23502":
			return gerr.Wrap(err, gerr.CodeBadRequest, "invalid reference")
		}
	}
	return err
}
