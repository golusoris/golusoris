// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package nfd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestTransientRenameError_windows(t *testing.T) {
	t.Parallel()
	for _, err := range []error{
		windows.ERROR_ACCESS_DENIED,
		windows.ERROR_SHARING_VIOLATION,
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: windows.ERROR_ACCESS_DENIED},
	} {
		require.True(t, transientRenameError(err), "%v", err)
	}
	for _, err := range []error{
		nil,
		windows.ERROR_FILE_NOT_FOUND,
		windows.ERROR_DISK_FULL,
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: windows.ERROR_PATH_NOT_FOUND},
	} {
		require.False(t, transientRenameError(err), "%v", err)
	}
}

// TestWriteFeatureFile_waitsForOpenReader holds the target open the way
// os.Open does (no FILE_SHARE_DELETE), so the first rename must be denied.
func TestWriteFeatureFile_waitsForOpenReader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	require.NoError(t, WriteFeatureFile(dir, "f", map[string]string{"example.com/a": "old"}))
	reader, err := os.Open(path)
	require.NoError(t, err)
	release := time.AfterFunc(50*time.Millisecond, func() { _ = reader.Close() })
	t.Cleanup(func() {
		if release.Stop() {
			_ = reader.Close()
		}
	})

	other := filepath.Join(dir, "g")
	require.NoError(t, os.WriteFile(other, nil, 0o600))
	err = os.Rename(other, path)
	require.True(t, transientRenameError(err), "a plain rename over the open file should be denied, got %v", err)

	require.NoError(t, WriteFeatureFile(dir, "f", map[string]string{"example.com/a": "new"}))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "example.com/a=new\n", string(got))
}
