// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package storage

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func tryLocalFileLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("flock local bucket: %w", err)
	}
	return true, nil
}

func unlockLocalFile(file *os.File) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("unlock local bucket flock: %w", err)
	}
	return nil
}
