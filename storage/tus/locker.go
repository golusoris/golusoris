// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"context"
	"sync"

	tusd "github.com/tus/tusd/v2/pkg/handler"
)

// uploadLocker coordinates tusd requests and non-interrupting maintenance.
// tusd contenders request cancellation from the current request; maintenance
// only skips a busy upload.
type uploadLocker struct {
	mu     sync.Mutex
	states map[string]*uploadLockState
}

type uploadLockState struct {
	mu    sync.Mutex
	wake  func()
	head  *uploadLockWaiter
	tail  *uploadLockWaiter
	owner *uploadLock
	held  bool
	refs  int
}

type uploadLockWaiter struct {
	previous       *uploadLockWaiter
	next           *uploadLockWaiter
	lock           *uploadLock
	ready          chan struct{}
	requestRelease func()
	queued         bool
	granted        bool
}

type uploadLock struct {
	locker   *uploadLocker
	id       string
	state    *uploadLockState
	acquired bool
}

func newUploadLocker() *uploadLocker {
	return &uploadLocker{states: make(map[string]*uploadLockState)}
}

// NewLock implements tusd's per-upload Locker contract.
func (l *uploadLocker) NewLock(id string) (tusd.Lock, error) {
	return &uploadLock{locker: l, id: id, state: l.retain(id)}, nil
}

func (l *uploadLocker) retain(id string) *uploadLockState {
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.states[id]
	if state == nil {
		state = &uploadLockState{}
		l.states[id] = state
	}
	state.refs++
	return state
}

func (l *uploadLocker) release(id string, state *uploadLockState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state.refs--
	if state.refs == 0 && l.states[id] == state {
		delete(l.states, id)
	}
}

// tryMaintenanceLock acquires id without interrupting an active tus request.
func (l *uploadLocker) tryMaintenanceLock(id string) (func(), bool) {
	state := l.retain(id)
	state.mu.Lock()
	if state.held {
		state.mu.Unlock()
		l.release(id, state)
		return nil, false
	}
	state.held = true
	state.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			state.mu.Lock()
			state.handoff()
			state.mu.Unlock()
			l.release(id, state)
		})
	}, true
}

// Lock implements [tusd.Lock]. A competing request asks the current request
// to stop, matching tusd's locker contract, then waits within ctx.
func (l *uploadLock) Lock(ctx context.Context, requestRelease func()) error {
	state := l.state
	state.mu.Lock()
	if !state.held {
		state.held = true
		state.owner = l
		state.wake = requestRelease
		l.acquired = true
		state.mu.Unlock()
		return nil
	}
	waiter := &uploadLockWaiter{
		lock:           l,
		ready:          make(chan struct{}),
		requestRelease: requestRelease,
		queued:         true,
	}
	state.enqueue(waiter)
	wake := state.wake
	state.mu.Unlock()
	if wake != nil {
		wake()
	}
	select {
	case <-waiter.ready:
		l.notifyWaiters()
		return nil
	case <-ctx.Done():
		state.mu.Lock()
		if waiter.granted {
			state.mu.Unlock()
			l.notifyWaiters()
			return nil
		}
		state.remove(waiter)
		state.mu.Unlock()
		l.locker.release(l.id, state)
		return tusd.ErrLockTimeout
	}
}

// Unlock implements [tusd.Lock].
func (l *uploadLock) Unlock() error {
	state := l.state
	state.mu.Lock()
	if !l.acquired {
		state.mu.Unlock()
		return nil
	}
	state.handoff()
	state.mu.Unlock()
	l.locker.release(l.id, state)
	return nil
}

func (s *uploadLockState) enqueue(waiter *uploadLockWaiter) {
	waiter.previous = s.tail
	if s.tail == nil {
		s.head = waiter
	} else {
		s.tail.next = waiter
	}
	s.tail = waiter
}

func (s *uploadLockState) remove(waiter *uploadLockWaiter) {
	if !waiter.queued {
		return
	}
	if waiter.previous == nil {
		s.head = waiter.next
	} else {
		waiter.previous.next = waiter.next
	}
	if waiter.next == nil {
		s.tail = waiter.previous
	} else {
		waiter.next.previous = waiter.previous
	}
	waiter.previous = nil
	waiter.next = nil
	waiter.queued = false
}

func (s *uploadLockState) handoff() {
	if s.owner != nil {
		s.owner.acquired = false
	}
	next := s.head
	if next == nil {
		s.held = false
		s.owner = nil
		s.wake = nil
		return
	}
	s.remove(next)
	next.granted = true
	next.lock.acquired = true
	s.owner = next.lock
	s.wake = next.requestRelease
	close(next.ready)
}

func (l *uploadLock) notifyWaiters() {
	state := l.state
	state.mu.Lock()
	wake := state.wake
	contended := state.owner == l && state.head != nil
	state.mu.Unlock()
	if contended && wake != nil {
		wake()
	}
}
