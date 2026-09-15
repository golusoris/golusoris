// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"
	"os/exec"
)

// RunScript hands a string to a shell, which interprets it as code.
func RunScript(ctx context.Context, script string) error {
	return exec.CommandContext(ctx, "sh", "-c", script).Run()
}

// RunBash does the same through bash.
func RunBash(script string) error {
	return exec.Command("/bin/bash", "-c", script).Run()
}
