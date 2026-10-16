// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package pgxpoolstate tells a constructed [pgxpool.Pool] from a zero value
// without dereferencing nil. Recovering a nil-dereference fault instead
// corrupts the heap on Windows hosts with large exception contexts
// (golang/go#81238).
package pgxpoolstate

import (
	"reflect"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Initialized reports whether pool was built by pgxpool.New or NewWithConfig.
// It returns false for nil and for a zero [pgxpool.Pool], and fails closed
// when pgxpool no longer stores its configuration in the inspected field.
func Initialized(pool *pgxpool.Pool) bool {
	if pool == nil {
		return false
	}
	config := reflect.ValueOf(pool).Elem().FieldByName("config")
	if !config.IsValid() || config.Kind() != reflect.Pointer {
		return false
	}
	return !config.IsNil()
}
