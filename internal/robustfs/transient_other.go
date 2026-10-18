// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !windows

package robustfs

// isTransient is always false on non-Windows platforms: POSIX rename and unlink
// succeed even while open file descriptors reference the target.
func isTransient(error) bool { return false }
