// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"
	"os/exec"
)

// Tidy runs a fixed binary with a fixed argument vector.
func Tidy(ctx context.Context) error {
	return exec.CommandContext(ctx, "go", "mod", "tidy").Run()
}
