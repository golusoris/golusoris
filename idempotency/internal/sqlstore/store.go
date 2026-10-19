// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package sqlstore shares the atomic SQL store algorithm across dialects.
package sqlstore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/validate/nilcheck"
	"github.com/golusoris/golusoris/idempotency/internal/storeutil"
	"github.com/golusoris/golusoris/idempotency/store"
)

const (
	// RowInFlight and RowCompleted are the persisted record states.
	RowInFlight = 1
	// RowCompleted marks a stored response.
	RowCompleted = 2
	// maxClaimAttempts bounds the Reserve-then-read loop when the competing
	// record vanishes between the two statements (Release or Sweep).
	maxClaimAttempts = 4
)

// Record is one persisted reservation or completed response.
type Record struct {
	State       int
	Fingerprint string
	Status      int
	Header      []byte
	Body        []byte
}

// Completion carries the arguments of one Commit statement.
type Completion struct {
	Key         string
	Token       string
	Fingerprint string
	Status      int
	Header      []byte
	Body        []byte
	Now         time.Time
	Expires     time.Time
}

// RowBackend is the dialect-specific statement set behind [Store].
type RowBackend interface {
	// Reserve inserts an in-flight record or takes over an expired one.
	Reserve(ctx context.Context, key, token, fingerprint string, now, expires time.Time) (bool, error)
	// Lookup returns the live record for key.
	Lookup(ctx context.Context, key string, now time.Time) (Record, bool, error)
	// Complete turns the in-flight record owned by token into a completed one.
	Complete(ctx context.Context, done Completion) (bool, error)
	// Owner returns the fingerprint of the live in-flight record owned by token.
	Owner(ctx context.Context, key, token string, now time.Time) (string, bool, error)
	// Release deletes the in-flight record owned by token.
	Release(ctx context.Context, key, token string) (bool, error)
	// Sweep deletes at most limit records that expired at or before now.
	Sweep(ctx context.Context, now time.Time, limit int) (int64, error)
}

// Store implements the atomic [store.Store] contract over a [RowBackend].
type Store struct {
	rows RowBackend
	clk  clockwork.Clock
}

// New returns the shared algorithm over a dialect backend.
func New(rows RowBackend, clk clockwork.Clock) Store {
	if nilcheck.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	return Store{rows: rows, clk: clk}
}

// Claim atomically reserves key or reports the live record that owns it.
func (s *Store) Claim(ctx context.Context, key string, fingerprint string, ttl time.Duration) (store.ClaimResult, error) {
	if err := storeutil.ValidateClaim(ctx, key, fingerprint, ttl); err != nil {
		return store.ClaimResult{}, fmt.Errorf("idempotency: %w", err)
	}
	token := rand.Text()
	for range maxClaimAttempts {
		now := s.clk.Now()
		acquired, err := s.rows.Reserve(ctx, key, token, fingerprint, now, now.Add(ttl))
		if err != nil {
			return store.ClaimResult{}, fmt.Errorf("idempotency: claim: %w", err)
		}
		if acquired {
			return store.ClaimResult{State: store.ClaimAcquired, Token: token}, nil
		}
		record, found, err := s.rows.Lookup(ctx, key, now)
		if err != nil {
			return store.ClaimResult{}, fmt.Errorf("idempotency: claim: %w", err)
		}
		if found {
			return record.claimResult(fingerprint)
		}
	}
	return store.ClaimResult{}, fmt.Errorf("idempotency: claim: record changed during %d attempts", maxClaimAttempts)
}

// Commit completes the reservation owned by token.
func (s *Store) Commit(
	ctx context.Context,
	key string,
	token string,
	fingerprint string,
	response store.CachedResponse,
	ttl time.Duration,
) error {
	if err := storeutil.ValidateCommit(ctx, key, token, ttl); err != nil {
		return fmt.Errorf("idempotency: %w", err)
	}
	header, err := encodeHeader(response.Header)
	if err != nil {
		return err
	}
	now := s.clk.Now()
	done, err := s.rows.Complete(ctx, Completion{
		Key:         key,
		Token:       token,
		Fingerprint: fingerprint,
		Status:      response.StatusCode,
		Header:      header,
		Body:        nonNilBytes(response.Body),
		Now:         now,
		Expires:     now.Add(ttl),
	})
	if err != nil {
		return fmt.Errorf("idempotency: commit: %w", err)
	}
	if done {
		return nil
	}
	owned, found, err := s.rows.Owner(ctx, key, token, now)
	if err != nil {
		return fmt.Errorf("idempotency: commit: %w", err)
	}
	if found && owned != fingerprint {
		return store.ErrFingerprintMismatch
	}
	return store.ErrReservationLost
}

// Release removes the in-flight reservation owned by token.
func (s *Store) Release(ctx context.Context, key string, token string) error {
	if err := storeutil.ValidateRelease(ctx, key, token); err != nil {
		return fmt.Errorf("idempotency: %w", err)
	}
	released, err := s.rows.Release(ctx, key, token)
	if err != nil {
		return fmt.Errorf("idempotency: release: %w", err)
	}
	if !released {
		return store.ErrReservationLost
	}
	return nil
}

// Sweep deletes at most limit expired records.
func (s *Store) Sweep(ctx context.Context, limit int) (int64, error) {
	if err := storeutil.ValidateSweep(ctx, limit, store.MaxSweepBatch); err != nil {
		return 0, fmt.Errorf("idempotency: %w", err)
	}
	removed, err := s.rows.Sweep(ctx, s.clk.Now(), limit)
	if err != nil {
		return 0, fmt.Errorf("idempotency: sweep: %w", err)
	}
	return removed, nil
}

func (r Record) claimResult(fingerprint string) (store.ClaimResult, error) {
	if r.Fingerprint != fingerprint {
		return store.ClaimResult{State: store.ClaimFingerprintMismatch}, nil
	}
	switch r.State {
	case RowInFlight:
		return store.ClaimResult{State: store.ClaimInFlight}, nil
	case RowCompleted:
		header, err := decodeHeader(r.Header)
		if err != nil {
			return store.ClaimResult{}, err
		}
		return store.ClaimResult{
			State:       store.ClaimCompleted,
			Fingerprint: r.Fingerprint,
			Response:    store.CachedResponse{StatusCode: r.Status, Header: header, Body: r.Body},
		}, nil
	default:
		return store.ClaimResult{}, fmt.Errorf("idempotency: claim: unknown record state %d", r.State)
	}
}

func encodeHeader(header http.Header) ([]byte, error) {
	raw, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("idempotency: encode header: %w", err)
	}
	return raw, nil
}

func decodeHeader(raw []byte) (http.Header, error) {
	var header http.Header
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("idempotency: decode header: %w", err)
	}
	return header, nil
}

// nonNilBytes keeps an empty body distinct from SQL NULL.
func nonNilBytes(body []byte) []byte {
	if body == nil {
		return []byte{}
	}
	return body
}
