// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// export_test.go bridges this package's internal (package ginkgofx) tests
// and its external (package ginkgofx_test) tests: RunHelperProcess is the
// shared re-exec harness both use to exercise a failure mode that would
// otherwise fail or tear down this whole package's `go test` run if
// triggered inline — a genuine t.Fatal-driven test failure, or Ginkgo's
// os.Exit(1) on an invalid spec tree. It re-invokes this same test binary
// running only one named Test function, with an env var set so that
// function knows to take its "wrong"/"failing" path instead of skipping,
// and hands back that child's combined output for the caller to inspect.
package ginkgofx

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// RunHelperProcess re-execs the current test binary (os.Args[0]), running
// only the test named run, with envVar=1 set in its environment, and
// returns its combined stdout+stderr and any error from waiting on it
// (an *exec.ExitError when the child test failed or the process was
// signaled, as these helper children are expected to). Bounded by an
// explicit context timeout (HISS-02): a hung child must not hang the
// caller's `go test` run.
func RunHelperProcess(t *testing.T, run, envVar string) ([]byte, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// #nosec G204 -- os.Args[0] is this package's own compiled test binary
	// (re-exec'd with a fixed -test.run pattern), not attacker-controlled
	// input.
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+run+"$", "-test.v")
	cmd.Env = append(os.Environ(), envVar+"=1")
	return cmd.CombinedOutput()
}
