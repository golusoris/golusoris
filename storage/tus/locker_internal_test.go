// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tusd "github.com/tus/tusd/v2/pkg/handler"
)

func TestUploadLocker_MaintenanceSkipsWithoutInterruptingRequest(t *testing.T) {
	t.Parallel()
	locker := newUploadLocker()
	request, err := locker.NewLock("upload")
	if err != nil {
		t.Fatalf("NewLock: %v", err)
	}
	var interrupted atomic.Int32
	if err = request.Lock(context.Background(), func() { interrupted.Add(1) }); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if unlock, ok := locker.tryMaintenanceLock("upload"); ok {
		unlock()
		t.Fatal("maintenance acquired an active upload")
	}
	if interrupted.Load() != 0 {
		t.Fatalf("maintenance interrupted request %d times", interrupted.Load())
	}
	if err = request.Unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	unlock, ok := locker.tryMaintenanceLock("upload")
	if !ok {
		t.Fatal("maintenance did not acquire released upload")
	}
	unlock()
}

func TestUploadLocker_RequestContenderAsksHolderToRelease(t *testing.T) {
	t.Parallel()
	locker := newUploadLocker()
	first, err := locker.NewLock("upload")
	if err != nil {
		t.Fatalf("first NewLock: %v", err)
	}
	releaseRequested := make(chan struct{}, 1)
	if err = first.Lock(context.Background(), func() { releaseRequested <- struct{}{} }); err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	second, err := locker.NewLock("upload")
	if err != nil {
		t.Fatalf("second NewLock: %v", err)
	}
	lockResult := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { lockResult <- second.Lock(ctx, func() {}) }()
	select {
	case <-releaseRequested:
	case <-ctx.Done():
		t.Fatal("contender did not request release")
	}
	if err = first.Unlock(); err != nil {
		t.Fatalf("first Unlock: %v", err)
	}
	if err = <-lockResult; err != nil {
		t.Fatalf("second Lock: %v", err)
	}
	if err = second.Unlock(); err != nil {
		t.Fatalf("second Unlock: %v", err)
	}
}

func TestUploadLocker_ThreeContendersWakeCurrentHolder(t *testing.T) {
	t.Parallel()
	locker := newUploadLocker()
	first, err := locker.NewLock("upload")
	if err != nil {
		t.Fatalf("first NewLock: %v", err)
	}
	firstRelease := make(chan struct{}, 2)
	if err = first.Lock(context.Background(), func() { firstRelease <- struct{}{} }); err != nil {
		t.Fatalf("first Lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := make(chan lockResult, 2)
	secondRelease := make(chan struct{}, 1)
	thirdRelease := make(chan struct{}, 1)
	startLockContender(t, ctx, locker, "second", secondRelease, results)
	waitSignal(t, ctx, firstRelease, "second contender did not request first release")
	startLockContender(t, ctx, locker, "third", thirdRelease, results)
	waitSignal(t, ctx, firstRelease, "third contender did not request first release")
	if err = first.Unlock(); err != nil {
		t.Fatalf("first Unlock: %v", err)
	}

	winner := waitLockResult(t, ctx, results)
	currentRelease := secondRelease
	if winner.name == "third" {
		currentRelease = thirdRelease
	}
	waitSignal(t, ctx, currentRelease, "waiting contender did not request current release")
	if err = winner.lock.Unlock(); err != nil {
		t.Fatalf("winner Unlock: %v", err)
	}
	remainder := waitLockResult(t, ctx, results)
	if err = remainder.lock.Unlock(); err != nil {
		t.Fatalf("remainder Unlock: %v", err)
	}
}

type lockResult struct {
	name string
	lock tusd.Lock
	err  error
}

func startLockContender(
	t *testing.T,
	ctx context.Context,
	locker *uploadLocker,
	name string,
	release chan<- struct{},
	results chan<- lockResult,
) {
	t.Helper()
	lock, err := locker.NewLock("upload")
	if err != nil {
		t.Fatalf("%s NewLock: %v", name, err)
	}
	go func() {
		err := lock.Lock(ctx, func() { release <- struct{}{} })
		results <- lockResult{name: name, lock: lock, err: err}
	}()
}

func waitSignal(t *testing.T, ctx context.Context, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal(failure)
	}
}

func waitLockResult(t *testing.T, ctx context.Context, results <-chan lockResult) lockResult {
	t.Helper()
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatalf("%s Lock: %v", result.name, result.err)
		}
		return result
	case <-ctx.Done():
		t.Fatal("contender did not acquire lock")
		return lockResult{}
	}
}

func TestUploadLocker_ContenderHonorsDeadline(t *testing.T) {
	t.Parallel()
	locker := newUploadLocker()
	first, err := locker.NewLock("upload")
	if err != nil {
		t.Fatalf("first NewLock: %v", err)
	}
	releaseRequested := make(chan struct{}, 1)
	if err = first.Lock(context.Background(), func() { releaseRequested <- struct{}{} }); err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	second, err := locker.NewLock("upload")
	if err != nil {
		t.Fatalf("second NewLock: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = second.Lock(ctx, func() {}); !errors.Is(err, tusd.ErrLockTimeout) {
		t.Fatalf("second Lock error = %v, want ErrLockTimeout", err)
	}
	select {
	case <-releaseRequested:
	default:
		t.Fatal("timed-out contender did not request release")
	}
	if err = first.Unlock(); err != nil {
		t.Fatalf("first Unlock: %v", err)
	}
}

func TestUploadLocker_SerializesConcurrentRequests(t *testing.T) {
	t.Parallel()
	locker := newUploadLocker()
	const callers = 32
	var active atomic.Int32
	var overlap atomic.Bool
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			lock, err := locker.NewLock("upload")
			if err != nil {
				overlap.Store(true)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err = lock.Lock(ctx, func() {}); err != nil {
				overlap.Store(true)
				return
			}
			if active.Add(1) != 1 {
				overlap.Store(true)
			}
			active.Add(-1)
			if err = lock.Unlock(); err != nil {
				overlap.Store(true)
			}
		}()
	}
	wg.Wait()
	if overlap.Load() {
		t.Fatal("concurrent requests overlapped or failed")
	}
}
