// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package leader defines the pluggable interface for single-leader
// election across a replica set. Backends:
//
//   - [leader/k8s]:    client-go Lease. Native on Kubernetes.
//   - [leader/pg]:     PostgreSQL advisory lock. Works anywhere pg is.
//   - [leader/always]: always leader, for standalone single-replica runs.
//
// Apps pick ONE backend and wire its fx.Module. This package defines
// only the interface, shared [Callbacks] and the [Status] observer; it has
// no runtime dependency on any backend.
//
// Each backend's NamedModule(key, cb) adds a further election per key,
// configured under leader.elections.<key>, and provides a *[Status]
// tagged `name:"<key>"`, so one process can lead several singleton tasks
// independently.
//
// Typical usage:
//
//	fx.New(
//	    golusoris.Core,
//	    golusoris.DB,
//	    leaderpg.Module(leader.Callbacks{ // lock name from leader.name
//	        OnStartedLeading: drainOutbox,
//	    }),
//	)
package leader

import (
	"context"
	"sync/atomic"
)

// Callbacks fires on lease state changes.
//
// OnStartedLeading runs in a fresh goroutine when this replica becomes
// leader. The supplied ctx is canceled when the lease is lost — handler
// code MUST return promptly on cancellation or risk concurrent leaders.
//
// OnStoppedLeading runs after OnStartedLeading's ctx is fully drained.
//
// OnNewLeader fires whenever any replica (including this one) becomes
// leader. Useful for logs + metrics.
type Callbacks struct {
	OnStartedLeading func(ctx context.Context)
	OnStoppedLeading func()
	OnNewLeader      func(identity string)
}

// Elector is the common interface both backends implement. Apps rarely
// touch it directly — they wire a backend's fx.Module.
type Elector interface {
	// Run blocks until ctx is canceled, driving the elector's lease
	// lifecycle + invoking Callbacks as transitions happen.
	Run(ctx context.Context, cb Callbacks) error
}

// Status reports whether this replica currently holds a leadership term.
// The zero value is ready and reports false.
type Status struct {
	terms   atomic.Uint64
	leading atomic.Uint64 // current term, 0 when not leading
}

// NewStatus returns a Status that reports false until a term starts.
func NewStatus() *Status { return &Status{} }

// IsLeader reports whether a term is active.
func (s *Status) IsLeader() bool { return s.leading.Load() != 0 }

// Observe returns cb wrapped so s reports true from OnStartedLeading until
// that term's context ends or OnStoppedLeading runs.
func (s *Status) Observe(cb Callbacks) Callbacks {
	out := cb
	out.OnStartedLeading = func(ctx context.Context) {
		term := s.terms.Add(1)
		s.leading.Store(term)
		// The term's own id keeps a late cancel from clearing a newer term.
		context.AfterFunc(ctx, func() { s.leading.CompareAndSwap(term, 0) })
		if cb.OnStartedLeading != nil {
			cb.OnStartedLeading(ctx)
		}
	}
	out.OnStoppedLeading = func() {
		s.leading.Store(0)
		if cb.OnStoppedLeading != nil {
			cb.OnStoppedLeading()
		}
	}
	return out
}
