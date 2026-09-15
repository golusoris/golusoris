// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package river

import (
	"testing"
	"time"
)

// TestOptions_withDefaults exercises the pure defaulting logic Start relies
// on, without needing Docker: positive (zero timeouts get the documented
// defaults), negative (both fields left zero at once), and boundary (an
// explicit non-zero value, however small, is preserved rather than
// overridden).
func TestOptions_withDefaults(t *testing.T) {
	t.Parallel()

	t.Run("zero timeouts get defaults", func(t *testing.T) {
		t.Parallel()
		got := Options{}.withDefaults()
		if got.JobTimeout != 5*time.Second {
			t.Errorf("JobTimeout = %v, want 5s", got.JobTimeout)
		}
		if got.StartTimeout != 10*time.Second {
			t.Errorf("StartTimeout = %v, want 10s", got.StartTimeout)
		}
	})

	t.Run("explicit values are preserved", func(t *testing.T) {
		t.Parallel()
		got := Options{JobTimeout: 30 * time.Second, StartTimeout: time.Minute}.withDefaults()
		if got.JobTimeout != 30*time.Second {
			t.Errorf("JobTimeout = %v, want 30s", got.JobTimeout)
		}
		if got.StartTimeout != time.Minute {
			t.Errorf("StartTimeout = %v, want 1m", got.StartTimeout)
		}
	})

	t.Run("a tiny explicit value is not treated as unset", func(t *testing.T) {
		t.Parallel()
		got := Options{JobTimeout: time.Nanosecond}.withDefaults()
		if got.JobTimeout != time.Nanosecond {
			t.Errorf("JobTimeout = %v, want 1ns (boundary: smallest non-zero duration)", got.JobTimeout)
		}
		if got.StartTimeout != 10*time.Second {
			t.Errorf("StartTimeout = %v, want 10s default (unaffected by JobTimeout)", got.StartTimeout)
		}
	})
}
