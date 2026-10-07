// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

// Package dirsync provides the platform boundary for directory durability.
package dirsync

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Sync uses directory flushing when available and accepts unsupported Windows
// failures for read-only directory handles after durable file writes.
func Sync(directory *os.File) error {
	err := directory.Sync()
	if err == nil {
		return nil
	}
	if unsupportedDirectorySyncError(err) {
		return nil
	}
	return fmt.Errorf("dirsync: sync directory: %w", err)
}

func unsupportedDirectorySyncError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_INVALID_HANDLE) ||
		errors.Is(err, windows.ERROR_INVALID_FUNCTION)
}
