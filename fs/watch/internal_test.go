// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package watch

import (
	"slices"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestEmitSortsAndClearsPendingPaths(t *testing.T) {
	t.Parallel()
	w := &Watcher{events: make(chan Event, 1)}
	pending := map[string]struct{}{"z": {}, "a": {}, "m": {}}
	w.emit(pending)
	event := <-w.events
	if !slices.Equal(event.Paths, []string{"a", "m", "z"}) {
		t.Fatalf("paths = %v; want sorted paths", event.Paths)
	}
	if len(pending) != 0 {
		t.Fatalf("pending retained %d paths after emit", len(pending))
	}
}

func TestStepFlushesAtMaxPaths(t *testing.T) {
	t.Parallel()
	inner := &fsnotify.Watcher{
		Events: make(chan fsnotify.Event, 1),
		Errors: make(chan error),
	}
	w := &Watcher{
		inner:  inner,
		events: make(chan Event, 1),
		opts:   Options{Debounce: time.Second, MaxPaths: 1},
	}
	pending := map[string]struct{}{"first": {}}
	inner.Events <- fsnotify.Event{Name: "second", Op: fsnotify.Write}
	timer, open := w.step(nil, pending)
	stopTimer(timer)
	if !open {
		t.Fatal("step closed watcher for a valid event")
	}
	if event := <-w.events; !slices.Equal(event.Paths, []string{"first"}) {
		t.Fatalf("flushed paths = %v; want first", event.Paths)
	}
	if _, ok := pending["second"]; !ok || len(pending) != 1 {
		t.Fatalf("pending = %v; want only second", pending)
	}
}
