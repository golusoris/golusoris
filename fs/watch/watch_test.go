// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package watch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/fs/watch"
)

func TestWatcher_detects_change(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := watch.New(watch.Options{Debounce: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Add(dir); err != nil {
		t.Fatal(err)
	}

	// Write a file to trigger an event.
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-w.Events():
		if len(ev.Paths) == 0 {
			t.Fatal("expected non-empty paths in event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch event")
	}
}

func TestWatcher_remove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := watch.New(watch.Options{Debounce: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Add(dir); err != nil {
		t.Fatal(err)
	}
	// Remove the path that was just added — should not error.
	if err := w.Remove(dir); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func TestWatcher_remove_unregistered(t *testing.T) {
	t.Parallel()
	w, err := watch.New(watch.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Removing a path that was never added should return a wrapped error.
	err = w.Remove("/nonexistent/path/that/was/never/added")
	if err == nil {
		t.Fatal("expected error when removing unregistered path")
	}
}

func TestWatcher_CloseClosesEvents(t *testing.T) {
	t.Parallel()
	w, err := watch.New(watch.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case _, ok := <-w.Events():
		if ok {
			t.Fatal("Events remained open after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Events did not close after Close")
	}
}

func TestNew_RejectsNegativeOptions(t *testing.T) {
	t.Parallel()
	for name, opts := range map[string]watch.Options{
		"debounce":    {Debounce: -time.Millisecond},
		"buffer size": {BufferSize: -1},
		"max paths":   {MaxPaths: -1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w, err := watch.New(opts)
			if err == nil {
				if w != nil {
					_ = w.Close()
				}
				t.Fatal("New accepted a negative option")
			}
			if w != nil {
				t.Fatal("New returned a watcher for a negative option")
			}
		})
	}
}

func TestWatcher_debounce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const debounce = 1 * time.Second
	w, err := watch.New(watch.Options{Debounce: debounce})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Add(dir); err != nil {
		t.Fatal(err)
	}

	// The debounce is trailing (each event restarts the timer), so one event
	// needs every gap between writes inside the window, not the whole burst.
	if gap := writeBurst(t, dir, 5); gap >= debounce {
		t.Fatalf("gap between writes %v reached debounce window %v", gap, debounce)
	}

	// Drain events up to a bounded timeout scaled to debounce.
	const maxEvents = 100
	var eventCount int
	drainTimeout := debounce + 2*time.Second
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
loop:
	for range maxEvents {
		select {
		case <-w.Events():
			eventCount++
		case <-ctx.Done():
			break loop
		}
	}
	// Debounce collapses rapid bursts into exactly 1 event.
	if eventCount != 1 {
		t.Fatalf("debounce failed: got %d events for 5 rapid writes, want 1", eventCount)
	}
}

// writeBurst writes n files into dir and returns the largest gap between
// the starts of consecutive writes.
func writeBurst(t *testing.T, dir string, n int) time.Duration {
	t.Helper()
	clk := clockwork.NewRealClock()
	var largest time.Duration
	prev := clk.Now()
	for i := range n {
		path := filepath.Join(dir, filepath.FromSlash("f"+string(rune('0'+i))+".txt"))
		start := clk.Now()
		if gap := start.Sub(prev); gap > largest {
			largest = gap
		}
		prev = start
		if err := os.WriteFile(path, []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}
		t.Logf("write %d took %v", i, clk.Since(start))
	}
	return largest
}
