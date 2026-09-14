// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !unix

package scan

import (
	"fmt"
	"log/slog"
	"runtime"

	"github.com/golusoris/golusoris/core/clock"
)

// errUnsupportedPlatform names the host so the fail-closed boot error is
// self-explanatory in logs.
var errUnsupportedPlatform = fmt.Errorf(
	"storage/scan: %s/%s: %w", runtime.GOOS, runtime.GOARCH, ErrUnsupported,
)

// NewClamdScanner is the non-unix stub: baruwa-enterprise/clamd needs unix
// socket syscalls, so the clamd backend cannot be built here. It always fails
// with [ErrUnsupported]; use the noop backend for local dev on this platform.
func NewClamdScanner(opts ClamdOptions, logger *slog.Logger, clk clock.Clock) (Scanner, error) {
	return newClamdScanner(opts, logger, clk)
}

// newClamdScanner mirrors the unix constructor's seam so module.go compiles
// unchanged; it never returns a Scanner.
func newClamdScanner(_ ClamdOptions, _ *slog.Logger, _ clock.Clock) (Scanner, error) {
	return nil, errUnsupportedPlatform
}
