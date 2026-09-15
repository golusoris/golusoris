// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ginkgofx_test

import (
	"context"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/testutil/ginkgofx"
)

// greeter is the trivial fx-provided value the suite resolves inside its
// specs, standing in for a real service.
type greeter struct {
	Message string
	started bool
}

func newGreeter(lc fx.Lifecycle) *greeter {
	g := &greeter{Message: "hello from fx"}
	lc.Append(fx.Hook{OnStart: func(context.Context) error {
		g.started = true
		return nil
	}})
	return g
}

var g *greeter

// h is declared as a package-level var — the suite's true top level — not
// inside any Describe/Context closure. That placement is load-bearing, not
// stylistic: Setup registers ginkgo.BeforeSuite/AfterSuite, which Ginkgo
// only accepts during its top-level tree-construction phase; see
// ginkgofx_wrongpattern_test.go for what happens when Setup is called from
// inside a container instead.
var h = ginkgofx.Setup(
	fx.Provide(newGreeter),
	ginkgofx.Populate(&g),
)

var _ = ginkgo.Describe("ginkgofx.Setup", func() {
	ginkgo.It("boots a trivial fx app once for the suite and resolves it via Populate", func() {
		gomega.Expect(g).NotTo(gomega.BeNil())
		gomega.Expect(g.Message).To(gomega.Equal("hello from fx"))
		gomega.Expect(g.started).To(gomega.BeTrue(), "OnStart hook should have run before any It")
		gomega.Expect(h.App()).NotTo(gomega.BeNil())
	})
})

var eachCount int

var hEach *ginkgofx.Harness

// SetupEach's BeforeEach/AfterEach must be registered inside this
// Describe's closure, not at package level: Ginkgo scopes a Setup*
// node to the container it is called from, so calling it at the top
// level would run it before every spec in the whole suite, including
// the unrelated "ginkgofx.Setup" specs above.
var _ = ginkgo.Describe("ginkgofx.SetupEach", func() {
	hEach = ginkgofx.SetupEach(
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{OnStart: func(context.Context) error {
				eachCount++
				return nil
			}})
		}),
	)

	ginkgo.It("starts a fresh app for the first spec", func() {
		gomega.Expect(hEach.App()).NotTo(gomega.BeNil())
		gomega.Expect(eachCount).To(gomega.Equal(1))
	})

	ginkgo.It("starts a fresh app for the second spec too", func() {
		gomega.Expect(eachCount).To(gomega.Equal(2))
	})
})
