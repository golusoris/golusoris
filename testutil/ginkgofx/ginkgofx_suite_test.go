// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ginkgofx_test

import (
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// TestGinkgofx is the Ginkgo bootstrap: it runs every Describe/It in this
// package as one spec suite, exercising ginkgofx.Setup end to end against
// real Ginkgo BeforeSuite/AfterSuite hooks.
//
// Qualified ginkgo./gomega. calls throughout this package, not the usual
// dot-imported DSL: the repo's golangci-lint config enables revive's
// dot-imports rule with no test-file exclusion (.golangci.yml), so the
// idiomatic `. "github.com/onsi/ginkgo/v2"` form would fail the lint gate.
func TestGinkgofx(t *testing.T) {
	t.Parallel()
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "ginkgofx Suite")
}
