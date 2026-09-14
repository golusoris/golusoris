// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package notify_test

import (
	"log/slog"

	"github.com/golusoris/golusoris/notify"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// compile-time: notify.New accepts *slog.Logger
var _ = notify.New(discardLogger())
