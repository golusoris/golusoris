// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const s3AmzDateLayout = "20060102T150405Z"

// PresignPut implements [PutPresigner] with a SigV4 query-signed PUT. Content
// type, exact content length, metadata, and checksum (SHA-256, CRC32C, or
// MD5) become signed headers, so S3 rejects an upload that differs from them.
func (b *S3Bucket) PresignPut(
	ctx context.Context, key string, ttl time.Duration, opts PresignPutOptions,
) (PresignedRequest, error) {
	clean, err := cleanS3Key(key)
	if err != nil {
		return PresignedRequest{}, err
	}
	if err = ValidatePresignPut(ttl, opts); err != nil {
		return PresignedRequest{}, fmt.Errorf("storage/s3: presign put %q: %w", clean, err)
	}
	in := &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(clean)}
	if opts.ContentType != "" {
		in.ContentType = aws.String(opts.ContentType)
	}
	if opts.ContentLength > 0 {
		in.ContentLength = aws.Int64(opts.ContentLength)
	}
	if len(opts.Metadata) > 0 {
		in.Metadata = maps.Clone(opts.Metadata)
	}
	applyS3Checksum(in, opts.Checksum)
	req, err := b.presigner.PresignPutObject(ctx, in, func(po *s3.PresignOptions) { po.Expires = ttl })
	if err != nil {
		return PresignedRequest{}, fmt.Errorf("storage/s3: presign put %q: %w", clean, err)
	}
	return s3PresignedRequest(req, ttl)
}

func applyS3Checksum(in *s3.PutObjectInput, sum Checksum) {
	switch sum.Algorithm {
	case ChecksumSHA256:
		in.ChecksumSHA256 = aws.String(sum.Base64())
	case ChecksumCRC32C:
		in.ChecksumCRC32C = aws.String(sum.Base64())
	case ChecksumMD5:
		in.ContentMD5 = aws.String(sum.Base64())
	}
}

// s3PresignedRequest derives Expires from the signed X-Amz-Date, so the
// reported deadline is exactly the one S3 enforces.
func s3PresignedRequest(req *v4PresignedRequest, ttl time.Duration) (PresignedRequest, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return PresignedRequest{}, fmt.Errorf("storage/s3: parse presigned url: %w", err)
	}
	signedAt, err := time.Parse(s3AmzDateLayout, u.Query().Get("X-Amz-Date"))
	if err != nil {
		return PresignedRequest{}, fmt.Errorf("storage/s3: parse presigned X-Amz-Date: %w", err)
	}
	header := make(http.Header, len(req.SignedHeader))
	for name, values := range req.SignedHeader {
		if strings.EqualFold(name, "Host") {
			continue
		}
		for _, value := range values {
			header.Add(name, value)
		}
	}
	return PresignedRequest{Method: req.Method, URL: req.URL, Header: header, Expires: signedAt.Add(ttl)}, nil
}
