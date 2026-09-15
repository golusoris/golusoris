// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"os"
	"os/exec"
)

// RunUserShell is a violation the rule does not report: the executable is
// whatever $SHELL says at run time, but the name reaches exec.Command through
// a plain variable, and the rule deliberately does not decide variables —
// every legitimate exec site in this tree uses that same shape.
func RunUserShell(script string) error {
	bin := os.Getenv("SHELL")
	if bin == "" {
		return os.ErrNotExist
	}
	return exec.Command(bin, "-c", script).Run()
}
