// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p is the after-state of a removal.
package p

// Keep survives. The package previously also exported Drop(string) error;
// deleting it breaks every importer. The CI job "API compatibility (apidiff)"
// prints the difference and downgrades every failure to a warning, so nothing
// fails, and no gate requires a Migration: footer for the change.
func Keep(s string) string {
	return s
}
