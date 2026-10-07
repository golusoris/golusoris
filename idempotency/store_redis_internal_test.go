// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency

import (
	"errors"
	"testing"
	"time"

	"github.com/redis/rueidis"
)

func TestRedisMillis_RoundsUp(t *testing.T) {
	t.Parallel()
	tests := map[time.Duration]string{
		time.Nanosecond:                          "1",
		time.Millisecond:                         "1",
		time.Millisecond + time.Nanosecond:       "2",
		24 * time.Hour:                           "86400000",
		1500*time.Millisecond + time.Microsecond: "1501",
	}
	for ttl, want := range tests {
		if got := redisMillis(ttl); got != want {
			t.Errorf("redisMillis(%v) = %s; want %s", ttl, got, want)
		}
	}
}

func TestRedisOutcome(t *testing.T) {
	t.Parallel()
	if err := redisOutcome("ok"); err != nil {
		t.Fatalf("ok = %v", err)
	}
	if err := redisOutcome("lost"); !errors.Is(err, ErrReservationLost) {
		t.Fatalf("lost = %v", err)
	}
	if err := redisOutcome("mismatch"); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("mismatch = %v", err)
	}
	if err := redisOutcome("weird"); err == nil {
		t.Fatal("unknown outcome accepted")
	}
}

func TestRedisClaimResult_RejectsEmptyReply(t *testing.T) {
	t.Parallel()
	if _, err := redisClaimResult([]rueidis.RedisMessage{}, "fp", "token"); err == nil {
		t.Fatal("empty reply accepted")
	}
}
