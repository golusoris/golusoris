// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !windows

// Package dirsync provides the platform boundary for directory durability.
package dirsync

import (
	"fmt"
	"os"
)

// Sync flushes directory-entry changes when the platform supports it.
func Sync(directory *os.File) error {
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("dirsync: sync directory: %w", err)
	}
	return nil
}
