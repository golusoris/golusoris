// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/golusoris/golusoris/storage"
	"github.com/golusoris/golusoris/storage/storagetest"
)

func TestS3Bucket_PagedListing(t *testing.T) {
	t.Parallel()
	b := startS3Bucket(t, storage.S3Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	storagetest.CheckPagedListing(ctx, t, b)
}

func TestLocalBucket_PagedListing(t *testing.T) {
	t.Parallel()
	b, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storagetest.CheckPagedListing(t.Context(), t, b)
}
