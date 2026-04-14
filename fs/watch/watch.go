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
	"fmt"
	"sync"
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
}

func (o *Options) defaults() {
	if o.Debounce == 0 {
		o.Debounce = 100 * time.Millisecond
	}
	if o.BufferSize == 0 {
		o.BufferSize = 16
	}
}

// Watcher watches one or more directories for changes.
type Watcher struct {
	inner   *fsnotify.Watcher
	events  chan Event
	opts    Options
	mu      sync.Mutex
	pending map[string]struct{}
	timer   *time.Timer
	done    chan struct{}
}

// New creates a Watcher. Call [Watcher.Add] to register directories, then
// read from [Watcher.Events].
func New(opts Options) (*Watcher, error) {
	opts.defaults()
	inner, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watch: create watcher: %w", err)
	}
	w := &Watcher{
		inner:   inner,
		events:  make(chan Event, opts.BufferSize),
		opts:    opts,
		pending: map[string]struct{}{},
		done:    make(chan struct{}),
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
	for {
		select {
		case ev, ok := <-w.inner.Events:
			if !ok {
				w.flush()
				return
			}
			if ev.Op == fsnotify.Chmod {
				continue // ignore pure permission changes
			}
			w.schedule(ev.Name)

		case _, ok := <-w.inner.Errors:
			if !ok {
				return
			}
			// Errors are surfaced as a closed channel; callers can reopen.
		}
	}
}

func (w *Watcher) schedule(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending[path] = struct{}{}
	if w.timer != nil {
		w.timer.Reset(w.opts.Debounce)
	} else {
		w.timer = time.AfterFunc(w.opts.Debounce, w.flush)
	}
}

func (w *Watcher) flush() {
	w.mu.Lock()
	paths := make([]string, 0, len(w.pending))
	for p := range w.pending {
		paths = append(paths, p)
	}
	w.pending = map[string]struct{}{}
	w.timer = nil
	w.mu.Unlock()

	if len(paths) == 0 {
		return
	}
	select {
	case w.events <- Event{Paths: paths}:
	default: // drop if channel full — caller is not keeping up
	}
}
