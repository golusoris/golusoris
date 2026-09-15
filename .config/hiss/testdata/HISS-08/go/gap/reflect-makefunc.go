// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p synthesises a function body at run time.
package p

import "reflect"

// Make builds a callable whose body does not exist anywhere in the source.
func Make(proto any, body func([]reflect.Value) []reflect.Value) reflect.Value {
	return reflect.MakeFunc(reflect.TypeOf(proto), body)
}
