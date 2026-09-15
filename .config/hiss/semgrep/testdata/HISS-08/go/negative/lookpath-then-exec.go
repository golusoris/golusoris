// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"
	"os/exec"
)

// Git resolves a fixed binary name on PATH and then runs it.
func Git(ctx context.Context, args ...string) error {
	bin, err := exec.LookPath("git")
	if err != nil {
		return err
	}
	return exec.CommandContext(ctx, bin, args...).Run()
}
