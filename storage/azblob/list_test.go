// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azblob_test

import (
	"context"
	"testing"
	"time"

	"github.com/golusoris/golusoris/storage/azblob"
	"github.com/golusoris/golusoris/storage/storagetest"
)

// TestBucket_PagedListing seeds MaxListLimit+1 blobs under walk/ plus
// walk-other/x, which sorts before them, against Azurite. StartAfter travels
// as the inclusive startFrom parameter; the equal blob must be skipped.
func TestBucket_PagedListing(t *testing.T) {
	t.Parallel()
	b := startBucket(t, azblob.Options{BlockSize: azblob.MinBlockSize, Concurrency: 1})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	storagetest.CheckPagedListing(ctx, t, b)
}
