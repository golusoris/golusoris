// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package dra publishes the devices a node exposes as Kubernetes Dynamic
// Resource Allocation ResourceSlices (resource.k8s.io/v1, GA in Kubernetes
// 1.34), so the scheduler can place ResourceClaims by device attributes such
// as backend, device index or memory.
//
// It wraps the upstream ResourceSlice controller of
// k8s.io/dynamic-resource-allocation/resourceslice: the controller diffs the
// desired pool against the API server and creates, updates or deletes only
// what changed. Slices are owned by the Node, so node deletion garbage
// collects them; [Publisher.Stop] also deletes them.
//
// Scope is the publisher only. The kubelet plugin framework
// (k8s.io/dynamic-resource-allocation/kubeletplugin, which prepares claimed
// devices for containers) pulls k8s.io/kubelet, etcd client utilities and
// CDI specs and implements a driver's gRPC contract; a DRA driver that
// prepares devices wires it directly.
package dra

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/validate/content"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/client"
	"k8s.io/dynamic-resource-allocation/resourceslice"
)

var (
	// ErrNotStarted reports Update or Stop before Start.
	ErrNotStarted = errors.New("dra: publisher not started")
	// ErrStarted reports a second Start.
	ErrStarted = errors.New("dra: publisher already started")
)

// Options configures a [Publisher]. Config keys (env APP_K8S_DRA_*):
//
//	k8s.dra.enabled # master switch (default false)
//	k8s.dra.driver  # DRA driver name, a DNS subdomain (required)
//	k8s.dra.node    # node name (default podinfo NODE_NAME)
//	k8s.dra.pool    # pool name (default node name)
type Options struct {
	Enabled bool   `koanf:"enabled"`
	Driver  string `koanf:"driver"`
	Node    string `koanf:"node"`
	Pool    string `koanf:"pool"`
}

func (o Options) withDefaults() Options {
	if o.Pool == "" {
		o.Pool = o.Node
	}
	return o
}

func (o Options) validate() error {
	if len(o.Driver) > resourceapi.DriverNameMaxLength || len(content.IsDNS1123Subdomain(o.Driver)) > 0 {
		return fmt.Errorf("dra: driver %q must be a DNS subdomain of at most %d bytes", o.Driver, resourceapi.DriverNameMaxLength)
	}
	if errs := content.IsDNS1123Subdomain(o.Node); len(errs) > 0 {
		return fmt.Errorf("dra: node %q: %s", o.Node, strings.Join(errs, "; "))
	}
	if errs := content.IsDNS1123Subdomain(o.Pool); len(errs) > 0 {
		return fmt.Errorf("dra: pool %q: %s", o.Pool, strings.Join(errs, "; "))
	}
	return nil
}

// Publisher keeps one node-local pool of ResourceSlices in sync with the
// devices passed to [Publisher.Start] and [Publisher.Update].
type Publisher struct {
	opts     Options
	client   kubernetes.Interface
	logger   *slog.Logger
	disabled bool

	mu     sync.Mutex
	ctrl   *resourceslice.Controller
	cancel context.CancelFunc
	last   *resourceslice.DriverResources
}

// NewPublisher validates opts and returns an unstarted Publisher.
func NewPublisher(k kubernetes.Interface, opts Options, logger *slog.Logger) (*Publisher, error) {
	opts = opts.withDefaults()
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if k == nil || logger == nil {
		return nil, errors.New("dra: kubernetes client and logger are required")
	}
	return &Publisher{opts: opts, client: k, logger: logger}, nil
}

func (p *Publisher) resources(devices []Device) (*resourceslice.DriverResources, error) {
	pool, err := buildPool(devices)
	if err != nil {
		return nil, err
	}
	return &resourceslice.DriverResources{Pools: map[string]resourceslice.Pool{p.opts.Pool: pool}}, nil
}

// Start publishes devices and keeps them in sync until [Publisher.Stop].
// ctx bounds only the start (initial informer sync); the controller runs
// on its own context so it outlives fx's start deadline.
func (p *Publisher) Start(ctx context.Context, devices []Device) error {
	res, err := p.resources(devices)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctrl != nil {
		return ErrStarted
	}
	// The controller outlives fx's start context; Stop cancels it.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	runCtx = logr.NewContext(runCtx, logr.FromSlogHandler(p.logger.Handler()))
	abortOnStartDeadline := context.AfterFunc(ctx, cancel)
	ctrl, err := resourceslice.StartController(runCtx, resourceslice.Options{
		DriverName:   p.opts.Driver,
		KubeClient:   p.client,
		Owner:        &resourceslice.Owner{APIVersion: "v1", Kind: "Node", Name: p.opts.Node},
		Resources:    res,
		ErrorHandler: p.handleError,
	})
	if !abortOnStartDeadline() {
		err = errors.Join(err, fmt.Errorf("dra: start aborted: %w", context.Cause(ctx)))
	}
	if err != nil {
		cancel()
		ctrl.Stop()
		return fmt.Errorf("dra: start controller: %w", err)
	}
	p.ctrl, p.cancel, p.last = ctrl, cancel, res
	return nil
}

func (p *Publisher) handleError(ctx context.Context, err error, msg string) {
	p.logger.ErrorContext(ctx, "dra: "+msg,
		slog.String("driver", p.opts.Driver), slog.String("pool", p.opts.Pool), slog.String("error", err.Error()))
}

// Update replaces the published devices. Unchanged devices cause no API
// traffic. A disabled Publisher validates devices and discards them.
func (p *Publisher) Update(devices []Device) error {
	res, err := p.resources(devices)
	if err != nil || p.disabled {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctrl == nil {
		return ErrNotStarted
	}
	if equality.Semantic.DeepEqual(p.last, res) {
		return nil
	}
	p.ctrl.Update(res)
	p.last = res
	return nil
}

// Stop halts the controller and deletes this node's slices of the driver,
// both bounded by ctx. Stop on a disabled or already stopped Publisher is
// a no-op.
func (p *Publisher) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disabled || p.ctrl == nil {
		return nil
	}
	ctrl := p.ctrl
	p.cancel()
	p.ctrl, p.cancel, p.last = nil, nil, nil
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ctrl.Stop()
	}()
	select {
	case <-stopped:
	case <-ctx.Done():
		return fmt.Errorf("dra: controller did not stop before the stop deadline: %w", ctx.Err())
	}
	return p.deleteSlices(ctx)
}

func (p *Publisher) deleteSlices(ctx context.Context) error {
	api := client.New(p.client).ResourceSlices()
	selector := fields.Set{
		resourceapi.ResourceSliceSelectorDriver:   p.opts.Driver,
		resourceapi.ResourceSliceSelectorNodeName: p.opts.Node,
	}.String()
	list, err := api.List(ctx, metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		return fmt.Errorf("dra: list slices for cleanup: %w", err)
	}
	var errs []error
	for i := range list.Items {
		s := &list.Items[i]
		// Re-check the selector: not every client honours field selectors.
		if s.Spec.Driver != p.opts.Driver || s.Spec.NodeName == nil || *s.Spec.NodeName != p.opts.Node {
			continue
		}
		pre := metav1.Preconditions{UID: &s.UID}
		if delErr := api.Delete(ctx, s.Name, metav1.DeleteOptions{Preconditions: &pre}); delErr != nil && !apierrors.IsNotFound(delErr) {
			errs = append(errs, fmt.Errorf("dra: delete slice %s: %w", s.Name, delErr))
		}
	}
	return errors.Join(errs...)
}
