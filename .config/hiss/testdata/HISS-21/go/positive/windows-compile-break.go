// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package p

// BrokenOnWindows is selected only by the Windows leg and fails compilation there.
func BrokenOnWindows() {
	missingWindowsImplementation()
}
