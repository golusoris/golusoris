// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"context"
	"fmt"

	"github.com/golusoris/golusoris/storage"
)

func numberedKeys(prefix string, n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("%s%04d", prefix, i)
	}
	return keys
}

func walkKeys(ctx context.Context, b storage.Bucket, opts storage.ListOptions) ([]string, error) {
	var keys []string
	err := storage.Walk(ctx, b, opts, func(obj storage.Object) error {
		keys = append(keys, obj.Key)
		return nil
	})
	return keys, err
}
