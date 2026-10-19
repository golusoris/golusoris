// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package store provides atomic idempotency contracts and an in-memory implementation.
package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/validate/nilcheck"
	"github.com/golusoris/golusoris/idempotency/internal/storeutil"
)

// CachedResponse is the stored representation of a completed response.
type CachedResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// ClaimState describes the atomic result of [Store.Claim].
type ClaimState uint8

const (
	// ClaimAcquired grants the caller ownership of a new in-flight reservation.
	ClaimAcquired ClaimState = iota + 1
	// ClaimInFlight reports that another request owns the reservation.
	ClaimInFlight
	// ClaimCompleted returns the response and fingerprint from a completed request.
	ClaimCompleted
	// ClaimFingerprintMismatch reports reuse of a key for a different payload.
	ClaimFingerprintMismatch
)

// ClaimResult is the atomic result of claiming a scoped idempotency key.
type ClaimResult struct {
	State       ClaimState
	Token       string
	Fingerprint string
	Response    CachedResponse
}

// Store atomically reserves and completes scoped idempotency keys.
type Store interface {
	// Claim atomically returns an existing record or creates an in-flight
	// reservation. Token is set only when State is ClaimAcquired.
	Claim(ctx context.Context, key string, fingerprint string, ttl time.Duration) (ClaimResult, error)
	// Commit completes the reservation owned by token. A failed Commit leaves
	// the reservation releasable by the same token.
	Commit(
		ctx context.Context,
		key string,
		token string,
		fingerprint string,
		resp CachedResponse,
		ttl time.Duration,
	) error
	// Release removes an in-flight reservation only when token still owns it.
	Release(ctx context.Context, key string, token string) error
}

// MaxSweepBatch caps the rows one [Sweeper.Sweep] call may delete.
const MaxSweepBatch = 10_000

// Sweeper deletes expired records in bounded batches. [MemoryStore] and the
// SQL-backed stores implement it; Redis expires records server-side.
type Sweeper interface {
	// Sweep deletes at most limit expired records and reports how many it removed.
	Sweep(ctx context.Context, limit int) (int64, error)
}

// MemoryStore is a simple in-memory [Store] for tests. Not suitable for
// multi-replica deployments.
type MemoryStore struct {
	mu       sync.Mutex
	entries  map[string]memEntry
	clk      clockwork.Clock
	sequence uint64
}

type entryState uint8

const (
	entryInFlight entryState = iota + 1
	entryCompleted
)

type memEntry struct {
	state       entryState
	token       string
	fingerprint string
	resp        CachedResponse
	expiresAt   time.Time
}

// NewMemoryStore returns an empty MemoryStore using the real clock.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithClock(clockwork.NewRealClock())
}

// NewMemoryStoreWithClock returns an empty MemoryStore with an injected clock.
func NewMemoryStoreWithClock(clk clockwork.Clock) *MemoryStore {
	if nilcheck.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	return &MemoryStore{entries: map[string]memEntry{}, clk: clk}
}

// Claim atomically finds or reserves key.
func (s *MemoryStore) Claim(
	ctx context.Context,
	key string,
	fingerprint string,
	ttl time.Duration,
) (ClaimResult, error) {
	if err := storeutil.ValidateClaim(ctx, key, fingerprint, ttl); err != nil {
		return ClaimResult{}, fmt.Errorf("idempotency: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	entry, found := s.entries[key]
	if found && !now.Before(entry.expiresAt) {
		delete(s.entries, key)
		found = false
	}
	if !found {
		s.sequence++
		token := strconv.FormatUint(s.sequence, 10)
		s.entries[key] = memEntry{
			state:       entryInFlight,
			token:       token,
			fingerprint: fingerprint,
			expiresAt:   now.Add(ttl),
		}
		return ClaimResult{State: ClaimAcquired, Token: token}, nil
	}
	if entry.fingerprint != fingerprint {
		return ClaimResult{State: ClaimFingerprintMismatch}, nil
	}
	if entry.state == entryInFlight {
		return ClaimResult{State: ClaimInFlight}, nil
	}
	return ClaimResult{
		State:       ClaimCompleted,
		Fingerprint: entry.fingerprint,
		Response:    cloneResponse(entry.resp),
	}, nil
}

// Commit completes the reservation owned by token.
func (s *MemoryStore) Commit(
	ctx context.Context,
	key string,
	token string,
	fingerprint string,
	response CachedResponse,
	ttl time.Duration,
) error {
	if err := storeutil.ValidateCommit(ctx, key, token, ttl); err != nil {
		return fmt.Errorf("idempotency: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	entry, found := s.entries[key]
	if !found || entry.state != entryInFlight || entry.token != token || !now.Before(entry.expiresAt) {
		return ErrReservationLost
	}
	if entry.fingerprint != fingerprint {
		return ErrFingerprintMismatch
	}
	s.entries[key] = memEntry{
		state:       entryCompleted,
		token:       token,
		fingerprint: fingerprint,
		resp:        cloneResponse(response),
		expiresAt:   now.Add(ttl),
	}
	return nil
}

// Release removes the in-flight reservation owned by token.
func (s *MemoryStore) Release(ctx context.Context, key string, token string) error {
	if err := storeutil.ValidateRelease(ctx, key, token); err != nil {
		return fmt.Errorf("idempotency: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, found := s.entries[key]
	if !found || entry.state != entryInFlight || entry.token != token {
		return ErrReservationLost
	}
	delete(s.entries, key)
	return nil
}

// Sweep deletes at most limit expired entries.
func (s *MemoryStore) Sweep(ctx context.Context, limit int) (int64, error) {
	if err := storeutil.ValidateSweep(ctx, limit, MaxSweepBatch); err != nil {
		return 0, fmt.Errorf("idempotency: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	var removed int64
	for key, entry := range s.entries {
		if removed == int64(limit) {
			break
		}
		if !now.Before(entry.expiresAt) {
			delete(s.entries, key)
			removed++
		}
	}
	return removed, nil
}

func cloneResponse(response CachedResponse) CachedResponse {
	response.Header = response.Header.Clone()
	response.Body = bytes.Clone(response.Body)
	return response
}

var (
	// ErrConflict reports an in-flight request for the same scoped key.
	ErrConflict = errors.New("idempotency: request with this key is still in progress")
	// ErrFingerprintMismatch reports key reuse with a different payload.
	ErrFingerprintMismatch = errors.New("idempotency: key reused with a different payload")
	// ErrRequestBodyTooLarge reports a payload larger than MaxRequestBody.
	ErrRequestBodyTooLarge = errors.New("idempotency: request body too large")
	// ErrReservationLost reports that a token no longer owns its reservation.
	ErrReservationLost = errors.New("idempotency: reservation lost")
)
