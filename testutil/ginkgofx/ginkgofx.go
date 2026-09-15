// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ginkgofx wires a go.uber.org/fx application into a
// github.com/onsi/ginkgo/v2 spec suite's lifecycle: it starts the app
// before specs run and stops it afterwards, each call bounded by an
// explicit context timeout (HISS-02), and gives specs a way to resolve
// fx-provided values via Populate.
//
// Root-module package, not a nested go.mod: github.com/onsi/ginkgo/v2 and
// github.com/onsi/gomega are pure Go with no embedded binaries — unlike
// testutil/pact's pact-go dependency, which embeds a ~40 MB Ruby
// standalone binary and is the actual reason that package gets its own
// go.mod. ginkgo/gomega already sit in this module's dependency graph
// (pulled in indirectly via sigs.k8s.io/controller-runtime's own test
// requirements), so promoting them to direct requirements adds no new
// build weight, and every other test-only heavy dependency in testutil/
// (testcontainers-go, tsenart/vegeta, leanovate/gopter, go-mutesting,
// gofakeit) already lives directly in the root go.mod for the same
// reason. Root placement also keeps this package inside `make
// verify-all`'s default gate (MODULES := . core in the root Makefile);
// the nested/native sub-modules build only on demand and are not part of
// that default gate, which would be the wrong trade for a package that
// carries its own tests.
//
// # Suite-scoped: one app for the whole spec suite
//
//	var svc *myservice.Service
//	var h = ginkgofx.Setup(myservice.Module, ginkgofx.Populate(&svc))
//
//	var _ = Describe("MyService", func() {
//	    It("does the thing", func() {
//	        Expect(h.App().Err()).NotTo(HaveOccurred())
//	        Expect(svc.DoThing()).To(Succeed())
//	    })
//	})
//
// Call Setup (or SetupWithOptions) once per suite, at tree-construction
// time — Ginkgo allows only one BeforeSuite/AfterSuite handler per suite,
// same restriction as calling ginkgo.BeforeSuite directly.
//
// # Spec-scoped: a fresh app per It
//
//	var svc *myservice.Service
//
//	var _ = Describe("MyService", func() {
//	    _ = ginkgofx.SetupEach(myservice.Module, ginkgofx.Populate(&svc))
//
//	    It("does the thing", func() {
//	        Expect(svc.DoThing()).To(Succeed())
//	    })
//	})
//
// Call SetupEach from inside the Describe/Context it should apply to, the
// same as calling ginkgo.BeforeEach directly: Ginkgo scopes a Setup node
// to the container it is registered from, so a package-level SetupEach
// call runs before every spec in the whole suite, not just the ones you
// meant it for.
package ginkgofx

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"go.uber.org/fx"
)

// Default bounds for Start/Stop when Options leaves them zero. Scalar
// constants per HISS-02 (explicit context timeout on all I/O).
const (
	DefaultStartTimeout = 15 * time.Second
	DefaultStopTimeout  = 15 * time.Second
)

// Options bounds the fx app lifecycle calls a Harness performs.
type Options struct {
	// StartTimeout bounds fx.App.Start. Zero (the default Options{}) uses
	// DefaultStartTimeout.
	StartTimeout time.Duration
	// StopTimeout bounds fx.App.Stop. Zero uses DefaultStopTimeout.
	StopTimeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.StartTimeout <= 0 {
		o.StartTimeout = DefaultStartTimeout
	}
	if o.StopTimeout <= 0 {
		o.StopTimeout = DefaultStopTimeout
	}
	return o
}

// Harness holds the fx.App a Setup/SetupEach call manages. App returns nil
// until the registered start hook has run at least once.
type Harness struct {
	app *fx.App
}

// App returns the underlying fx.App, or nil before the first start hook
// has run.
func (h *Harness) App() *fx.App {
	return h.app
}

// Populate is an fx.Populate alias for resolving fx-provided values into
// specs, named for discoverability alongside Setup/SetupEach.
func Populate(targets ...any) fx.Option {
	return fx.Populate(targets...)
}

// Setup registers BeforeSuite/AfterSuite hooks that start an fx app built
// from opts before any spec runs, and stop it after every spec has run.
// Call it at tree-construction time (top-level or inside a Describe),
// before RunSpecs. fx.Populate targets in opts are resolved as soon as
// fx.New(opts...) returns, before Start is even attempted, so specs may
// reference them freely. It uses DefaultStartTimeout/DefaultStopTimeout;
// use SetupWithOptions for other bounds.
func Setup(opts ...fx.Option) *Harness {
	return SetupWithOptions(Options{}, opts...)
}

// SetupWithOptions is Setup with explicit Start/Stop timeouts.
func SetupWithOptions(o Options, opts ...fx.Option) *Harness {
	o = o.withDefaults()
	h := &Harness{}
	ginkgo.BeforeSuite(func() { startHarness(h, opts, o.StartTimeout) })
	ginkgo.AfterSuite(func() { stopHarness(h, o.StopTimeout) })
	return h
}

// SetupEach is Setup wired to BeforeEach/AfterEach instead of
// BeforeSuite/AfterSuite: every spec gets its own fresh fx app, started
// and stopped around that one spec.
func SetupEach(opts ...fx.Option) *Harness {
	return SetupEachWithOptions(Options{}, opts...)
}

// SetupEachWithOptions is SetupEach with explicit Start/Stop timeouts.
func SetupEachWithOptions(o Options, opts ...fx.Option) *Harness {
	o = o.withDefaults()
	h := &Harness{}
	ginkgo.BeforeEach(func() { startHarness(h, opts, o.StartTimeout) })
	ginkgo.AfterEach(func() { stopHarness(h, o.StopTimeout) })
	return h
}

func startHarness(h *Harness, opts []fx.Option, timeout time.Duration) {
	h.app = fx.New(opts...)
	if err := StartApp(context.Background(), h.app, timeout); err != nil {
		ginkgo.Fail(err.Error())
	}
}

func stopHarness(h *Harness, timeout time.Duration) {
	if h.app == nil {
		return
	}
	if err := StopApp(context.Background(), h.app, timeout); err != nil {
		ginkgo.Fail(err.Error())
	}
}

// StartApp starts app, bounding the call by timeout via
// context.WithTimeout (HISS-02: explicit context timeout on all I/O).
// fx.App.Start does not itself enforce fx.StartTimeout/fx.StopTimeout —
// those only apply through fx.App.Run — so a direct Start(ctx) call
// blocks until ctx is done or every OnStart hook returns. StartApp is the
// primitive Setup/SetupEach build on; call it directly for custom
// lifecycle wiring, e.g. a hand-rolled BeforeEach.
func StartApp(ctx context.Context, app *fx.App, timeout time.Duration) error {
	startCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		return fmt.Errorf("ginkgofx: start: %w", err)
	}
	return nil
}

// StopApp stops app, bounding the call by timeout via context.WithTimeout
// (HISS-02).
func StopApp(ctx context.Context, app *fx.App, timeout time.Duration) error {
	stopCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		return fmt.Errorf("ginkgofx: stop: %w", err)
	}
	return nil
}
