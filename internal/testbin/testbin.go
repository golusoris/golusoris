// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package testbin builds a main package and runs the binary, so a test drives
// a command through main with the linker flags a release passes.
package testbin

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Timeout bounds one build or one run.
const Timeout = 3 * time.Minute

// Result is the outcome of one run of a built binary.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Build compiles pkg into a temporary directory with the given -ldflags value
// and returns the binary path. It fails the test when the build fails.
func Build(tb testing.TB, pkg, ldflags string) string {
	tb.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		tb.Fatalf("testbin: go toolchain not on PATH: %v", err)
	}
	bin := filepath.Join(tb.TempDir(), filepath.Base(pkg))
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	ctx, cancel := context.WithTimeout(tb.Context(), Timeout)
	defer cancel()
	// Match module build flags to reuse the warm compile cache; -trimpath would change cache keys.
	// #nosec G204 -- the toolchain path comes from LookPath and the arguments from the calling test.
	cmd := exec.CommandContext(ctx, goBin, "build", "-ldflags", ldflags, "-o", bin, pkg)
	if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
		tb.Fatalf("testbin: go build %s: %v\n%s", pkg, buildErr, out)
	}
	return bin
}

// Run executes bin with args and returns its output and exit code. It fails
// the test when the binary cannot start or outlives [Timeout].
func Run(tb testing.TB, bin string, args ...string) Result {
	tb.Helper()
	ctx, cancel := context.WithTimeout(tb.Context(), Timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	// #nosec G204 -- the binary is one Build produced for the calling test.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		tb.Fatalf("testbin: %s outlived %s: %v", bin, Timeout, ctx.Err())
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		tb.Fatalf("testbin: run %s: %v", bin, err)
	}
	return res
}
