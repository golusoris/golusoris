// Package meter provides usage metering for billing-by-consumption
// SaaS apps. Apps record granular usage events; the package
// deduplicates by event ID, stores them, and aggregates over time
// windows for billing periods.
//
// Apps export aggregated rollups to their payment processor's metered-
// billing endpoint (e.g. Stripe usage records, Lemon Squeezy
// `usage_record`, Paddle `transaction.adjustment`). The package itself
// is processor-agnostic.
//
// Usage:
//
//	rec := meter.NewRecorder(store, clock)
//	_ = rec.Record(ctx, meter.Event{
//	    ID:         "req-abc-123",        // idempotency key
//	    CustomerID: "cust_42",
//	    Meter:      "api_calls",
//	    Quantity:   1,
//	    At:         time.Now(),
//	})
//	usage, _ := rec.Usage(ctx, "cust_42", "api_calls", periodStart, periodEnd)
package meter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golusoris/golusoris/clock"
)

// Event is a single usage record.
type Event struct {
	// ID is the idempotency key. Recording the same ID twice is a no-op.
	ID string
	// CustomerID identifies who consumed the resource.
	CustomerID string
	// Meter is the metric name (e.g. "api_calls", "storage_bytes",
	// "compute_seconds").
	Meter string
	// Quantity is the consumed amount (use float64 so we can express
	// fractional units like compute-seconds).
	Quantity float64
	// At is the event timestamp. Apps may pass time.Time{}; the
	// Recorder fills it in from the clock.
	At time.Time
	// Metadata is provider-specific extras (request ID, region, …).
	Metadata map[string]string
}

// Filter narrows queries.
type Filter struct {
	CustomerID string
	Meter      string
	Since      time.Time // inclusive
	Until      time.Time // exclusive
	Limit      int
}

// Store persists events. Implementations must enforce ID uniqueness
// (silently ignore duplicates) and support range queries by customer +
// meter + time.
type Store interface {
	// Insert is idempotent on Event.ID — a duplicate ID returns
	// [ErrDuplicate]; callers ignore it.
	Insert(ctx context.Context, e Event) error
	// Query returns events matching the filter. Implementations should
	// stream large result sets where possible.
	Query(ctx context.Context, f Filter) ([]Event, error)
	// Sum returns the total Quantity matching the filter.
	Sum(ctx context.Context, f Filter) (float64, error)
}

// ErrDuplicate is returned by [Store.Insert] when Event.ID already
// exists.
var ErrDuplicate = errors.New("payments/meter: duplicate event ID")

// Recorder writes events through a [Store].
type Recorder struct {
	store  Store
	clock  clock.Clock
	logger *slog.Logger
}

// NewRecorder returns a Recorder. logger may be nil.
func NewRecorder(store Store, clk clock.Clock, logger *slog.Logger) *Recorder {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Recorder{store: store, clock: clk, logger: logger}
}

// Record persists an event. Returns nil on success or duplicate (the
// latter is logged at debug). Other Store errors propagate.
func (r *Recorder) Record(ctx context.Context, e Event) error {
	if e.ID == "" {
		return errors.New("payments/meter: Event.ID is required for idempotency")
	}
	if e.CustomerID == "" || e.Meter == "" {
		return errors.New("payments/meter: CustomerID and Meter are required")
	}
	if e.At.IsZero() {
		e.At = r.clock.Now()
	}
	err := r.store.Insert(ctx, e)
	if errors.Is(err, ErrDuplicate) {
		r.logger.DebugContext(ctx, "meter: duplicate event ignored",
			slog.String("id", e.ID),
			slog.String("customer", e.CustomerID),
		)
		return nil
	}
	if err != nil {
		return err //nolint:wrapcheck // Store-defined; pass through
	}
	return nil
}

// Usage returns the summed Quantity for one customer/meter pair over
// [since, until).
func (r *Recorder) Usage(ctx context.Context, customerID, name string, since, until time.Time) (float64, error) {
	v, err := r.store.Sum(ctx, Filter{
		CustomerID: customerID,
		Meter:      name,
		Since:      since,
		Until:      until,
	})
	if err != nil {
		return 0, fmt.Errorf("payments/meter: sum: %w", err)
	}
	return v, nil
}

// List returns raw events matching the filter. Use for audit /
// reconciliation; for billing aggregates use [Recorder.Usage].
func (r *Recorder) List(ctx context.Context, f Filter) ([]Event, error) {
	out, err := r.store.Query(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("payments/meter: query: %w", err)
	}
	return out, nil
}
