// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"
	"os/exec"
)

// RunUsrBinBash reaches the interpreter by its real path on a distribution
// where /bin is a symlink to /usr/bin (Arch, Fedora), which must be recognised
// exactly like the /bin spelling.
func RunUsrBinBash(script string) error {
	return exec.Command("/usr/bin/bash", "-c", script).Run()
}

// RunUsrBinSh is the same shape through the POSIX shell.
func RunUsrBinSh(ctx context.Context, script string) error {
	return exec.CommandContext(ctx, "/usr/bin/sh", "-c", script).Run()
}
