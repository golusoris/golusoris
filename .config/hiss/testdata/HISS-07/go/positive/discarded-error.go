// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p discards a call result outright.
package p

import "os"

// Remove throws the result of the call away, so a failure is unobservable.
func Remove(path string) {
	_ = os.Remove(path)
}
