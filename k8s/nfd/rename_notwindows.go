// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !windows

package nfd

// transientRenameError is always false: POSIX rename replaces the target
// even while readers hold it open.
func transientRenameError(error) bool { return false }
