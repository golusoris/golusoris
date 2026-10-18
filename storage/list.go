// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"

	corestorage "github.com/golusoris/golusoris/core/storage"
)

// ErrListOrder is returned when a listing yields a key that does not sort
// strictly after the previous key or [ListOptions.StartAfter], for example
// because the backend ignored StartAfter or sorts keys differently.
var ErrListOrder = corestorage.ErrListOrder

// MaxWalkPages bounds the List calls one [Walk] makes.
const MaxWalkPages = corestorage.MaxWalkPages

// NormalizeListLimit maps a zero [ListOptions.Limit] to [DefaultListLimit]
// and rejects limits outside 1..[MaxListLimit].
func NormalizeListLimit(limit int) (int, error) {
	return corestorage.NormalizeListLimit(limit) //nolint:wrapcheck // Preserve shared API error values and messages.
}

// NormalizeListOptions validates opts for a backend's List. Limit resolves
// through [NormalizeListLimit]; Prefix and StartAfter must pass
// [CleanListPrefix]. A StartAfter sorting before every key under Prefix is
// dropped. empty reports a StartAfter sorting after every key under Prefix:
// the page is empty without backend I/O.
func NormalizeListOptions(opts ListOptions) (normalized ListOptions, empty bool, err error) {
	return corestorage.NormalizeListOptions(opts) //nolint:wrapcheck // Preserve shared API error values and messages.
}

// Walk calls fn for every object whose key begins with opts.Prefix and sorts
// after opts.StartAfter, in ascending key order. It lists pages of opts.Limit
// objects through b.List, continuing each page after its last key, and stops
// at the first empty page, at fn's first error, or on ctx cancellation. At most
// [MaxWalkPages] pages are listed. A key that does not sort after its
// predecessor fails the walk with [ErrListOrder] instead of skipping objects.
func Walk(ctx context.Context, b Bucket, opts ListOptions, fn func(Object) error) error {
	return corestorage.Walk(ctx, b, opts, fn) //nolint:wrapcheck // Preserve shared API error values and messages.
}
