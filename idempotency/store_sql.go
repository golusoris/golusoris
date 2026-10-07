// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/validate"
)

const (
	// rowInFlight and rowCompleted are the persisted record states.
	rowInFlight  = 1
	rowCompleted = 2
	// maxClaimAttempts bounds the reserve-then-read loop when the competing
	// record vanishes between the two statements (release or sweep).
	maxClaimAttempts = 4
	// MaxSweepBatch caps the rows one [Sweeper.Sweep] call may delete.
	MaxSweepBatch = 10_000
)

// Sweeper deletes expired records in bounded batches. [MemoryStore] and the
// SQL-backed stores implement it; Redis expires records server-side.
type Sweeper interface {
	// Sweep deletes at most limit expired records and reports how many it removed.
	Sweep(ctx context.Context, limit int) (int64, error)
}

// sqlRecord is one persisted reservation or completed response.
type sqlRecord struct {
	state       int
	fingerprint string
	status      int
	header      []byte
	body        []byte
}

// completion carries the arguments of one Commit statement.
type completion struct {
	key         string
	token       string
	fingerprint string
	status      int
	header      []byte
	body        []byte
	now         time.Time
	expires     time.Time
}

// rowBackend is the dialect-specific statement set behind [sqlStore].
type rowBackend interface {
	// reserve inserts an in-flight record or takes over an expired one.
	reserve(ctx context.Context, key, token, fingerprint string, now, expires time.Time) (bool, error)
	// lookup returns the live record for key.
	lookup(ctx context.Context, key string, now time.Time) (sqlRecord, bool, error)
	// complete turns the in-flight record owned by token into a completed one.
	complete(ctx context.Context, done completion) (bool, error)
	// owner returns the fingerprint of the live in-flight record owned by token.
	owner(ctx context.Context, key, token string, now time.Time) (string, bool, error)
	// release deletes the in-flight record owned by token.
	release(ctx context.Context, key, token string) (bool, error)
	// sweep deletes at most limit records that expired at or before now.
	sweep(ctx context.Context, now time.Time, limit int) (int64, error)
}

// sqlStore implements the atomic [Store] contract over a [rowBackend].
type sqlStore struct {
	rows rowBackend
	clk  clockwork.Clock
}

func newSQLStore(rows rowBackend, clk clockwork.Clock) sqlStore {
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	return sqlStore{rows: rows, clk: clk}
}

// Claim atomically reserves key or reports the live record that owns it.
func (s *sqlStore) Claim(ctx context.Context, key string, fingerprint string, ttl time.Duration) (ClaimResult, error) {
	if err := validateClaim(ctx, key, fingerprint, ttl); err != nil {
		return ClaimResult{}, err
	}
	token := rand.Text()
	for range maxClaimAttempts {
		now := s.clk.Now()
		acquired, err := s.rows.reserve(ctx, key, token, fingerprint, now, now.Add(ttl))
		if err != nil {
			return ClaimResult{}, fmt.Errorf("idempotency: claim: %w", err)
		}
		if acquired {
			return ClaimResult{State: ClaimAcquired, Token: token}, nil
		}
		record, found, err := s.rows.lookup(ctx, key, now)
		if err != nil {
			return ClaimResult{}, fmt.Errorf("idempotency: claim: %w", err)
		}
		if found {
			return record.claimResult(fingerprint)
		}
	}
	return ClaimResult{}, fmt.Errorf("idempotency: claim: record changed during %d attempts", maxClaimAttempts)
}

// Commit completes the reservation owned by token.
func (s *sqlStore) Commit(
	ctx context.Context,
	key string,
	token string,
	fingerprint string,
	response CachedResponse,
	ttl time.Duration,
) error {
	if err := validateCommit(ctx, key, token, ttl); err != nil {
		return err
	}
	header, err := encodeHeader(response.Header)
	if err != nil {
		return err
	}
	now := s.clk.Now()
	done, err := s.rows.complete(ctx, completion{
		key:         key,
		token:       token,
		fingerprint: fingerprint,
		status:      response.StatusCode,
		header:      header,
		body:        nonNilBytes(response.Body),
		now:         now,
		expires:     now.Add(ttl),
	})
	if err != nil {
		return fmt.Errorf("idempotency: commit: %w", err)
	}
	if done {
		return nil
	}
	owned, found, err := s.rows.owner(ctx, key, token, now)
	if err != nil {
		return fmt.Errorf("idempotency: commit: %w", err)
	}
	if found && owned != fingerprint {
		return ErrFingerprintMismatch
	}
	return ErrReservationLost
}

// Release removes the in-flight reservation owned by token.
func (s *sqlStore) Release(ctx context.Context, key string, token string) error {
	if err := validateRelease(ctx, key, token); err != nil {
		return err
	}
	released, err := s.rows.release(ctx, key, token)
	if err != nil {
		return fmt.Errorf("idempotency: release: %w", err)
	}
	if !released {
		return ErrReservationLost
	}
	return nil
}

// Sweep deletes at most limit expired records.
func (s *sqlStore) Sweep(ctx context.Context, limit int) (int64, error) {
	if err := validateSweep(ctx, limit); err != nil {
		return 0, err
	}
	removed, err := s.rows.sweep(ctx, s.clk.Now(), limit)
	if err != nil {
		return 0, fmt.Errorf("idempotency: sweep: %w", err)
	}
	return removed, nil
}

func (r sqlRecord) claimResult(fingerprint string) (ClaimResult, error) {
	if r.fingerprint != fingerprint {
		return ClaimResult{State: ClaimFingerprintMismatch}, nil
	}
	switch r.state {
	case rowInFlight:
		return ClaimResult{State: ClaimInFlight}, nil
	case rowCompleted:
		header, err := decodeHeader(r.header)
		if err != nil {
			return ClaimResult{}, err
		}
		return ClaimResult{
			State:       ClaimCompleted,
			Fingerprint: r.fingerprint,
			Response:    CachedResponse{StatusCode: r.status, Header: header, Body: r.body},
		}, nil
	default:
		return ClaimResult{}, fmt.Errorf("idempotency: claim: unknown record state %d", r.state)
	}
}

func validateClaim(ctx context.Context, key, fingerprint string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("idempotency: claim: %w", err)
	}
	if key == "" || fingerprint == "" {
		return errors.New("idempotency: claim: key and fingerprint required")
	}
	if ttl <= 0 {
		return errors.New("idempotency: claim: positive TTL required")
	}
	return nil
}

func validateCommit(ctx context.Context, key, token string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("idempotency: commit: %w", err)
	}
	if key == "" || token == "" {
		return errors.New("idempotency: commit: key and token required")
	}
	if ttl <= 0 {
		return errors.New("idempotency: commit: positive TTL required")
	}
	return nil
}

func validateRelease(ctx context.Context, key, token string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("idempotency: release: %w", err)
	}
	if key == "" || token == "" {
		return errors.New("idempotency: release: key and token required")
	}
	return nil
}

func validateSweep(ctx context.Context, limit int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("idempotency: sweep: %w", err)
	}
	if limit <= 0 || limit > MaxSweepBatch {
		return fmt.Errorf("idempotency: sweep: limit %d outside 1..%d", limit, MaxSweepBatch)
	}
	return nil
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
