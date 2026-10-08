// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"golang.org/x/sync/errgroup"
)

// s3AbortTimeout bounds AbortMultipartUpload after a failed or cancelled copy.
const s3AbortTimeout = 30 * time.Second

// Copy implements [Copier] server-side. Sources up to 5 GiB use one
// CopyObject; larger sources use a multipart UploadPartCopy bounded by
// Concurrency. Every request pins the source ETag read first, so a concurrent
// overwrite fails the copy instead of mixing object versions.
func (b *S3Bucket) Copy(ctx context.Context, srcKey, dstKey string) (Object, error) {
	src, err := cleanS3Key(srcKey)
	if err != nil {
		return Object{}, err
	}
	dst, err := cleanS3Key(dstKey)
	if err != nil {
		return Object{}, err
	}
	if src == dst {
		return Object{}, fmt.Errorf("storage/s3: copy %q: %w", src, ErrCopySameKey)
	}
	head, err := b.Stat(ctx, src)
	if err != nil {
		return Object{}, err
	}
	if head.Size > b.transfer.copyThreshold {
		return b.copyMultipart(ctx, src, dst, head)
	}
	return b.copySingle(ctx, src, dst, head)
}

func (b *S3Bucket) copySingle(ctx context.Context, src, dst string, head Object) (Object, error) {
	out, err := b.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            aws.String(b.bucket),
		Key:               aws.String(dst),
		CopySource:        aws.String(b.copySource(src)),
		CopySourceIfMatch: optionalString(head.ETag),
		MetadataDirective: types.MetadataDirectiveCopy,
	})
	if err != nil {
		return Object{}, s3CopyError(src, dst, err)
	}
	obj := copiedObject(dst, head)
	if res := out.CopyObjectResult; res != nil {
		obj.ETag = aws.ToString(res.ETag)
		obj.LastModified = aws.ToTime(res.LastModified)
	}
	return obj, nil
}

func (b *S3Bucket) copyMultipart(ctx context.Context, src, dst string, head Object) (Object, error) {
	in := &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(dst),
		ContentType: optionalString(head.ContentType),
	}
	if len(head.Metadata) > 0 {
		in.Metadata = maps.Clone(head.Metadata)
	}
	created, err := b.client.CreateMultipartUpload(ctx, in)
	if err != nil {
		return Object{}, s3CopyError(src, dst, err)
	}
	uploadID := aws.ToString(created.UploadId)
	parts, err := b.copyParts(ctx, src, dst, uploadID, head)
	if err != nil {
		return Object{}, errors.Join(s3CopyError(src, dst, err), b.abortUpload(ctx, dst, uploadID))
	}
	done, err := b.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(b.bucket),
		Key:             aws.String(dst),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		return Object{}, errors.Join(s3CopyError(src, dst, err), b.abortUpload(ctx, dst, uploadID))
	}
	obj := copiedObject(dst, head)
	obj.ETag = aws.ToString(done.ETag)
	return obj, nil
}

func (b *S3Bucket) copyParts(
	ctx context.Context, src, dst, uploadID string, head Object,
) ([]types.CompletedPart, error) {
	partSize := max(b.transfer.copyPartSize, ceilDiv(head.Size, s3MaxParts))
	if partSize > S3MaxPartSize {
		return nil, fmt.Errorf("storage/s3: %d-byte source exceeds multipart copy limit", head.Size)
	}
	count := ceilDiv(head.Size, partSize)
	parts := make([]types.CompletedPart, count)
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(b.transfer.concurrency)
	for i := range count {
		group.Go(func() error {
			part, err := b.copyPart(groupCtx, src, dst, uploadID, head, partRange{
				number: int32(i + 1), // #nosec G115 -- count <= s3MaxParts (10000) by the partSize floor above.
				first:  i * partSize,
				last:   min((i+1)*partSize, head.Size) - 1,
			})
			parts[i] = part
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("storage/s3: copy parts: %w", err)
	}
	return parts, nil
}

type partRange struct {
	number      int32
	first, last int64
}

func (b *S3Bucket) copyPart(
	ctx context.Context, src, dst, uploadID string, head Object, r partRange,
) (types.CompletedPart, error) {
	out, err := b.client.UploadPartCopy(ctx, &s3.UploadPartCopyInput{
		Bucket:            aws.String(b.bucket),
		Key:               aws.String(dst),
		UploadId:          aws.String(uploadID),
		PartNumber:        aws.Int32(r.number),
		CopySource:        aws.String(b.copySource(src)),
		CopySourceIfMatch: optionalString(head.ETag),
		CopySourceRange:   aws.String(fmt.Sprintf("bytes=%d-%d", r.first, r.last)),
	})
	if err != nil {
		return types.CompletedPart{}, fmt.Errorf("storage/s3: copy part %d: %w", r.number, err)
	}
	part := types.CompletedPart{PartNumber: aws.Int32(r.number)}
	if res := out.CopyPartResult; res != nil {
		part.ETag = res.ETag
	}
	return part, nil
}

// abortUpload outlives a cancelled caller context so no billed parts linger.
func (b *S3Bucket) abortUpload(ctx context.Context, key, uploadID string) error {
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s3AbortTimeout)
	defer cancel()
	_, err := b.client.AbortMultipartUpload(abortCtx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(b.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		return fmt.Errorf("storage/s3: abort multipart upload %q: %w", key, err)
	}
	return nil
}

// copySource URL-encodes each key segment; '+' is escaped too because some
// S3-compatible stores form-decode it to a space.
func (b *S3Bucket) copySource(key string) string {
	segments := strings.Split(key, "/")
	for i, segment := range segments {
		segments[i] = strings.ReplaceAll(url.PathEscape(segment), "+", "%2B")
	}
	return url.PathEscape(b.bucket) + "/" + strings.Join(segments, "/")
}

func copiedObject(dst string, head Object) Object {
	return Object{
		Key:         dst,
		Size:        head.Size,
		ContentType: head.ContentType,
		Metadata:    maps.Clone(head.Metadata),
	}
}

func s3CopyError(src, dst string, err error) error {
	if isNotFound(err) {
		return fmt.Errorf("storage/s3: copy %q: %w", src, ErrNotFound)
	}
	return fmt.Errorf("storage/s3: copy %q to %q: %w", src, dst, err)
}

func optionalString(v string) *string {
	if v == "" {
		return nil
	}
	return aws.String(v)
}

func ceilDiv(n, d int64) int64 {
	return (n + d - 1) / d
}
