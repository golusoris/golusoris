// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p drops an error by never binding it.
package p

import "os"

// Remove calls a function that returns an error and binds nothing at all.
// Deciding that the dropped result IS an error needs type information the
// syntax scanner does not build; errcheck decides it with types.
func Remove(path string) {
	os.Remove(path)
}
