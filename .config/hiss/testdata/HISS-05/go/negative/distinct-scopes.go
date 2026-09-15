// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p keeps every binding in its narrowest distinct scope.
package p

import "errors"

// Load names the inner failure distinctly, so nothing is shadowed and nothing
// is rebound.
func Load(ok bool) error {
	err := errors.New("outer")
	if ok {
		inner := errors.New("inner")
		return inner
	}
	return err
}
