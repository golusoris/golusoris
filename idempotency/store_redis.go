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
	"strconv"
	"time"

	"github.com/redis/rueidis"

	"github.com/golusoris/golusoris/core/validate"
)

// DefaultRedisPrefix namespaces [RedisStore] keys.
const DefaultRedisPrefix = "golusoris:idempotency:"

// Each script touches one key, so it runs unchanged on Redis Cluster.
const (
	redisClaimScript = `local cur = redis.call('HMGET', KEYS[1], 'state', 'fp', 'resp')
if not cur[1] then
  redis.call('HSET', KEYS[1], 'state', 'i', 'token', ARGV[2], 'fp', ARGV[1])
  redis.call('PEXPIRE', KEYS[1], ARGV[3])
  return {'acquired'}
end
if cur[2] ~= ARGV[1] then return {'mismatch'} end
if cur[1] == 'i' then return {'inflight'} end
return {'completed', cur[3]}`
	redisCommitScript = `local cur = redis.call('HMGET', KEYS[1], 'state', 'token', 'fp')
if cur[1] ~= 'i' or cur[2] ~= ARGV[1] then return 'lost' end
if cur[3] ~= ARGV[2] then return 'mismatch' end
redis.call('HSET', KEYS[1], 'state', 'c', 'resp', ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return 'ok'`
	redisReleaseScript = `local cur = redis.call('HMGET', KEYS[1], 'state', 'token')
if cur[1] ~= 'i' or cur[2] ~= ARGV[1] then return 'lost' end
redis.call('DEL', KEYS[1])
return 'ok'`
)

// RedisStore is a [Store] shared by every replica through Redis or Valkey.
// Each record is one hash whose PX expiry the server enforces, so it needs
// no sweeper. Lua scripts make claim, commit, and release atomic.
type RedisStore struct {
	client  rueidis.Client
	prefix  string
	claim   *rueidis.Lua
	commit  *rueidis.Lua
	release *rueidis.Lua
}

var _ Store = (*RedisStore)(nil)

// redisResponse is the JSON form of a completed [CachedResponse].
type redisResponse struct {
	Status int         `json:"status"`
	Header http.Header `json:"header"`
	Body   []byte      `json:"body"`
}

// NewRedisStore returns a RedisStore over client. An empty prefix uses
// [DefaultRedisPrefix].
func NewRedisStore(client rueidis.Client, prefix string) (*RedisStore, error) {
	if validate.IsNil(client) {
		return nil, errors.New("idempotency: redis store: nil client")
	}
	if prefix == "" {
		prefix = DefaultRedisPrefix
	}
	return &RedisStore{
		client:  client,
		prefix:  prefix,
		claim:   rueidis.NewLuaScript(redisClaimScript),
		commit:  rueidis.NewLuaScript(redisCommitScript),
		release: rueidis.NewLuaScript(redisReleaseScript),
	}, nil
}

// Claim atomically finds or reserves key.
func (s *RedisStore) Claim(ctx context.Context, key string, fingerprint string, ttl time.Duration) (ClaimResult, error) {
	if err := validateClaim(ctx, key, fingerprint, ttl); err != nil {
		return ClaimResult{}, err
	}
	token := rand.Text()
	reply, err := s.claim.Exec(ctx, s.client, []string{s.prefix + key},
		[]string{fingerprint, token, redisMillis(ttl)}).ToArray()
	if err != nil {
		return ClaimResult{}, fmt.Errorf("idempotency: claim: redis: %w", err)
	}
	return redisClaimResult(reply, fingerprint, token)
}

func redisClaimResult(reply []rueidis.RedisMessage, fingerprint, token string) (ClaimResult, error) {
	if len(reply) == 0 {
		return ClaimResult{}, errors.New("idempotency: claim: redis: empty reply")
	}
	outcome, err := reply[0].ToString()
	if err != nil {
		return ClaimResult{}, fmt.Errorf("idempotency: claim: redis outcome: %w", err)
	}
	switch outcome {
	case "acquired":
		return ClaimResult{State: ClaimAcquired, Token: token}, nil
	case "inflight":
		return ClaimResult{State: ClaimInFlight}, nil
	case "mismatch":
		return ClaimResult{State: ClaimFingerprintMismatch}, nil
	case "completed":
		if len(reply) < 2 {
			return ClaimResult{}, errors.New("idempotency: claim: redis: completed reply without response")
		}
		response, err := decodeRedisResponse(&reply[1])
		if err != nil {
			return ClaimResult{}, err
		}
		return ClaimResult{State: ClaimCompleted, Fingerprint: fingerprint, Response: response}, nil
	default:
		return ClaimResult{}, fmt.Errorf("idempotency: claim: redis: unknown outcome %q", outcome)
	}
}

func decodeRedisResponse(message *rueidis.RedisMessage) (CachedResponse, error) {
	raw, err := message.AsBytes()
	if err != nil {
		return CachedResponse{}, fmt.Errorf("idempotency: claim: redis response: %w", err)
	}
	var stored redisResponse
	if err := json.Unmarshal(raw, &stored); err != nil {
		return CachedResponse{}, fmt.Errorf("idempotency: claim: decode redis response: %w", err)
	}
	return CachedResponse{StatusCode: stored.Status, Header: stored.Header, Body: stored.Body}, nil
}

// Commit completes the reservation owned by token.
func (s *RedisStore) Commit(
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
	raw, err := json.Marshal(redisResponse{Status: response.StatusCode, Header: response.Header, Body: response.Body})
	if err != nil {
		return fmt.Errorf("idempotency: commit: encode redis response: %w", err)
	}
	outcome, err := s.commit.Exec(ctx, s.client, []string{s.prefix + key},
		[]string{token, fingerprint, string(raw), redisMillis(ttl)}).ToString()
	if err != nil {
		return fmt.Errorf("idempotency: commit: redis: %w", err)
	}
	return redisOutcome(outcome)
}

// Release removes the in-flight reservation owned by token.
func (s *RedisStore) Release(ctx context.Context, key string, token string) error {
	if err := validateRelease(ctx, key, token); err != nil {
		return err
	}
	outcome, err := s.release.Exec(ctx, s.client, []string{s.prefix + key}, []string{token}).ToString()
	if err != nil {
		return fmt.Errorf("idempotency: release: redis: %w", err)
	}
	return redisOutcome(outcome)
}

func redisOutcome(outcome string) error {
	switch outcome {
	case "ok":
		return nil
	case "lost":
		return ErrReservationLost
	case "mismatch":
		return ErrFingerprintMismatch
	default:
		return fmt.Errorf("idempotency: redis: unknown outcome %q", outcome)
	}
}

// redisMillis rounds ttl up to whole milliseconds so PEXPIRE never gets 0.
func redisMillis(ttl time.Duration) string {
	millis := ttl.Milliseconds()
	if time.Duration(millis)*time.Millisecond < ttl {
		millis++
	}
	return strconv.FormatInt(millis, 10)
}
