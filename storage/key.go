// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"strings"

	corestorage "github.com/golusoris/golusoris/core/storage"
)

// ErrUnsafeKey is returned when an object key fails validation.
var ErrUnsafeKey = corestorage.ErrUnsafeKey

const (
	// MaxKeyBytes is the backend-neutral object-key byte limit.
	MaxKeyBytes     = corestorage.MaxKeyBytes
	localTempPrefix = ".golusoris-put-"
	localTempSuffix = ".tmp"
)

// CleanKey validates a canonical object key as a local slash path.
func CleanKey(key string, maxLen int) (string, error) {
	return corestorage.CleanKey(key, maxLen) //nolint:wrapcheck // Preserve shared API error values and messages.
}

// MustBeLocal rejects keys that are not already local slash paths.
func MustBeLocal(key string) error {
	return corestorage.MustBeLocal(key) //nolint:wrapcheck // Preserve shared API error values and messages.
}

// CleanListPrefix validates a [ListOptions.Prefix]: empty is valid, and a
// trailing slash survives canonicalisation.
func CleanListPrefix(prefix string) (string, error) {
	return corestorage.CleanListPrefix(prefix) //nolint:wrapcheck // Preserve shared API error values and messages.
}

func isLocalTempName(name string) bool {
	return strings.HasPrefix(name, localTempPrefix) && strings.HasSuffix(name, localTempSuffix)
}
