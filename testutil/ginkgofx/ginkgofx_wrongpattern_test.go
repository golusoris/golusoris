// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ginkgofx_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2"

	"github.com/golusoris/golusoris/testutil/ginkgofx"
)

// wrongPatternEnv gates TestWrongSetupPattern_child: set only in the
// subprocess TestSetup_negative_nestedInsideDescribeFailsClearly re-execs
// via ginkgofx.RunHelperProcess.
const wrongPatternEnv = "GINKGOFX_WRONG_PATTERN_CHILD"

// wantWrongPatternSubstr is the text Ginkgo v2 prints
// (types.GinkgoErrors.SuiteNodeInNestedContext) when a suite-level node
// (BeforeSuite/AfterSuite) is registered from inside a container instead
// of at the top level — exactly what ginkgofx.Setup does internally.
const wantWrongPatternSubstr = "can only be called at the top level"

// TestSetup_negative_nestedInsideDescribeFailsClearly proves the call
// pattern documented on Setup is load-bearing, not just a style
// preference: registering Setup's BeforeSuite/AfterSuite hooks from inside
// a Describe, instead of at the package's true top level, is rejected by
// Ginkgo v2 itself. BeforeSuite/AfterSuite are suite-level nodes that may
// only be added while Ginkgo is still in its top-level tree-construction
// phase; a container's closure body isn't run until later, once RunSpecs
// starts entering the tree (Ginkgo v2 internal/suite.go: PushNode defers
// entering a container's closure to PhaseBuildTree, and pushSuiteNode
// rejects any suite node registered once phase is PhaseBuildTree). Ginkgo
// reports the violation by printing an error and calling os.Exit(1) —
// which would tear down this whole package's `go test` run if triggered
// inline — so the wrong pattern is exercised in a re-exec'd subprocess via
// ginkgofx.RunHelperProcess instead.
func TestSetup_negative_nestedInsideDescribeFailsClearly(t *testing.T) {
	t.Parallel()

	out, err := ginkgofx.RunHelperProcess(t, "TestWrongSetupPattern_child", wrongPatternEnv)
	if err == nil {
		t.Fatalf("helper process with Setup nested inside Describe unexpectedly succeeded; output:\n%s", out)
	}
	if !strings.Contains(string(out), wantWrongPatternSubstr) {
		t.Fatalf("helper output missing %q; got:\n%s", wantWrongPatternSubstr, out)
	}
}

// TestWrongSetupPattern_child is not a real test: it only runs when
// re-exec'd by TestSetup_negative_nestedInsideDescribeFailsClearly (via
// wrongPatternEnv). It demonstrates the call pattern Setup's doc comment
// and this package's AGENTS.md warn against: registering Setup from inside
// a Describe closure instead of at the suite's true top level.
func TestWrongSetupPattern_child(t *testing.T) {
	t.Parallel()
	if os.Getenv(wrongPatternEnv) == "" {
		t.Skip("only runs as a re-exec'd child of TestSetup_negative_nestedInsideDescribeFailsClearly")
	}

	_ = ginkgo.Describe("wrong ginkgofx.Setup call pattern", func() {
		// WRONG: Setup registers ginkgo.BeforeSuite/AfterSuite, which must
		// be called at the top level — never from inside a Describe
		// closure like this one. This line is what makes Ginkgo reject the
		// tree once RunSpecs below starts entering containers.
		ginkgofx.Setup()
		ginkgo.It("never runs", func() {})
	})

	ginkgo.RunSpecs(t, "wrong ginkgofx.Setup pattern suite")
}
