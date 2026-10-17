// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package robustfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	errTransient = errors.New("transient")
	errPermanent = errors.New("permanent")
)

type fakeOp struct {
	failures int
	err      error
	calls    int
	sleeps   []time.Duration
}

func (f *fakeOp) retryer() retryer {
	return retryer{
		op: func() error {
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

func TestRetryer_retriesTransientUntilSuccess(t *testing.T) {
	t.Parallel()
	f := &fakeOp{failures: 3, err: errTransient}
	require.NoError(t, f.retryer().run())
	require.Equal(t, 4, f.calls)
	require.Equal(t, []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond}, f.sleeps)
}

func TestRetryer_nonTransientFailsAtOnce(t *testing.T) {
	t.Parallel()
	f := &fakeOp{failures: attempts, err: errPermanent}
	err := f.retryer().run()
	require.ErrorIs(t, err, errPermanent)
	require.EqualError(t, err, "permanent")
	require.Equal(t, 1, f.calls)
	require.Empty(t, f.sleeps)
}

func TestRetryer_succeedsOnLastAttempt(t *testing.T) {
	t.Parallel()
	f := &fakeOp{failures: attempts - 1, err: errTransient}
	require.NoError(t, f.retryer().run())
	require.Equal(t, attempts, f.calls)
}

func TestRetryer_givesUpAfterBound(t *testing.T) {
	t.Parallel()
	f := &fakeOp{failures: attempts, err: errTransient}
	err := f.retryer().run()
	require.ErrorIs(t, err, errTransient)
	require.EqualError(t, err, "64 attempts: transient")
	require.Equal(t, attempts, f.calls)
	require.Len(t, f.sleeps, attempts-1)

	var total time.Duration
	for _, d := range f.sleeps {
		require.LessOrEqual(t, d, maxWait)
		total += d
	}
	require.Equal(t, maxWait, f.sleeps[len(f.sleeps)-1])
	require.Equal(t, 1887*time.Millisecond, total, "budget should stay near robustio's 2s")
}

func TestRename_renamesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old")
	newPath := filepath.Join(dir, "new")
	require.NoError(t, os.WriteFile(oldPath, []byte("data"), 0o600))
	require.NoError(t, Rename(oldPath, newPath))
	got, err := os.ReadFile(newPath)
	require.NoError(t, err)
	require.Equal(t, "data", string(got))
	_, err = os.Stat(oldPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRename_notExistFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := Rename(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dst"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRemove_removesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(path, []byte("content"), 0o600))
	require.NoError(t, Remove(path))
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRemove_notExistReturnsErrNotExist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := Remove(filepath.Join(dir, "nonexistent"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
