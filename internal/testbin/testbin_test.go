// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testbin_test

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/internal/testbin"
)

const fixture = "github.com/golusoris/golusoris/internal/testbin/testdata/exit"

// fatalRecorder captures the first Fatalf and stops the calling goroutine.
type fatalRecorder struct {
	testing.TB

	msg string
}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func fatalOf(t *testing.T, fn func(testing.TB)) string {
	t.Helper()
	rec := &fatalRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(rec)
	}()
	<-done
	return rec.msg
}

func TestBuildAppliesLinkerFlags(t *testing.T) {
	t.Parallel()
	bin := testbin.Build(t, fixture, "-X main.version=v9.9.9")

	res := testbin.Run(t, bin)

	require.Equal(t, testbin.Result{Stdout: "v9.9.9\n", Stderr: "stderr line\n"}, res)
}

func TestBuildWithoutLinkerFlagsKeepsDefault(t *testing.T) {
	t.Parallel()
	bin := testbin.Build(t, fixture, "")

	require.Equal(t, "unset\n", testbin.Run(t, bin).Stdout)
}

func TestRunReportsExitCode(t *testing.T) {
	t.Parallel()
	bin := testbin.Build(t, fixture, "")

	require.Equal(t, 3, testbin.Run(t, bin, "fail").ExitCode)
}

func TestBuildFailsOnMissingPackage(t *testing.T) {
	t.Parallel()
	msg := fatalOf(t, func(tb testing.TB) {
		tb.Helper()
		testbin.Build(tb, "github.com/golusoris/golusoris/internal/testbin/testdata/absent", "")
	})

	require.Contains(t, msg, "testbin: go build")
}

func TestRunFailsOnMissingBinary(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent")
	msg := fatalOf(t, func(tb testing.TB) {
		tb.Helper()
		testbin.Run(tb, missing)
	})

	require.Contains(t, msg, "testbin: run")
}
