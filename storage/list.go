// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrListOrder is returned when a listing yields a key that does not sort
// strictly after the previous key or [ListOptions.StartAfter], for example
// because the backend ignored StartAfter or sorts keys differently.
var ErrListOrder = errors.New("storage: listing out of ascending key order")

// MaxWalkPages bounds the List calls one [Walk] makes.
const MaxWalkPages = 1 << 20

// NormalizeListLimit maps a zero [ListOptions.Limit] to [DefaultListLimit]
// and rejects limits outside 1..[MaxListLimit].
func NormalizeListLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultListLimit, nil
	}
	if limit < 0 || limit > MaxListLimit {
		return 0, fmt.Errorf("storage: list limit %d outside range 1..%d", limit, MaxListLimit)
	}
	return limit, nil
}

// NormalizeListOptions validates opts for a backend's List. Limit resolves
// through [NormalizeListLimit]; Prefix and StartAfter must pass
// [CleanListPrefix]. A StartAfter sorting before every key under Prefix is
// dropped. empty reports a StartAfter sorting after every key under Prefix:
// the page is empty without backend I/O.
func NormalizeListOptions(opts ListOptions) (normalized ListOptions, empty bool, err error) {
	limit, err := NormalizeListLimit(opts.Limit)
	if err != nil {
		return ListOptions{}, false, err
	}
	prefix, err := CleanListPrefix(opts.Prefix)
	if err != nil {
		return ListOptions{}, false, fmt.Errorf("storage: validate list prefix: %w", err)
	}
	after, err := CleanListPrefix(opts.StartAfter)
	if err != nil {
		return ListOptions{}, false, fmt.Errorf("storage: validate list start-after: %w", err)
	}
	normalized = ListOptions{Prefix: prefix, Limit: limit}
	switch {
	case strings.HasPrefix(after, prefix):
		normalized.StartAfter = after
	case after > prefix:
		empty = true
	}
	return normalized, empty, nil
}

// Walk calls fn for every object whose key begins with opts.Prefix and sorts
// after opts.StartAfter, in ascending key order. It lists pages of opts.Limit
// objects through b.List, continuing each page after its last key, and stops
// at the first empty page, at fn's first error, or on ctx cancellation. At most
// [MaxWalkPages] pages are listed. A key that does not sort after its
// predecessor fails the walk with [ErrListOrder] instead of skipping objects.
func Walk(ctx context.Context, b Bucket, opts ListOptions, fn func(Object) error) error {
	after := opts.StartAfter
	for range MaxWalkPages {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("storage: walk: %w", err)
		}
		opts.StartAfter = after
		page, err := b.List(ctx, opts)
		if err != nil {
			return fmt.Errorf("storage: walk: %w", err)
		}
		if len(page) == 0 {
			return nil
		}
		if after, err = walkPage(page, after, fn); err != nil {
			return err
		}
	}
	return fmt.Errorf("storage: walk: page bound %d reached after key %q", MaxWalkPages, after)
}

func walkPage(page []Object, after string, fn func(Object) error) (string, error) {
	for _, obj := range page {
		if obj.Key <= after {
			return "", fmt.Errorf("storage: walk: key %q after %q: %w", obj.Key, after, ErrListOrder)
		}
		if err := fn(obj); err != nil {
			return "", fmt.Errorf("storage: walk %q: %w", obj.Key, err)
		}
		after = obj.Key
	}
	return after, nil
}
