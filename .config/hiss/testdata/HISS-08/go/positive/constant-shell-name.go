// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "os/exec"

// shell holds the interpreter name in a constant, which semgrep propagates
// into the call below. This fixture pins that reach: a rewrite that matched
// only literal arguments would stop reporting here.
const shell = "/bin/sh"

// RunConstShell runs a script through the constant-named interpreter.
func RunConstShell(script string) error {
	return exec.Command(shell, "-c", script).Run()
}
