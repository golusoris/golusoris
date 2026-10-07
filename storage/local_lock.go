// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

const (
	localLockRetryInterval = 10 * time.Millisecond
	maxLocalLockAttempts   = 6000
)

type localOperationLock struct {
	file   *os.File
	locked bool
}

func acquireLocalOperationLock(
	ctx context.Context, root *os.Root, phase string,
) (*localOperationLock, error) {
	file, err := openLocalLockFile(root)
	if err != nil {
		return nil, err
	}
	lock := &localOperationLock{file: file}
	if err = lock.acquire(ctx, phase); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if err = validateLocalLockIdentity(root, file); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return lock, nil
}

func validateLocalLockIdentity(root *os.Root, file *os.File) error {
	pathInfo, err := root.Lstat(localLockName)
	if err != nil {
		return fmt.Errorf("storage: reinspect local operation lock: %w", err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("storage: restat local operation lock: %w", err)
	}
	if !pathInfo.Mode().IsRegular() || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return errors.New("storage: local operation lock replaced during acquisition")
	}
	return nil
}

func openLocalLockFile(root *os.Root) (*os.File, error) {
	for range 2 {
		before, err := root.Lstat(localLockName)
		if errors.Is(err, fs.ErrNotExist) {
			file, createErr := root.OpenFile(localLockName, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if createErr == nil {
				return file, nil
			}
			if errors.Is(createErr, fs.ErrExist) {
				continue
			}
			return nil, fmt.Errorf("storage: create local operation lock: %w", createErr)
		}
		if err != nil {
			return nil, fmt.Errorf("storage: inspect local operation lock: %w", err)
		}
		return openExistingLocalLock(root, before)
	}
	return nil, errors.New("storage: local operation lock changed during open")
}

func openExistingLocalLock(root *os.Root, before os.FileInfo) (*os.File, error) {
	if !before.Mode().IsRegular() {
		return nil, errors.New("storage: local operation lock must be a regular file")
	}
	file, err := root.OpenFile(localLockName, os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("storage: open local operation lock: %w", err)
	}
	opened, err := file.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("storage: stat local operation lock: %w", err), file.Close())
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, errors.Join(errors.New("storage: local operation lock changed during open"), file.Close())
	}
	return file, nil
}

func (l *localOperationLock) acquire(ctx context.Context, phase string) error {
	for attempt := range maxLocalLockAttempts {
		if err := checkLocalContext(ctx, phase+" lock wait"); err != nil {
			return err
		}
		acquired, err := tryLocalFileLock(l.file)
		if err != nil {
			return fmt.Errorf("storage: acquire local %s lock: %w", phase, err)
		}
		if acquired {
			l.locked = true
			return nil
		}
		if attempt+1 == maxLocalLockAttempts {
			break
		}
		timer := time.NewTimer(localLockRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("storage: local %s lock wait: %w", phase, ctx.Err())
		case <-timer.C:
		}
	}
	return fmt.Errorf("storage: acquire local %s lock: retry limit reached", phase)
}

func (l *localOperationLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	var unlockErr error
	if l.locked {
		unlockErr = unlockLocalFile(l.file)
		l.locked = false
	}
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
}
