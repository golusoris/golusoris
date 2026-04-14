package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/payments/meter"
)

func TestRecord_Idempotent(t *testing.T) {
	t.Parallel()
	store := meter.NewMemoryStore()
	rec := meter.NewRecorder(store, clock.NewFake(), nil)

	ev := meter.Event{ID: "e1", CustomerID: "c1", Meter: "api_calls", Quantity: 1}
	require.NoError(t, rec.Record(context.Background(), ev))
	require.NoError(t, rec.Record(context.Background(), ev)) // dupe ignored

	usage, err := rec.Usage(context.Background(), "c1", "api_calls", time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Equal(t, 1.0, usage)
}

func TestUsage_TimeWindow(t *testing.T) {
	t.Parallel()
	store := meter.NewMemoryStore()
	clk := clock.NewFake()
	rec := meter.NewRecorder(store, clk, nil)

	t0 := clk.Now()
	require.NoError(t, rec.Record(context.Background(), meter.Event{
		ID: "e1", CustomerID: "c1", Meter: "m", Quantity: 5, At: t0,
	}))
	require.NoError(t, rec.Record(context.Background(), meter.Event{
		ID: "e2", CustomerID: "c1", Meter: "m", Quantity: 3, At: t0.Add(time.Hour),
	}))
	require.NoError(t, rec.Record(context.Background(), meter.Event{
		ID: "e3", CustomerID: "c1", Meter: "m", Quantity: 7, At: t0.Add(2 * time.Hour),
	}))

	// Window [t0, t0+1h) returns only e1.
	got, err := rec.Usage(context.Background(), "c1", "m", t0, t0.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, 5.0, got)

	// Window [t0, t0+90m) returns e1 + e2.
	got, err = rec.Usage(context.Background(), "c1", "m", t0, t0.Add(90*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 8.0, got)
}

func TestRecord_RequiresIDCustomerMeter(t *testing.T) {
	t.Parallel()
	rec := meter.NewRecorder(meter.NewMemoryStore(), clock.NewFake(), nil)
	require.Error(t, rec.Record(context.Background(), meter.Event{CustomerID: "c", Meter: "m"}))
	require.Error(t, rec.Record(context.Background(), meter.Event{ID: "e", Meter: "m"}))
	require.Error(t, rec.Record(context.Background(), meter.Event{ID: "e", CustomerID: "c"}))
}

func TestList_FilterByCustomer(t *testing.T) {
	t.Parallel()
	store := meter.NewMemoryStore()
	rec := meter.NewRecorder(store, clock.NewFake(), nil)
	for i, c := range []string{"c1", "c2", "c1"} {
		require.NoError(t, rec.Record(context.Background(), meter.Event{
			ID: string(rune('a' + i)), CustomerID: c, Meter: "m", Quantity: 1,
		}))
	}
	out, err := rec.List(context.Background(), meter.Filter{CustomerID: "c1"})
	require.NoError(t, err)
	require.Len(t, out, 2)
}
