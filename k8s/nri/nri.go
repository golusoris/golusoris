// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package nri is a small, typed scaffold over [github.com/containerd/nri]'s
// plugin stub: register a plugin under a configurable name/index, implement
// only the pod/container lifecycle hooks the fleet needs as plain functions
// (no interface-satisfaction ceremony), and get every hook call bounded by a
// context timeout automatically.
//
// An app supplies its callbacks as [Hooks] and wires the fx module:
//
//	fx.New(
//	    golusoris.Core,
//	    nri.Module,
//	    nri.ProvideHooks(nri.Hooks{
//	        CreateContainer:  myCreateContainer,
//	        StartContainer:   myStartContainer,
//	        StopContainer:    myStopContainer,
//	        RemovePodSandbox: myRemovePodSandbox,
//	    }),
//	).Run()
//
// Config keys live under the "nri" prefix (Name, Index, HookTimeout).
//
// Own go.mod sub-module: github.com/containerd/nri's pkg/api unconditionally
// imports tetratelabs/wazero (a WASM runtime, for its optional WASM-plugin
// support) plus containerd/ttrpc and opencontainers/runtime-spec — none of
// which the root module's controller-runtime/client-go graph already carries.
// Splitting it out keeps that footprint out of every app that doesn't run as
// an NRI plugin, matching the framework's convention for heavy/native-dep
// packages (see README.md "Specialty sub-modules").
package nri

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/containerd/nri/pkg/api"
	nristub "github.com/containerd/nri/pkg/stub"
)

// CreateContainerFunc adjusts a container being created. It may request
// resource-spec adjustments to the container itself and updates to other
// already-running containers.
type CreateContainerFunc func(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)

// StartContainerFunc observes a container starting. It cannot adjust the
// container; it can only react (e.g. update bookkeeping, emit metrics).
type StartContainerFunc func(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) error

// StopContainerFunc observes a container stopping. It may request updates to
// other still-running containers in response.
type StopContainerFunc func(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) ([]*api.ContainerUpdate, error)

// RemovePodSandboxFunc observes a pod sandbox being removed.
type RemovePodSandboxFunc func(ctx context.Context, pod *api.PodSandbox) error

// Hooks are the pod/container lifecycle callbacks a fleet plugin implements.
// All four fields are mandatory — [New] rejects a zero-value Hooks — because
// they are the baseline set every current fleet consumer needs; extend this
// struct (and the compile-time interface assertions below) when a consumer
// needs a hook NRI supports but this package does not yet expose.
type Hooks struct {
	// CreateContainer is called for every container NRI is asked to create.
	CreateContainer CreateContainerFunc
	// StartContainer is called after a container starts.
	StartContainer StartContainerFunc
	// StopContainer is called before a container stops.
	StopContainer StopContainerFunc
	// RemovePodSandbox is called after a pod sandbox is removed.
	RemovePodSandbox RemovePodSandboxFunc
}

// validate reports the first missing hook, if any.
func (h Hooks) validate() error {
	switch {
	case h.CreateContainer == nil:
		return errors.New("nri: Hooks.CreateContainer is required")
	case h.StartContainer == nil:
		return errors.New("nri: Hooks.StartContainer is required")
	case h.StopContainer == nil:
		return errors.New("nri: Hooks.StopContainer is required")
	case h.RemovePodSandbox == nil:
		return errors.New("nri: Hooks.RemovePodSandbox is required")
	default:
		return nil
	}
}

// plugin adapts [Hooks] to the NRI stub's typed handler interfaces, bounding
// every call by timeout so a wedged hook can't hang the containerd request
// that dispatched it (HISS-02: context timeout on all I/O).
type plugin struct {
	hooks   Hooks
	timeout time.Duration
}

var (
	_ nristub.CreateContainerInterface = (*plugin)(nil)
	_ nristub.StartContainerInterface  = (*plugin)(nil)
	_ nristub.StopContainerInterface   = (*plugin)(nil)
	_ nristub.RemovePodInterface       = (*plugin)(nil)
)

// boundedResult carries fn's return value through a channel exclusively —
// never through a variable the caller also touches — so a timed-out call
// stays race-free even though fn's goroutine keeps running after bounded
// returns (Go has no way to force-preempt it).
type boundedResult[T any] struct {
	val T
	err error
}

// bounded runs fn under a child context cancelled after timeout, returning a
// wrapped [context.DeadlineExceeded] if fn has not returned by then, or a
// wrapped [context.Canceled] if ctx itself was cancelled by the caller before
// the timeout elapsed (HISS-02: context timeout on all I/O). hook names the
// calling hook (e.g. "CreateContainer") so a timeout, cancellation, or panic
// error identifies which callback misbehaved. fn runs in its own goroutine
// that recovers any panic and reports it as an error instead of crashing the
// plugin process — an NRI plugin runs as its own OS process, so an unrecovered
// panic in a hook would take the whole plugin down, not just this request.
// The zero value of T is returned on timeout, cancellation, or panic.
func bounded[T any](ctx context.Context, timeout time.Duration, hook string, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan boundedResult[T], 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				var zero T
				done <- boundedResult[T]{val: zero, err: fmt.Errorf("nri: hook %s panicked: %v", hook, r)}
			}
		}()
		val, err := fn(ctx)
		done <- boundedResult[T]{val: val, err: err}
	}()

	select {
	case <-ctx.Done():
		var zero T
		if errors.Is(ctx.Err(), context.Canceled) {
			return zero, fmt.Errorf("nri: hook %s: caller context cancelled: %w", hook, ctx.Err())
		}
		return zero, fmt.Errorf("nri: hook %s exceeded %s timeout: %w", hook, timeout, ctx.Err())
	case r := <-done:
		return r.val, r.err
	}
}

// createResult bundles CreateContainer's two success values so bounded's
// single-T channel can carry them together.
type createResult struct {
	adjust  *api.ContainerAdjustment
	updates []*api.ContainerUpdate
}

// CreateContainer implements [nristub.CreateContainerInterface].
func (p *plugin) CreateContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	r, err := bounded(ctx, p.timeout, "CreateContainer", func(ctx context.Context) (createResult, error) {
		adjust, updates, err := p.hooks.CreateContainer(ctx, pod, ctr)
		return createResult{adjust: adjust, updates: updates}, err
	})
	return r.adjust, r.updates, err
}

// StartContainer implements [nristub.StartContainerInterface].
func (p *plugin) StartContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) error {
	_, err := bounded(ctx, p.timeout, "StartContainer", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.hooks.StartContainer(ctx, pod, ctr)
	})
	return err
}

// StopContainer implements [nristub.StopContainerInterface].
func (p *plugin) StopContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) ([]*api.ContainerUpdate, error) {
	return bounded(ctx, p.timeout, "StopContainer", func(ctx context.Context) ([]*api.ContainerUpdate, error) {
		return p.hooks.StopContainer(ctx, pod, ctr)
	})
}

// RemovePodSandbox implements [nristub.RemovePodInterface].
func (p *plugin) RemovePodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	_, err := bounded(ctx, p.timeout, "RemovePodSandbox", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.hooks.RemovePodSandbox(ctx, pod)
	})
	return err
}

// Registration is a constructed, not-yet-started NRI plugin registration.
// Run it under the fx lifecycle via [Module], or call Stub directly for a
// non-fx caller.
type Registration struct {
	// Stub is the underlying NRI plugin stub — Start/Run/Stop/Wait.
	Stub nristub.Stub
}

// stubFactory matches [nristub.New]'s signature. Production code always
// passes nristub.New; tests inject a fake to avoid a real containerd socket.
type stubFactory func(p any, opts ...nristub.Option) (nristub.Stub, error)

// newRegistration validates hooks and options, builds the adapter, and asks
// factory to create the underlying stub. factory does no I/O by itself —
// [nristub.New] only validates and assigns identity; the real connection
// happens on Stub.Start/Run — so this is safe to call outside a cluster.
func newRegistration(opts Options, hooks Hooks, factory stubFactory) (*Registration, error) {
	if err := hooks.validate(); err != nil {
		return nil, err
	}
	if opts.HookTimeout <= 0 {
		return nil, errors.New("nri: Options.HookTimeout must be > 0")
	}
	// nristub.stub.ensureIdentity only honors an explicit Name if Index is
	// also set — given Name alone, it discards it and re-derives both from
	// the binary's own filename (the "<idx>-<name>" convention containerd
	// uses when it launches a plugin directly), which silently ignores our
	// caller's intent instead of erroring. Require both or neither.
	if (opts.Name == "") != (opts.Index == "") {
		return nil, errors.New("nri: Options.Name and Options.Index must both be set or both be empty")
	}

	p := &plugin{hooks: hooks, timeout: opts.HookTimeout}

	var stubOpts []nristub.Option
	if opts.Name != "" {
		stubOpts = append(stubOpts, nristub.WithPluginName(opts.Name))
	}
	if opts.Index != "" {
		stubOpts = append(stubOpts, nristub.WithPluginIdx(opts.Index))
	}

	s, err := factory(p, stubOpts...)
	if err != nil {
		return nil, fmt.Errorf("nri: new stub: %w", err)
	}
	return &Registration{Stub: s}, nil
}

// New builds and registers an NRI plugin against the real containerd socket
// dialer (no connection is made until Registration.Stub.Start or .Run is
// called). Apps using fx get this via [Module]; call New directly otherwise.
func New(opts Options, hooks Hooks) (*Registration, error) {
	return newRegistration(opts, hooks, nristub.New)
}
