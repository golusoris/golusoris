package audit_test

import (
	"context"
	"testing"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/audit"
)

func TestLog_basic(t *testing.T) {
	t.Parallel()
	store := audit.NewMemoryStore()
	clk := clockwork.NewFakeClock()
	lg := audit.New(store, audit.WithClock(clk))

	err := lg.Log(context.Background(), audit.Event{
		Actor:  "user:1",
		Action: "order.cancel",
		Target: "order:99",
	})
	if err != nil {
		t.Fatal(err)
	}

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.ID == "" {
		t.Error("ID should be auto-assigned")
	}
	if e.CreatedAt.IsZero() {
		t.Error("CreatedAt should be auto-assigned")
	}
	if e.Actor != "user:1" || e.Action != "order.cancel" || e.Target != "order:99" {
		t.Errorf("unexpected event: %+v", e)
	}
}

func TestList_filter(t *testing.T) {
	t.Parallel()
	store := audit.NewMemoryStore()
	lg := audit.New(store)

	for _, ev := range []audit.Event{
		{Actor: "user:1", Action: "login", Target: "session"},
		{Actor: "user:2", Action: "login", Target: "session"},
		{Actor: "user:1", Action: "order.create", Target: "order:1"},
	} {
		if err := lg.Log(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}

	evs, err := lg.List(context.Background(), audit.Filter{Actor: "user:1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("expected 2 events for user:1, got %d", len(evs))
	}
}

func TestList_limit(t *testing.T) {
	t.Parallel()
	store := audit.NewMemoryStore()
	lg := audit.New(store)

	for i := range 5 {
		_ = lg.Log(context.Background(), audit.Event{
			Actor: "user:1", Action: "ping", Target: "server",
			Metadata: map[string]any{"i": i},
		})
	}

	evs, err := lg.List(context.Background(), audit.Filter{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3 {
		t.Fatalf("expected 3, got %d", len(evs))
	}
}
