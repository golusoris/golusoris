// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hook

import (
	"context"
	"os"

	"github.com/golusoris/golusoris/leader"
)

// Lead runs one leadership term: OnNewLeader, then OnStartedLeading with a
// context canceled when the term ends, then blocks until ctx ends and runs
// OnStoppedLeading.
func Lead(ctx context.Context, identity string, cb leader.Callbacks) {
	if cb.OnNewLeader != nil {
		cb.OnNewLeader(identity)
	}
	leaderCtx, cancel := context.WithCancel(ctx)
	if cb.OnStartedLeading != nil {
		cb.OnStartedLeading(leaderCtx)
	}
	<-ctx.Done()
	cancel()
	if cb.OnStoppedLeading != nil {
		cb.OnStoppedLeading()
	}
}

// Identity returns identity when set, else the hostname, else "unknown".
func Identity(identity string) string {
	if identity != "" {
		return identity
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}
