// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p tests an error and then does nothing with it.
package p

import "os"

// Remove enters the failure branch and leaves it empty, which is an explicit
// decision to swallow the error.
func Remove(path string) {
	err := os.Remove(path)
	if err != nil {
	}
}
