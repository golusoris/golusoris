// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azblob

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"

	"github.com/golusoris/golusoris/storage"
)

const (
	copyPollInterval = 500 * time.Millisecond
	// maxCopyPolls bounds waiting for a pending server-side copy (1 hour).
	maxCopyPolls = 7200
)

// Copy implements [storage.Copier] with a same-account Copy Blob pinned to
// the source ETag, so a concurrent overwrite fails the copy instead of mixing
// versions. Content type and metadata are kept. A pending copy is polled
// until it settles, the context ends, or one hour passes.
func (b *Bucket) Copy(ctx context.Context, srcKey, dstKey string) (storage.Object, error) {
	src, err := cleanKey(srcKey)
	if err != nil {
		return storage.Object{}, err
	}
	dst, err := cleanKey(dstKey)
	if err != nil {
		return storage.Object{}, err
	}
	if src == dst {
		return storage.Object{}, fmt.Errorf("storage/azblob: copy %q: %w", src, storage.ErrCopySameKey)
	}
	source := b.container.NewBlobClient(src)
	props, err := source.GetProperties(ctx, nil)
	if err != nil {
		return storage.Object{}, mapError("copy", src, err)
	}
	target := b.container.NewBlobClient(dst)
	started, err := target.StartCopyFromURL(ctx, source.URL(), &blob.StartCopyFromURLOptions{
		SourceModifiedAccessConditions: &blob.SourceModifiedAccessConditions{SourceIfMatch: props.ETag},
	})
	if err != nil {
		return storage.Object{}, mapError("copy", src, err)
	}
	if err = b.awaitCopy(ctx, target, deref(started.CopyStatus)); err != nil {
		return storage.Object{}, fmt.Errorf("storage/azblob: copy %q to %q: %w", src, dst, err)
	}
	return b.Stat(ctx, dst)
}

func (b *Bucket) awaitCopy(ctx context.Context, target *blob.Client, status blob.CopyStatusType) error {
	for range maxCopyPolls {
		switch status {
		case blob.CopyStatusTypeSuccess:
			return nil
		case blob.CopyStatusTypePending:
		case blob.CopyStatusTypeAborted, blob.CopyStatusTypeFailed:
			return fmt.Errorf("copy status %q", status)
		default:
			return fmt.Errorf("unknown copy status %q", status)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("await copy: %w", ctx.Err())
		case <-b.clock.After(copyPollInterval):
		}
		props, err := target.GetProperties(ctx, nil)
		if err != nil {
			return fmt.Errorf("poll copy: %w", err)
		}
		status = deref(props.CopyStatus)
	}
	return fmt.Errorf("copy still pending after %d polls", maxCopyPolls)
}
