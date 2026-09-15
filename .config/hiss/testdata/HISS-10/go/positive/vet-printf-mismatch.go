// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p formats a value with the wrong verb.
package p

import "fmt"

// Describe passes a string to a %d verb.
func Describe(name string) string {
	return fmt.Sprintf("name=%d", name)
}
