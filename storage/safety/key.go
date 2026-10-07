// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package safety

import (
	"fmt"

	"github.com/golusoris/golusoris/storage"
)

// ErrUnsafeKey is returned when an object key fails validation.
var ErrUnsafeKey = storage.ErrUnsafeKey

// CleanKey validates a canonical forward-slash, traversal-free local path.
func CleanKey(key string, maxLen int) (string, error) {
	clean, err := storage.CleanKey(key, maxLen)
	if err != nil {
		return "", fmt.Errorf("storage/safety: clean key: %w", err)
	}
	return clean, nil
}

// MustBeLocal returns nil only when key is an already-local slash path.
func MustBeLocal(key string) error {
	if err := storage.MustBeLocal(key); err != nil {
		return fmt.Errorf("storage/safety: require local key: %w", err)
	}
	return nil
}
