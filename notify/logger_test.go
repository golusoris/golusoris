package notify_test

import (
	"log/slog"

	"github.com/golusoris/golusoris/notify"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// compile-time: notify.New accepts *slog.Logger
var _ = notify.New(discardLogger())
