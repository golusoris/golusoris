// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOptionsWithDefaultsDoesNotAllowNegativeBounds(t *testing.T) {
	t.Parallel()
	got := (Options{
		Timeouts: TimeoutOptions{
			Read:     -time.Second,
			Header:   -time.Second,
			Write:    -time.Second,
			Idle:     -time.Second,
			Shutdown: -time.Second,
		},
		Limits: LimitOptions{Header: -1, Body: -1},
	}).withDefaults()
	want := DefaultOptions()
	require.Equal(t, want.Timeouts, got.Timeouts)
	require.Equal(t, want.Limits, got.Limits)
}

func TestOptionsWithDefaultsRequiresExplicitUnlimitedBody(t *testing.T) {
	t.Parallel()
	defaults := (Options{}).withDefaults()
	require.Equal(t, DefaultOptions().Limits.Body, defaults.Limits.Body)
	unlimited := (Options{Limits: LimitOptions{AllowUnlimitedBody: true}}).withDefaults()
	require.Zero(t, unlimited.Limits.Body)
}
