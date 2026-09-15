// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p is formatted, fully used and warning-free.
package p

import "fmt"

// Describe formats each value with the verb its type calls for.
func Describe(name string, count int) string {
	return fmt.Sprintf("name=%s count=%d", name, count)
}
