// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nfd

import (
	"fmt"
	"os"
	"time"

	"github.com/jonboulle/clockwork"
)

// Rename retry budget: waits double from 1ms to 32ms, about 1.9s over all
// attempts, close to the 2s cmd/internal/robustio allows for Windows renames.
const (
	renameAttempts  = 64
	renameFirstWait = time.Millisecond
	renameMaxWait   = 32 * time.Millisecond
)

// renamer moves a temp file into place, retrying errors transient reports.
type renamer struct {
	rename    func(oldpath, newpath string) error
	transient func(error) bool
	sleep     func(time.Duration)
}

// osRenamer waits on the real clock: the backoff tracks other handles on the
// file, which a fake test clock would never release.
func osRenamer() renamer {
	return renamer{rename: os.Rename, transient: transientRenameError, sleep: clockwork.NewRealClock().Sleep}
}

// replace renames oldpath to newpath, retrying transient errors up to
// renameAttempts times.
func (r renamer) replace(oldpath, newpath string) error {
	wait := renameFirstWait
	var err error
	for attempt := range renameAttempts {
		if attempt > 0 {
			r.sleep(wait)
			wait = min(2*wait, renameMaxWait)
		}
		err = r.rename(oldpath, newpath)
		if err == nil {
			return nil
		}
		if !r.transient(err) {
			return fmt.Errorf("nfd: rename into place: %w", err)
		}
	}
	return fmt.Errorf("nfd: rename into place: %d attempts: %w", renameAttempts, err)
}
