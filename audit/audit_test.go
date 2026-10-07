// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package audit_test

import (
	"context"
	"testing"
	"time"

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

	events, err := store.AllChecked()
	if err != nil {
		t.Fatal(err)
	}
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

func TestNewHandlesTypedNilDependencies(t *testing.T) {
	t.Parallel()

	t.Run("store", func(t *testing.T) {
		t.Parallel()
		var store *audit.MemoryStore
		logger := audit.New(store)
		if err := logger.Log(t.Context(), audit.Event{}); err == nil {
			t.Fatal("typed-nil store should return an error")
		}
	})

	t.Run("clock", func(t *testing.T) {
		t.Parallel()
		var clk *clockwork.FakeClock
		logger := audit.New(audit.NewMemoryStore(), audit.WithClock(clk))
		if err := logger.Log(t.Context(), audit.Event{}); err != nil {
			t.Fatalf("typed-nil clock should use the default: %v", err)
		}
	})
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

func TestMemoryStoreSnapshotsMutableEventData(t *testing.T) {
	t.Parallel()
	store := audit.NewMemoryStore()
	logger := audit.New(store)
	metadata := map[string]any{
		"roles":  []string{"reader"},
		"nested": map[string]any{"ip": "127.0.0.1"},
	}
	diff := audit.Diff{"roles": {Before: []any{"reader"}, After: []any{"writer"}}}
	requireNoError(t, logger.Log(t.Context(), audit.Event{
		Actor: "user:1", Action: "role.change", Target: "user:1", Metadata: metadata, Diff: diff,
	}))
	metadata["roles"].([]string)[0] = "admin"
	metadata["nested"].(map[string]any)["ip"] = "attacker"
	diff["roles"] = audit.FieldChange{After: "admin"}

	first, err := store.AllChecked()
	requireNoError(t, err)
	if got := first[0].Metadata["roles"].([]string)[0]; got != "reader" {
		t.Fatalf("stored roles = %q", got)
	}
	if got := first[0].Metadata["nested"].(map[string]any)["ip"]; got != "127.0.0.1" {
		t.Fatalf("stored nested ip = %q", got)
	}
	first[0].Metadata["roles"].([]string)[0] = "mutated"
	again, err := store.AllChecked()
	requireNoError(t, err)
	if got := again[0].Metadata["roles"].([]string)[0]; got != "reader" {
		t.Fatalf("returned alias changed history: %q", got)
	}
}

func TestMemoryStoreSnapshotsTypedContainersAndCycles(t *testing.T) {
	t.Parallel()
	store := audit.NewMemoryStore()
	logger := audit.New(store)
	labels := map[string]string{"role": "reader"}
	counts := []int{1, 2}
	cycle := map[string]any{}
	cycle["self"] = cycle
	requireNoError(t, logger.Log(t.Context(), audit.Event{
		Actor: "user:1", Action: "snapshot.test", Target: "test:1",
		Metadata: map[string]any{"labels": labels, "counts": counts, "cycle": cycle},
	}))
	labels["role"] = "admin"
	counts[0] = 99
	cycle["late"] = "caller mutation"

	events, err := store.AllChecked()
	requireNoError(t, err)
	metadata := events[0].Metadata
	if got := metadata["labels"].(map[string]string)["role"]; got != "reader" {
		t.Fatalf("stored labels = %q", got)
	}
	if got := metadata["counts"].([]int)[0]; got != 1 {
		t.Fatalf("stored counts[0] = %d", got)
	}
	clonedCycle := metadata["cycle"].(map[string]any)
	if _, exists := clonedCycle["late"]; exists {
		t.Fatal("caller mutation reached stored cycle")
	}
	clonedCycle["returned"] = true
	again, err := store.AllChecked()
	requireNoError(t, err)
	if _, exists := again[0].Metadata["cycle"].(map[string]any)["returned"]; exists {
		t.Fatal("returned cycle aliases stored history")
	}
}

func TestListInclusiveBoundsAndOffset(t *testing.T) {
	t.Parallel()
	store := audit.NewMemoryStore()
	logger := audit.New(store)
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		requireNoError(t, logger.Log(t.Context(), audit.Event{
			ID:        string(rune('a' + i)),
			Actor:     "user",
			Action:    "test",
			Target:    "item",
			CreatedAt: start.Add(time.Duration(i) * time.Minute),
		}))
	}

	bounded, err := logger.List(t.Context(), audit.Filter{
		After: start.Add(time.Minute), Before: start.Add(3 * time.Minute),
	})
	requireNoError(t, err)
	if len(bounded) != 3 {
		t.Fatalf("inclusive bounded result count = %d, want 3", len(bounded))
	}

	paged, err := logger.List(t.Context(), audit.Filter{Offset: 2, Limit: 2})
	requireNoError(t, err)
	if len(paged) != 2 || paged[0].ID != "c" || paged[1].ID != "b" {
		t.Fatalf("offset page = %+v", paged)
	}
}

func TestMemoryStoreAllPreservesLegacyFunctionType(t *testing.T) {
	t.Parallel()
	store := audit.NewMemoryStore()
	requireNoError(t, store.Append(t.Context(), audit.Event{ID: "legacy"}))

	legacy := requireLegacyAll(store.All)
	events := legacy()
	if len(events) != 1 || events[0].ID != "legacy" {
		t.Fatalf("legacy snapshot = %+v", events)
	}
	// All remains a detached snapshot despite its legacy no-error signature.
	events[0].ID = "mutated"
	if got := legacy()[0].ID; got != "legacy" {
		t.Fatalf("stored event mutated through legacy snapshot: %q", got)
	}
}

func requireLegacyAll(all func() []audit.Event) func() []audit.Event {
	return all
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
