// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build soak

package twotier_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/twotier"
	"github.com/golusoris/golusoris/testutil/load"
	redistest "github.com/golusoris/golusoris/testutil/redis"
)

func TestCacheTwoTier_Soak(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	l1, err := memory.NewForTest(5000, 5*time.Minute)
	if err != nil {
		t.Fatalf("memory.NewForTest: %v", err)
	}

	opts := twotier.Options{
		L2:    twotier.L2Redis,
		L1TTL: time.Minute,
		L2TTL: 5 * time.Minute,
	}
	tt, err := twotier.New(l1, opts, slog.New(slog.DiscardHandler), twotier.WithRedis(client))
	if err != nil {
		t.Fatalf("twotier.New: %v", err)
	}
	t.Cleanup(tt.Listen())
	typed := twotier.NewTyped[string](tt, "soak")

	loader := func(context.Context) (string, error) {
		return "origin-data", nil
	}

	var counter atomic.Int64
	op := func(ctx context.Context) error {
		id := counter.Add(1)
		key := fmt.Sprintf("k-%d", id%500)
		opCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		return executeCacheOp(opCtx, typed, key, id, loader)
	}

	load.RunSoak(t, load.SoakOptions{
		Name:        "cache/twotier",
		Pace:        5 * time.Millisecond,
		Concurrency: 2,
	}, op)
}

func executeCacheOp(
	ctx context.Context,
	typed *twotier.Typed[string],
	key string,
	id int64,
	loader func(context.Context) (string, error),
) error {
	switch id % 10 {
	case 0:
		if err := typed.Delete(ctx, key); err != nil {
			return fmt.Errorf("twotier soak delete: %w", err)
		}
	case 1, 2:
		if err := typed.Set(ctx, key, "val"); err != nil {
			return fmt.Errorf("twotier soak set: %w", err)
		}
	default:
		if _, err := typed.Get(ctx, key, loader); err != nil {
			return fmt.Errorf("twotier soak get: %w", err)
		}
	}
	return nil
}
