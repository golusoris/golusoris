// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package scaffold

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/core/clikit"
)

const (
	bumpCommandTimeout      = 5 * time.Minute
	bumpCommandWaitDelay    = time.Second
	maxBumpDiagnosticsBytes = 64 << 10
	commandOutputTruncated  = "\n...[output truncated]"
)

type boundedCommandOutput struct {
	mu        sync.Mutex
	bytes     []byte
	limit     int
	truncated bool
}

// BumpCmd returns the `golusoris bump <version>` command.
func BumpCmd() *cobra.Command {
	return clikit.Command(
		"bump", "Bump golusoris to a specific version in the current module",
		clikit.WithRunE(func(cmd *cobra.Command, args []string) error {
			version := "latest"
			if len(args) > 0 {
				version = args[0]
			}
			return bumpGolusoris(cmd, version)
		}),
	)
}

func bumpGolusoris(cmd *cobra.Command, version string) error {
	ctx := cmd.Context()
	pkg := "github.com/golusoris/golusoris"
	if version != "latest" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	target := pkg + "@" + version

	fmt.Printf("Running: go get %s\n", target)
	out, err := runGoCommand(ctx, "get", target)
	if err != nil {
		return commandError("go get "+target, err, out)
	}

	fmt.Printf("Running: go mod tidy\n")
	out, err = runGoCommand(ctx, "mod", "tidy")
	if err != nil {
		return commandError("go mod tidy", err, out)
	}

	fmt.Printf("\nBumped %s to %s.\n", pkg, version)
	fmt.Printf("Check docs/migrations/ in the framework repo for breaking-change notes.\n")
	return nil
}

func runGoCommand(ctx context.Context, args ...string) (string, error) {
	return runBoundedCommand(
		ctx,
		bumpCommandTimeout,
		maxBumpDiagnosticsBytes,
		"go",
		args...,
	)
}

func runBoundedCommand(
	ctx context.Context,
	timeout time.Duration,
	outputLimit int,
	name string,
	args ...string,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output := &boundedCommandOutput{limit: outputLimit}
	command := exec.CommandContext(ctx, name, args...) // #nosec G204 -- production executable is fixed to the Go tool
	command.Stdout = output
	command.Stderr = output
	command.WaitDelay = bumpCommandWaitDelay
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return output.String(), ctxErr
	}
	return output.String(), err
}

func (o *boundedCommandOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	written := len(p)
	remaining := max(o.limit-len(o.bytes), 0)
	if len(p) > remaining {
		o.truncated = true
		p = p[:remaining]
	}
	o.bytes = append(o.bytes, p...)
	return written, nil
}

func (o *boundedCommandOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.truncated || o.limit <= 0 {
		return string(o.bytes)
	}
	markerLength := min(len(commandOutputTruncated), o.limit)
	prefixLength := min(o.limit-markerLength, len(o.bytes))
	return string(o.bytes[:prefixLength]) + commandOutputTruncated[:markerLength]
}

func commandError(operation string, err error, diagnostics string) error {
	if diagnostics == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w\n%s", operation, err, diagnostics)
}
