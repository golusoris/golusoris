// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "reflect"

// Synthesize builds a callee at run time, so the call graph is not decidable
// from the source.
func Synthesize(typ reflect.Type) reflect.Value {
	return reflect.MakeFunc(typ, func(args []reflect.Value) []reflect.Value {
		return args
	})
}
