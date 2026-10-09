// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package nfd

import (
	"errors"

	"golang.org/x/sys/windows"
)

// transientRenameError reports the errors MoveFileEx returns while another
// handle holds the target open without FILE_SHARE_DELETE, as os.Open does;
// cmd/internal/robustio retries the same two.
func transientRenameError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
