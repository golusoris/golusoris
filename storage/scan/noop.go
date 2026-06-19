package scan

import (
	"context"
	"fmt"
	"io"
	"log/slog"
)

// noopScanner always reports Clean. It exists for local dev / tests where no
// clamd daemon is reachable and MUST be opted into explicitly (backend="noop")
// — it can never be the silent default, and it shouts a WARN on construction so
// a disabled scanner can never go unnoticed in prod.
type noopScanner struct{ logger *slog.Logger }

// NewNoopScanner builds the no-op (always-clean) backend. It logs a loud WARN
// that scanning is DISABLED. For dev/test only — never in prod.
func NewNoopScanner(logger *slog.Logger) Scanner { return newNoopScanner(logger) }

// newNoopScanner builds the no-op backend and logs the loud disabled warning.
func newNoopScanner(logger *slog.Logger) *noopScanner {
	logger.Warn("storage/scan: NOOP backend selected — malware scanning is DISABLED")
	return &noopScanner{logger: logger}
}

// Scan always returns a clean verdict, draining r to honour the io.Reader
// contract (callers may expect the stream consumed).
func (s *noopScanner) Scan(ctx context.Context, r io.Reader) (Verdict, error) {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return Verdict{}, fmt.Errorf("storage/scan: noop drain: %w", err)
	}
	return Verdict{Clean: true}, nil
}

// ScanStrict always succeeds.
func (s *noopScanner) ScanStrict(ctx context.Context, r io.Reader) error {
	_, err := s.Scan(ctx, r)
	return err
}

// Ping always reports the noop backend reachable.
func (s *noopScanner) Ping(ctx context.Context) error { return nil }
