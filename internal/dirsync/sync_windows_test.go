// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package dirsync

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSyncDirectoryUsesWindowsBestAvailableBoundary(t *testing.T) {
	t.Parallel()
	directory, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := directory.Close(); closeErr != nil {
			t.Errorf("close directory: %v", closeErr)
		}
	}()
	if err = Sync(directory); err != nil {
		t.Fatalf("Sync directory: %v", err)
	}
}

func TestUnsupportedDirectorySyncErrorsAreNarrow(t *testing.T) {
	t.Parallel()
	for _, err := range []error{
		windows.ERROR_ACCESS_DENIED,
		windows.ERROR_INVALID_HANDLE,
		windows.ERROR_INVALID_FUNCTION,
		&os.PathError{Op: "sync", Path: ".", Err: windows.ERROR_ACCESS_DENIED},
	} {
		if !unsupportedDirectorySyncError(err) {
			t.Errorf("error %v not recognized as unsupported", err)
		}
	}
	if unsupportedDirectorySyncError(windows.ERROR_DISK_FULL) {
		t.Fatal("disk-full error was treated as unsupported")
	}
	if unsupportedDirectorySyncError(errors.New("other")) {
		t.Fatal("arbitrary error was treated as unsupported")
	}
}
