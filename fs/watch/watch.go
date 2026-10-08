// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package watch provides a debounced recursive directory watcher built on
// fsnotify. A single Handler func is called once per debounce window with
// all paths that changed, preventing event storms from editors that write
// multiple times per save.
//
// Usage:
//
//	w, err := watch.New(watch.Options{Debounce: 200 * time.Millisecond})
//	if err != nil { ... }
//	defer w.Close()
//
//	w.Add("/var/config")
//	for ev := range w.Events() {
//	    log.Println("changed:", ev.Paths)
//	}
package watch

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Event is delivered to the Events channel after the debounce window closes.
type Event struct {
	Paths []string // deduplicated paths that changed
}

// Options tunes the Watcher.
type Options struct {
	// Debounce is how long to wait after the last filesystem event before
	// emitting an Event. Default: 100ms.
	Debounce time.Duration
	// BufferSize is the Events channel capacity. Default: 16.
	BufferSize int
	// MaxPaths bounds unique paths retained during one debounce window.
	// Default: 1024. A full batch is emitted before accepting another path.
	MaxPaths int
}

func (o *Options) defaults() {
	if o.Debounce == 0 {
		o.Debounce = 100 * time.Millisecond
	}
	if o.BufferSize == 0 {
		o.BufferSize = 16
	}
	if o.MaxPaths == 0 {
		o.MaxPaths = 1024
	}
}

// Watcher watches one or more directories for changes.
type Watcher struct {
	inner  *fsnotify.Watcher
	events chan Event
	opts   Options
	done   chan struct{}
}

// New creates a Watcher. Call [Watcher.Add] to register directories, then
// read from [Watcher.Events].
func New(opts Options) (*Watcher, error) {
	if opts.Debounce < 0 {
		return nil, errors.New("watch: debounce must not be negative")
	}
	if opts.BufferSize < 0 {
		return nil, errors.New("watch: buffer size must not be negative")
	}
	if opts.MaxPaths < 0 {
		return nil, errors.New("watch: max paths must not be negative")
	}
	opts.defaults()
	inner, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watch: create watcher: %w", err)
	}
	w := &Watcher{
		inner:  inner,
		events: make(chan Event, opts.BufferSize),
		opts:   opts,
		done:   make(chan struct{}),
	}
	go w.run()
	return w, nil
}

// Add registers path (file or directory) for watching. Recursive watching
// of subdirectories is not automatic — call Add for each subdirectory.
func (w *Watcher) Add(path string) error {
	if err := w.inner.Add(path); err != nil {
		return fmt.Errorf("watch: add %s: %w", path, err)
	}
	return nil
}

// Remove unregisters path.
func (w *Watcher) Remove(path string) error {
	if err := w.inner.Remove(path); err != nil {
		return fmt.Errorf("fs/watch: remove %q: %w", path, err)
	}
	return nil
}

// Events returns the channel of debounced change events.
func (w *Watcher) Events() <-chan Event { return w.events }

// Close shuts down the watcher and drains resources.
func (w *Watcher) Close() error {
	err := w.inner.Close()
	<-w.done
	if err != nil {
		return fmt.Errorf("fs/watch: close: %w", err)
	}
	return nil
}

func (w *Watcher) run() {
	defer close(w.done)
	defer close(w.events)
	pending := make(map[string]struct{})
	var timer *time.Timer
	for open := true; open; {
		timer, open = w.step(timer, pending)
	}
	stopTimer(timer)
	w.emit(pending)
}

// step handles one inner fsnotify event; it reports false once either inner
// channel is closed, which ends the run loop.
func (w *Watcher) step(timer *time.Timer, pending map[string]struct{}) (*time.Timer, bool) {
	var timerC <-chan time.Time
	if timer != nil {
		timerC = timer.C
	}
	select {
	case ev, ok := <-w.inner.Events:
		if !ok {
			return timer, false
		}
		if ev.Op == fsnotify.Chmod {
			return timer, true // ignore pure permission changes
		}
		if _, exists := pending[ev.Name]; !exists && len(pending) == w.opts.MaxPaths {
			w.emit(pending)
		}
		pending[ev.Name] = struct{}{}
		return resetTimer(timer, w.opts.Debounce), true

	case <-w.inner.Errors:
		// Errors are surfaced by closing Events; callers can reopen a watcher.
		return timer, false

	case <-timerC:
		w.emit(pending)
		return nil, true
	}
}

func resetTimer(timer *time.Timer, debounce time.Duration) *time.Timer {
	if timer == nil {
		return time.NewTimer(debounce)
	}
	stopTimer(timer)
	timer.Reset(debounce)
	return timer
}

func stopTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func (w *Watcher) emit(pending map[string]struct{}) {
	paths := make([]string, 0, len(pending))
	for p := range pending {
		paths = append(paths, p)
		delete(pending, p)
	}
	sort.Strings(paths)

	if len(paths) == 0 {
		return
	}
	select {
	case w.events <- Event{Paths: paths}:
	default: // drop if channel full — caller is not keeping up
	}
}
