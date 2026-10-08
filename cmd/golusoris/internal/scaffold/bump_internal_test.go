// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package scaffold

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// bumpHelperBlockFor outlives every caller deadline in these tests.
const bumpHelperBlockFor = time.Minute

func TestRunBoundedCommandTimesOutFromBackground(t *testing.T) {
	t.Parallel()
	name, args := bumpHelperCommand("block", 0)
	started := time.Now()

	_, err := runBoundedCommand(context.Background(), 25*time.Millisecond, 128, name, args...)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), time.Second)
}

func TestRunBoundedCommandPreservesEarlierCallerDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	name, args := bumpHelperCommand("block", 0)
	started := time.Now()

	_, err := runBoundedCommand(ctx, time.Minute, 128, name, args...)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), time.Second)
}

func TestRunBoundedCommandPreservesCallerCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	name, args := bumpHelperCommand("block", 0)

	_, err := runBoundedCommand(ctx, time.Minute, 128, name, args...)

	require.ErrorIs(t, err, context.Canceled)
}

func TestRunBoundedCommandCapsDiagnosticsAtBoundary(t *testing.T) {
	t.Parallel()
	const outputLimit = 128
	for _, tt := range []struct {
		name          string
		outputBytes   int
		wantTruncated bool
	}{
		{name: "exact limit", outputBytes: outputLimit},
		{name: "one byte over", outputBytes: outputLimit + 1, wantTruncated: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			name, args := bumpHelperCommand("output", tt.outputBytes)

			diagnostics, err := runBoundedCommand(
				context.Background(),
				time.Second,
				outputLimit,
				name,
				args...,
			)

			require.Error(t, err)
			require.Len(t, diagnostics, outputLimit)
			require.Equal(t, tt.wantTruncated, strings.HasSuffix(diagnostics, commandOutputTruncated))
		})
	}
}

func bumpHelperCommand(mode string, outputBytes int) (string, []string) {
	return os.Args[0], []string{
		"-test.run=TestBumpCommandHelperProcess",
		"--",
		mode,
		strconv.Itoa(outputBytes),
	}
}

func TestBumpCommandHelperProcess(t *testing.T) {
	t.Parallel()
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		return
	}
	mode := os.Args[separator+1]
	switch mode {
	case "block":
		// A pending timer keeps the runtime deadlock detector quiet; select {} exits 2 in cgo-free binaries.
		time.Sleep(bumpHelperBlockFor)
	case "output":
		outputBytes, err := strconv.Atoi(os.Args[separator+2])
		if err != nil {
			panic(err)
		}
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("x", outputBytes))
		os.Exit(7)
	default:
		panic("unknown helper mode")
	}
}
