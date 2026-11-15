// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package scaffold

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// bumpHelperBlockFor allows hosted Windows start-up latency while distinguishing deadline return from helper completion.
const bumpHelperBlockFor = time.Minute

// bumpHelperDeadline leaves the helper time to write its start marker before the deadline fires.
const bumpHelperDeadline = 10 * time.Second

func TestRunBoundedCommandTimesOutFromBackground(t *testing.T) {
	t.Parallel()
	marker, name, args := bumpBlockingHelper(t)
	started := time.Now()

	_, err := runBoundedCommand(context.Background(), bumpHelperDeadline, 128, name, args...)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), bumpHelperBlockFor/2)
	require.FileExists(t, marker, "helper must start before the deadline")
}

func TestRunBoundedCommandPreservesEarlierCallerDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), bumpHelperDeadline)
	defer cancel()
	marker, name, args := bumpBlockingHelper(t)
	started := time.Now()

	_, err := runBoundedCommand(ctx, time.Minute, 128, name, args...)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), bumpHelperBlockFor/2)
	require.FileExists(t, marker, "helper must start before the deadline")
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
				bumpHelperBlockFor,
				outputLimit,
				name,
				args...,
			)

			require.Error(t, err)
			require.NotErrorIs(t, err, context.DeadlineExceeded)
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

func bumpBlockingHelper(t *testing.T) (string, string, []string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "started")
	name, args := bumpHelperCommand("block", 0)
	return marker, name, append(args, marker)
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
		if len(os.Args) > separator+3 {
			// The marker is the start handshake the deadline tests assert on.
			if err := os.WriteFile(os.Args[separator+3], nil, 0o600); err != nil {
				panic(err)
			}
		}
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
