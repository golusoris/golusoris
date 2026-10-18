// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package robustfs

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransient reports whether an error from a file system operation (such as
// MoveFileEx or DeleteFile) is retryable because another handle currently holds
// the file open without FILE_SHARE_DELETE; cmd/internal/robustio retries the same two.
func isTransient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
