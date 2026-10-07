// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	gstorage "cloud.google.com/go/storage"

	"github.com/golusoris/golusoris/storage"
)

const (
	// signTimeout bounds metadata lookup plus remote signBlob per URL.
	signTimeout     = 30 * time.Second
	goog4DateLayout = "20060102T150405Z"
	// expiryPad offsets the signer truncating X-Goog-Expires to whole seconds.
	expiryPad = 500 * time.Millisecond
)

// URL implements [storage.Bucket] with a V4 signed GET valid for PresignTTL.
func (b *Bucket) URL(ctx context.Context, key string) (string, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return "", err
	}
	return b.signedURL(ctx, clean, b.presignTTL, &gstorage.SignedURLOptions{Method: http.MethodGet})
}

// PresignPut implements [storage.PutPresigner] with a V4 signed PUT. Content
// type, metadata, exact length (x-goog-content-length-range), and an MD5 or
// CRC32C checksum (x-goog-hash) become signed headers that GCS enforces.
// SHA-256 returns [storage.ErrUnsupportedChecksum].
func (b *Bucket) PresignPut(
	ctx context.Context, key string, ttl time.Duration, opts storage.PresignPutOptions,
) (storage.PresignedRequest, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return storage.PresignedRequest{}, err
	}
	if err = storage.ValidatePresignPut(ttl, opts); err != nil {
		return storage.PresignedRequest{}, fmt.Errorf("storage/gcs: presign put %q: %w", clean, err)
	}
	signOpts, header, err := putConstraints(opts)
	if err != nil {
		return storage.PresignedRequest{}, fmt.Errorf("storage/gcs: presign put %q: %w", clean, err)
	}
	signed, err := b.signedURL(ctx, clean, ttl, signOpts)
	if err != nil {
		return storage.PresignedRequest{}, err
	}
	expires, err := signedExpiry(signed)
	if err != nil {
		return storage.PresignedRequest{}, err
	}
	return storage.PresignedRequest{Method: http.MethodPut, URL: signed, Header: header, Expires: expires}, nil
}

func putConstraints(opts storage.PresignPutOptions) (*gstorage.SignedURLOptions, http.Header, error) {
	signOpts := &gstorage.SignedURLOptions{Method: http.MethodPut, ContentType: opts.ContentType}
	header := make(http.Header, len(opts.Metadata)+3)
	add := func(name, value string) {
		signOpts.Headers = append(signOpts.Headers, name+":"+value)
		header.Set(name, value)
	}
	if opts.ContentType != "" {
		header.Set("Content-Type", opts.ContentType)
	}
	if opts.ContentLength > 0 {
		add("x-goog-content-length-range", fmt.Sprintf("%d,%d", opts.ContentLength, opts.ContentLength))
	}
	for name, value := range opts.Metadata {
		add("x-goog-meta-"+strings.ToLower(name), value)
	}
	switch opts.Checksum.Algorithm {
	case "":
	case storage.ChecksumMD5:
		signOpts.MD5 = opts.Checksum.Base64()
		header.Set("Content-MD5", signOpts.MD5)
	case storage.ChecksumCRC32C:
		add("x-goog-hash", "crc32c="+opts.Checksum.Base64())
	case storage.ChecksumSHA256:
		return nil, nil, fmt.Errorf("%w: GCS hashes uploads with MD5 or CRC32C only", storage.ErrUnsupportedChecksum)
	default:
		return nil, nil, fmt.Errorf("%w: GCS cannot bind %q", storage.ErrUnsupportedChecksum, opts.Checksum.Algorithm)
	}
	return signOpts, header, nil
}

func (b *Bucket) signedURL(ctx context.Context, key string, ttl time.Duration, opts *gstorage.SignedURLOptions) (string, error) {
	signCtx, cancel := context.WithTimeout(ctx, signTimeout)
	defer cancel()
	if err := b.signer.apply(signCtx, opts); err != nil {
		return "", err
	}
	opts.Scheme = gstorage.SigningSchemeV4
	opts.Insecure = b.insecure
	opts.Expires = b.clock.Now().Add(ttl + expiryPad)
	signed, err := b.handle.SignedURL(key, opts)
	if err != nil {
		return "", fmt.Errorf("storage/gcs: sign %s %q: %w", opts.Method, key, err)
	}
	return signed, nil
}

// signedExpiry reports the deadline GCS enforces: X-Goog-Date + X-Goog-Expires.
func signedExpiry(signed string) (time.Time, error) {
	u, err := url.Parse(signed)
	if err != nil {
		return time.Time{}, fmt.Errorf("storage/gcs: parse signed url: %w", err)
	}
	q := u.Query()
	signedAt, err := time.Parse(goog4DateLayout, q.Get("X-Goog-Date"))
	if err != nil {
		return time.Time{}, fmt.Errorf("storage/gcs: parse X-Goog-Date: %w", err)
	}
	seconds, err := strconv.Atoi(q.Get("X-Goog-Expires"))
	if err != nil {
		return time.Time{}, fmt.Errorf("storage/gcs: parse X-Goog-Expires: %w", err)
	}
	return signedAt.Add(time.Duration(seconds) * time.Second), nil
}
