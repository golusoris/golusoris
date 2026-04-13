// Package leader defines the pluggable interface for single-leader
// election across a replica set. Backends:
//
//   - [leader/k8s]: client-go Lease. Native on Kubernetes.
//   - [leader/pg]:  PostgreSQL advisory lock. Works anywhere pg is.
//
// Apps pick ONE backend and wire its fx.Module. This package defines
// only the interface + shared [Callbacks]; it has no runtime
// dependency on any backend.
//
// Typical usage:
//
//	fx.New(
//	    golusoris.Core,
//	    golusoris.DB,
//	    leaderpg.Module("outbox-drainer", leader.Callbacks{
//	        OnStartedLeading: drainOutbox,
//	    }),
//	)
package leader

import "context"

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
