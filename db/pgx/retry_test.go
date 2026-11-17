// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pgx_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/log"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
)

func retryOptions() dbpgx.Options {
	return dbpgx.Options{
		ConnectTimeout: time.Second,
		Retry:          dbpgx.RetryOptions{Attempts: 3, Initial: time.Millisecond, Max: 2 * time.Millisecond},
	}
}

func TestRetrySucceedsAfterFailures(t *testing.T) {
	t.Parallel()
	calls := 0
	opts := retryOptions()
	clk := clockwork.NewRealClock()
	var attemptDone <-chan struct{}
	err := dbpgx.Retry(t.Context(), opts, log.New(log.Options{}), clk, func(ctx context.Context) error {
		calls++
		attemptDone = ctx.Done()
		if deadline, ok := ctx.Deadline(); !ok || deadline.After(clk.Now().Add(opts.ConnectTimeout)) {
			t.Error("attempt timeout missing or exceeds connect timeout")
		}
		if calls < 3 {
			return errors.New("database starting")
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	select {
	case <-attemptDone:
	default:
		t.Error("attempt context not canceled")
	}
}

func TestRetryExhausted(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("database unavailable")
	calls := 0
	err := dbpgx.Retry(t.Context(), retryOptions(), log.New(log.Options{}), clockwork.NewRealClock(), func(context.Context) error {
		calls++
		return sentinel
	})
	require.ErrorContains(t, err, "connect failed after 3 attempts")
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 3, calls)
}

func TestRetrySingleAttemptNeverWaits(t *testing.T) {
	t.Parallel()
	opts := retryOptions()
	opts.Retry.Attempts = 1
	calls := 0
	sentinel := errors.New("database unavailable")
	err := dbpgx.Retry(t.Context(), opts, log.New(log.Options{}), clock.NewFake(), func(context.Context) error {
		calls++
		return sentinel
	})
	require.ErrorContains(t, err, "connect failed after 1 attempts")
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 1, calls)
}

func TestRetryCanceledDuringBackoff(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	clk := clock.NewFake()
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		result <- dbpgx.Retry(ctx, retryOptions(), log.New(log.Options{}), clk, func(context.Context) error {
			return errors.New("database starting")
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	cancel()
	err := <-result
	require.ErrorContains(t, err, "connect canceled")
	require.ErrorIs(t, err, context.Canceled)
}

func TestRetryDefaultsWithoutDSN(t *testing.T) {
	t.Parallel()
	calls := 0
	err := dbpgx.Retry(t.Context(), dbpgx.Options{}, log.New(log.Options{}), clock.NewFake(), func(context.Context) error {
		calls++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestRetryRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*dbpgx.Options)
	}{
		{"connect timeout must be positive", func(o *dbpgx.Options) { o.ConnectTimeout = -time.Second }},
		{"retry attempts must be positive", func(o *dbpgx.Options) { o.Retry.Attempts = -1 }},
		{"retry delays must be positive", func(o *dbpgx.Options) { o.Retry.Initial = -time.Second }},
		{"retry delays must be positive", func(o *dbpgx.Options) { o.Retry.Max = -time.Second }},
		{"initial retry delay exceeds maximum", func(o *dbpgx.Options) { o.Retry.Initial = time.Second }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := retryOptions()
			tt.mutate(&opts)
			err := dbpgx.Retry(t.Context(), opts, log.New(log.Options{}), clock.NewFake(), func(context.Context) error {
				t.Error("invalid options reached connect")
				return nil
			})
			require.ErrorContains(t, err, tt.name)
		})
	}
}

func TestRetryRejectsNilDependencies(t *testing.T) {
	t.Parallel()
	var typedNilClock *clockwork.FakeClock
	tests := []struct {
		name    string
		want    string
		logger  *slog.Logger
		clk     clock.Clock
		connect func(context.Context) error
	}{
		{"logger", "nil logger", nil, clock.NewFake(), func(context.Context) error { return nil }},
		{"clock", "nil clock", log.New(log.Options{}), nil, func(context.Context) error { return nil }},
		{"typed nil clock", "nil clock", log.New(log.Options{}), typedNilClock, func(context.Context) error { return nil }},
		{"connect", "nil connect", log.New(log.Options{}), clock.NewFake(), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := dbpgx.Retry(t.Context(), retryOptions(), tt.logger, tt.clk, tt.connect)
			require.ErrorContains(t, err, tt.want)
		})
	}
}
