// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "os/exec"

// RunPwsh hands a string to PowerShell Core, whose binary is named pwsh —
// not powershell, which is the Windows-only 5.x name.
func RunPwsh(script string) error {
	return exec.Command("pwsh", "-Command", script).Run()
}

// RunPwshWindows does the same through the Windows executable name.
func RunPwshWindows(script string) error {
	return exec.Command("pwsh.exe", "-Command", script).Run()
}
