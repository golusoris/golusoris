// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"
	"fmt"
	"os/exec"
)

// RunConcatenated picks the executable by string concatenation at the call site.
func RunConcatenated(ctx context.Context, dir string) error {
	return exec.CommandContext(ctx, dir+"/helper", "--check").Run()
}

// RunFormatted picks the executable with a format string at the call site.
func RunFormatted(name string) error {
	return exec.Command(fmt.Sprintf("%s-cli", name), "version").Run()
}
