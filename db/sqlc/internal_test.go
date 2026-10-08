// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sqlc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type stubBeginner struct {
	tx  pgx.Tx
	err error
}

func (s stubBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return s.tx, s.err
}

type stubTx struct {
	pgx.Tx
	commitErr           error
	rollbackErr         error
	commits             int
	rollbacks           int
	rollbackErrAtCall   error
	rollbackDeadline    time.Time
	rollbackHasDeadline bool
}

func (s *stubTx) Commit(context.Context) error {
	s.commits++
	return s.commitErr
}

func (s *stubTx) Rollback(ctx context.Context) error {
	s.rollbacks++
	s.rollbackErrAtCall = ctx.Err()
	s.rollbackDeadline, s.rollbackHasDeadline = ctx.Deadline()
	return s.rollbackErr
}

func TestWithTxRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	if err := WithTx(context.Background(), nil, func(context.Context, pgx.Tx) error { return nil }); err == nil {
		t.Fatal("WithTx() error = nil, want missing pool error")
	}
	tx := &stubTx{}
	//nolint:staticcheck // WHY: boundary regression deliberately supplies an invalid nil context.
	if err := withTx(nil, stubBeginner{tx: tx}, func(context.Context, pgx.Tx) error { return nil }); err == nil {
		t.Fatal("withTx() error = nil, want missing context error")
	}
	if err := withTx(context.Background(), stubBeginner{tx: tx}, nil); err == nil {
		t.Fatal("withTx() error = nil, want missing function error")
	}
	if tx.commits != 0 || tx.rollbacks != 0 {
		t.Fatalf("invalid input touched transaction: commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
}

func TestWithTxCommitsSuccessfulCallback(t *testing.T) {
	t.Parallel()

	tx := &stubTx{}
	err := withTx(context.Background(), stubBeginner{tx: tx}, func(context.Context, pgx.Tx) error {
		return nil
	})
	if err != nil {
		t.Fatalf("withTx() error = %v", err)
	}
	if tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("commits=%d rollbacks=%d, want 1/0", tx.commits, tx.rollbacks)
	}
}

func TestWithTxRollsBackCallbackErrorAfterCancellation(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("callback failed")
	rollbackErr := errors.New("rollback failed")
	tx := &stubTx{rollbackErr: rollbackErr}
	ctx, cancel := context.WithCancel(context.Background())
	err := withTx(ctx, stubBeginner{tx: tx}, func(context.Context, pgx.Tx) error {
		cancel()
		return wantErr
	})
	if !errors.Is(err, wantErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("withTx() error = %v, want joined callback and rollback errors", err)
	}
	if tx.rollbacks != 1 {
		t.Fatalf("rollbacks=%d, want 1", tx.rollbacks)
	}
	if tx.rollbackErrAtCall != nil {
		t.Fatalf("rollback context error = %v, want uncancelled context", tx.rollbackErrAtCall)
	}
	if !tx.rollbackHasDeadline || time.Until(tx.rollbackDeadline) > rollbackTimeout {
		t.Fatalf("rollback deadline = %v, ok=%v", tx.rollbackDeadline, tx.rollbackHasDeadline)
	}
}

func TestWithTxRollsBackCommitFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("commit failed")
	tx := &stubTx{commitErr: wantErr}
	err := withTx(context.Background(), stubBeginner{tx: tx}, func(context.Context, pgx.Tx) error {
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("withTx() error = %v, want commit error", err)
	}
	if tx.commits != 1 || tx.rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d, want 1/1", tx.commits, tx.rollbacks)
	}
}

func TestWithTxRollsBackPanicAndPreservesValue(t *testing.T) {
	t.Parallel()

	tx := &stubTx{}
	wantPanic := &struct{}{}
	var gotPanic any
	func() {
		defer func() { gotPanic = recover() }()
		_ = withTx(context.Background(), stubBeginner{tx: tx}, func(context.Context, pgx.Tx) error {
			panic(wantPanic)
		})
	}()
	if gotPanic != wantPanic {
		t.Fatalf("panic = %v, want original value", gotPanic)
	}
	if tx.rollbacks != 1 {
		t.Fatalf("rollbacks=%d, want 1", tx.rollbacks)
	}
}
