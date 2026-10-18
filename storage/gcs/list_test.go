// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcs_test

import (
	"context"
	"testing"
	"time"

	"github.com/golusoris/golusoris/storage/gcs"
	"github.com/golusoris/golusoris/storage/storagetest"
)

// TestBucket_PagedListing seeds MaxListLimit+1 keys under walk/ plus
// walk-other/x, which sorts before them, against fake-gcs-server. StartAfter
// travels as the inclusive StartOffset; the equal key must be skipped.
func TestBucket_PagedListing(t *testing.T) {
	t.Parallel()
	b := startBucket(t, gcs.MinChunkSize)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	storagetest.CheckPagedListing(ctx, t, b)
}
