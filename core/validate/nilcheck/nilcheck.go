// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package nilcheck detects nil values, including typed-nil values in interfaces.
package nilcheck

import "reflect"

// IsNil reports whether value is nil, including an interface that contains a
// typed-nil channel, function, map, pointer, or slice.
func IsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	kind := reflected.Kind()
	return (kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface ||
		kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice) && reflected.IsNil()
}
