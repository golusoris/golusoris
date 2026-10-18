// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package robustfs provides file system operations that retry transient sharing
// and access-denied errors on platforms like Windows where concurrent file handles
// prevent rename and deletion.
package robustfs

import (
	"fmt"
	"os"
	"time"

	"github.com/jonboulle/clockwork"
)

// Retry budget: waits double from 1ms to 32ms, about 1.9s over all
// attempts, close to the 2s cmd/internal/robustio allows for Windows renames.
const (
	attempts  = 64
	firstWait = time.Millisecond
	maxWait   = 32 * time.Millisecond
)

// retryer retries an operation when transient reports a retryable error.
type retryer struct {
	op        func() error
	transient func(error) bool
	sleep     func(time.Duration)
}

// osRetryer waits on the real clock: the backoff tracks other handles on the
// file, which a fake test clock would never release.
func osRetryer(op func() error) retryer {
	return retryer{
		op:        op,
		transient: isTransient,
		sleep:     clockwork.NewRealClock().Sleep,
	}
}

// run executes the operation, retrying transient errors up to attempts times.
func (r retryer) run() error {
	wait := firstWait
	var err error
	for attempt := range attempts {
		if attempt > 0 {
			r.sleep(wait)
			wait = min(2*wait, maxWait)
		}
		err = r.op()
		if err == nil {
			return nil
		}
		if !r.transient(err) {
			return err
		}
	}
	return fmt.Errorf("%d attempts: %w", attempts, err)
}

// Rename moves oldpath to newpath, retrying transient sharing and access errors.
func Rename(oldpath, newpath string) error {
	return osRetryer(func() error { return os.Rename(oldpath, newpath) }).run()
}

// Remove removes the named file, retrying transient sharing and access errors.
// Non-transient errors (such as os.ErrNotExist) pass through unwrapped so
// callers can inspect them with errors.Is.
func Remove(path string) error {
	return osRetryer(func() error { return os.Remove(path) }).run()
}
