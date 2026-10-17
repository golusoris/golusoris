// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package storeutil validates backend operations before I/O.
package storeutil

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ValidateClaim checks arguments before touching the backend.
func ValidateClaim(ctx context.Context, key, fingerprint string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("claim: %w", err)
	}
	if key == "" || fingerprint == "" {
		return errors.New("claim: key and fingerprint required")
	}
	if ttl <= 0 {
		return errors.New("claim: positive TTL required")
	}
	return nil
}

// ValidateCommit checks arguments before touching the backend.
func ValidateCommit(ctx context.Context, key, token string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if key == "" || token == "" {
		return errors.New("commit: key and token required")
	}
	if ttl <= 0 {
		return errors.New("commit: positive TTL required")
	}
	return nil
}

// ValidateRelease checks arguments before touching the backend.
func ValidateRelease(ctx context.Context, key, token string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	if key == "" || token == "" {
		return errors.New("release: key and token required")
	}
	return nil
}

// ValidateSweep checks arguments before touching the backend.
func ValidateSweep(ctx context.Context, limit, maxSweepBatch int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("sweep: %w", err)
	}
	if limit <= 0 || limit > maxSweepBatch {
		return fmt.Errorf("sweep: limit %d outside 1..%d", limit, maxSweepBatch)
	}
	return nil
}
