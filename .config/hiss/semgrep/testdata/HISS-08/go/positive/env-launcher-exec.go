// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "os/exec"

// RunViaEnv names env as the executable, which then execs whatever its first
// argument says: the binary that actually runs is chosen at run time.
func RunViaEnv(bin string, args ...string) error {
	return exec.Command("env", append([]string{bin}, args...)...).Run()
}
