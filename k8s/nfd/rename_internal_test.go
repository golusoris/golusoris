// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nfd

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	errTransient = errors.New("transient")
	errPermanent = errors.New("permanent")
)

// fakeRenamer fails the first failures calls with err, then succeeds.
type fakeRenamer struct {
	failures int
	err      error
	calls    int
	sleeps   []time.Duration
}

func (f *fakeRenamer) renamer() renamer {
	return renamer{
		rename: func(_, _ string) error {
			f.calls++
			if f.calls <= f.failures {
				return f.err
			}
			return nil
		},
		transient: func(err error) bool { return errors.Is(err, errTransient) },
		sleep:     func(d time.Duration) { f.sleeps = append(f.sleeps, d) },
	}
}

func TestRenamerReplace_retriesTransientUntilSuccess(t *testing.T) {
	t.Parallel()
	f := &fakeRenamer{failures: 3, err: errTransient}
	require.NoError(t, f.renamer().replace("old", "new"))
	require.Equal(t, 4, f.calls)
	require.Equal(t, []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond}, f.sleeps)
}

func TestRenamerReplace_nonTransientFailsAtOnce(t *testing.T) {
	t.Parallel()
	f := &fakeRenamer{failures: renameAttempts, err: errPermanent}
	err := f.renamer().replace("old", "new")
	require.ErrorIs(t, err, errPermanent)
	require.EqualError(t, err, "nfd: rename into place: permanent")
	require.Equal(t, 1, f.calls)
	require.Empty(t, f.sleeps)
}

func TestRenamerReplace_succeedsOnLastAttempt(t *testing.T) {
	t.Parallel()
	f := &fakeRenamer{failures: renameAttempts - 1, err: errTransient}
	require.NoError(t, f.renamer().replace("old", "new"))
	require.Equal(t, renameAttempts, f.calls)
}

func TestRenamerReplace_givesUpAfterBound(t *testing.T) {
	t.Parallel()
	f := &fakeRenamer{failures: renameAttempts, err: errTransient}
	err := f.renamer().replace("old", "new")
	require.ErrorIs(t, err, errTransient)
	require.EqualError(t, err, "nfd: rename into place: 64 attempts: transient")
	require.Equal(t, renameAttempts, f.calls)
	require.Len(t, f.sleeps, renameAttempts-1)

	var total time.Duration
	for _, d := range f.sleeps {
		require.LessOrEqual(t, d, renameMaxWait)
		total += d
	}
	require.Equal(t, renameMaxWait, f.sleeps[len(f.sleeps)-1])
	require.Equal(t, 1887*time.Millisecond, total, "budget should stay near robustio's 2s")
}
