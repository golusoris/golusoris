// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package storage

import (
	"errors"
	"os"
)

func tryLocalFileLock(*os.File) (bool, error) {
	return false, errors.New("storage: local bucket locking is unsupported on this platform")
}

func unlockLocalFile(*os.File) error {
	return nil
}
