package safety

import (
	"log/slog"

	"github.com/golusoris/golusoris/clock"
)

// NewStripperForTest exposes the unexported stripper constructor to the
// external test package.
func NewStripperForTest(opts StripOptions, logger *slog.Logger) Stripper {
	return newStripper(Options{Strip: opts}, logger)
}

// NewFetcherForTest exposes the unexported fetcher constructor to the external
// test package.
func NewFetcherForTest(opts FetchOptions, logger *slog.Logger, clk clock.Clock) (Fetcher, error) {
	return newFetcher(Options{Fetch: opts}, logger, clk)
}
