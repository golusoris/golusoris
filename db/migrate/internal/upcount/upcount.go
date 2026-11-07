// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package upcount counts Migrator.Up calls through their log record, for the module tests of
// db/migrate and its driver modules.
package upcount

import (
	"context"
	"log/slog"
	"sync/atomic"
)

// upComplete is the record Migrator.Up writes once per call.
const upComplete = "db/migrate: up complete"

type handler struct {
	slog.Handler
	n *atomic.Int32
}

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == upComplete {
		h.n.Add(1)
	}
	return h.Handler.Handle(ctx, r) //nolint:wrapcheck // WHY: pass-through to the discard handler, no context to add.
}

// Enabled is true because DiscardHandler would drop the record before Handle.
func (handler) Enabled(context.Context, slog.Level) bool { return true }

// Logger returns a logger that adds one to n for each Up call it records.
func Logger(n *atomic.Int32) *slog.Logger {
	return slog.New(handler{Handler: slog.DiscardHandler, n: n})
}
