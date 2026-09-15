// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p shadows an outer binding inside a narrower block.
package p

import "errors"

// Load rebinds err inside the if-block, so the outer err keeps its old value
// and the inner failure is lost. govet's shadow analyzer reports the inner
// declaration.
func Load(ok bool) error {
	err := errors.New("outer")
	if ok {
		err := errors.New("inner")
		_ = err
	}
	return err
}
