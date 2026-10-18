// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package robustfs

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestTransientError_windows(t *testing.T) {
	t.Parallel()
	for _, err := range []error{
		windows.ERROR_ACCESS_DENIED,
		windows.ERROR_SHARING_VIOLATION,
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: windows.ERROR_ACCESS_DENIED},
		&os.PathError{Op: "remove", Path: "a", Err: windows.ERROR_ACCESS_DENIED},
	} {
		require.True(t, isTransient(err), "%v", err)
	}
	for _, err := range []error{
		nil,
		windows.ERROR_FILE_NOT_FOUND,
		windows.ERROR_DISK_FULL,
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: windows.ERROR_PATH_NOT_FOUND},
		&os.PathError{Op: "remove", Path: "a", Err: windows.ERROR_PATH_NOT_FOUND},
	} {
		require.False(t, isTransient(err), "%v", err)
	}
}

func TestRename_waitsForOpenReader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o600))

	reader, err := os.Open(target)
	require.NoError(t, err)
	var closeOnce sync.Once
	closeReader := func() { closeOnce.Do(func() { _ = reader.Close() }) }
	t.Cleanup(closeReader)

	replacement := filepath.Join(dir, "replacement")
	require.NoError(t, os.WriteFile(replacement, []byte("new"), 0o600))

	err = os.Rename(replacement, target)
	require.True(t, isTransient(err), "plain rename over open file should fail transiently, got %v", err)

	release := time.AfterFunc(50*time.Millisecond, closeReader)
	t.Cleanup(func() { release.Stop() })

	require.NoError(t, Rename(replacement, target))
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "new", string(got))
}

func TestRemove_waitsForOpenReader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte("data"), 0o600))

	reader, err := os.Open(target)
	require.NoError(t, err)
	var closeOnce sync.Once
	closeReader := func() { closeOnce.Do(func() { _ = reader.Close() }) }
	t.Cleanup(closeReader)

	err = os.Remove(target)
	require.True(t, isTransient(err), "plain remove over open file should fail transiently, got %v", err)

	release := time.AfterFunc(50*time.Millisecond, closeReader)
	t.Cleanup(func() { release.Stop() })

	require.NoError(t, Remove(target))
	_, err = os.Stat(target)
	require.ErrorIs(t, err, os.ErrNotExist)
}
