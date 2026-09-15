// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p loads executable code chosen at run time.
package p

import "plugin"

// Load maps a shared object into the process and calls a symbol resolved by
// name, so the program's behaviour is not decided by its own source.
func Load(path, symbol string) (any, error) {
	p, err := plugin.Open(path)
	if err != nil {
		return nil, err
	}
	return p.Lookup(symbol)
}
