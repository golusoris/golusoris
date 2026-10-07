// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package watch_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

	w, err := watch.New(watch.Options{Debounce: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Add(dir); err != nil {
		t.Fatal(err)
	}

	// Write 5 files rapidly — should produce at most 2 events (debounced).
	for i := range 5 {
		path := filepath.Join(dir, filepath.FromSlash("f"+string(rune('0'+i))+".txt"))
		_ = os.WriteFile(path, []byte("x"), 0o640)
	}

	// maxEvents bounds the drain loop; the timeout normally ends it first.
	const maxEvents = 100
	var eventCount int
	timeout := time.After(500 * time.Millisecond)
loop:
	for range maxEvents {
		select {
		case <-w.Events():
			eventCount++
		case <-timeout:
			break loop
		}
	}
	// Debounce collapses bursts; expect far fewer events than files.
	if eventCount > 3 {
		t.Fatalf("debounce failed: got %d events for 5 rapid writes", eventCount)
	}
}
