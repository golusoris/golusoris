// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"
	"os/exec"
)

// Runner holds a binary path resolved once by its constructor.
type Runner struct{ Bin string }

// Run executes the configured binary. The command name is a bound value, not
// an expression assembled at the call site, and the arguments travel as a
// slice rather than through a shell.
func (r *Runner) Run(ctx context.Context, args ...string) error {
	bin := r.Bin
	if bin == "" {
		bin = "docker"
	}
	return exec.CommandContext(ctx, bin, args...).Run()
}
