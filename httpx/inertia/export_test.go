// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package inertia

import (
	"log/slog"

	"github.com/golusoris/golusoris/core/config"
)

// LoadOptionsForTest exposes loadOptions to the external test package.
func LoadOptionsForTest(cfg *config.Config) (Options, error) {
	return loadOptions(cfg)
}

// SlogLoggerForTest is the gonertia Logger adapter, exposed for tests.
type SlogLoggerForTest interface {
	Printf(format string, v ...any)
	Println(v ...any)
}

// NewSlogLoggerForTest builds the slog->gonertia Logger adapter for tests.
func NewSlogLoggerForTest(l *slog.Logger) SlogLoggerForTest {
	return slogLogger{l: l}
}
