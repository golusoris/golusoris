// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package gcs implements [storage.Bucket], [storage.Copier], and
// [storage.PutPresigner] on Google Cloud Storage via cloud.google.com/go/storage.
//
// Usage:
//
//	fx.New(golusoris.Core, gcs.Module) // provides storage.Bucket
//
// Config keys (koanf prefix "storage.gcs"):
//
//	bucket:       "uploads"
//	endpoint:     ""          # emulator JSON API base, e.g. http://localhost:4443/storage/v1/
//	signer_email: ""          # service account that signs URLs; default detected
//	presign_ttl:  15m
//	chunk_size:   16777216
//
// Authentication uses Application Default Credentials, which covers GKE
// Workload Identity, workload identity federation, and service-account keys.
// Signed URLs use the credential's private key when it has one and the IAM
// Credentials signBlob API otherwise, so keyless workloads can presign.
package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"time"

	"cloud.google.com/go/auth/credentials"
	gstorage "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/storage"
)

const (
	// MinChunkSize is the smallest resumable-upload chunk GCS accepts.
	MinChunkSize = 256 << 10
	// MaxChunkSize bounds the per-Put upload buffer.
	MaxChunkSize = 1 << 30

	defaultChunkSize  = 16 << 20
	defaultPresignTTL = 15 * time.Minute
	cloudPlatform     = "https://www.googleapis.com/auth/cloud-platform"
)

// Options configures a [Bucket].
type Options struct {
	// Bucket is the GCS bucket name. Required.
	Bucket string `koanf:"bucket"`
	// Endpoint targets an emulator's JSON API (e.g.
	// "http://localhost:4443/storage/v1/") without authentication; URLs are
	// then signed with an ephemeral key, which emulators do not verify.
	Endpoint string `koanf:"endpoint"`
	// SignerEmail is the service account that signs URLs. Default: the
	// credential's own account, else the metadata server's default account.
	SignerEmail string `koanf:"signer_email"`
	// PresignTTL is the lifetime of [Bucket.URL] GET URLs (default 15m, at
	// most [storage.MaxPresignTTL]).
	PresignTTL time.Duration `koanf:"presign_ttl"`
	// ChunkSize is the resumable-upload chunk and per-Put buffer size in
	// bytes (default 16 MiB, range [MinChunkSize]..[MaxChunkSize]).
	ChunkSize int `koanf:"chunk_size"`
}

// Bucket implements [storage.Bucket] on one GCS bucket. Safe for concurrent use.
type Bucket struct {
	client     *gstorage.Client
	handle     *gstorage.BucketHandle
	clock      clock.Clock
	signer     *urlSigner
	presignTTL time.Duration
	chunkSize  int
	insecure   bool
}

// New connects to opts.Bucket. Close releases the client.
func New(ctx context.Context, opts Options, clk clock.Clock) (*Bucket, error) {
	if err := validate(opts, clk); err != nil {
		return nil, err
	}
	client, signer, err := connect(ctx, opts)
	if err != nil {
		return nil, err
	}
	return newBucket(client, signer, opts, clk), nil
}

func validate(opts Options, clk clock.Clock) error {
	if opts.Bucket == "" {
		return errors.New("storage/gcs: bucket is required")
	}
	if clk == nil {
		return errors.New("storage/gcs: clock is required")
	}
	if opts.PresignTTL != 0 {
		if err := storage.ValidatePresignTTL(opts.PresignTTL); err != nil {
			return fmt.Errorf("storage/gcs: presign_ttl: %w", err)
		}
	}
	if opts.ChunkSize != 0 && (opts.ChunkSize < MinChunkSize || opts.ChunkSize > MaxChunkSize) {
		return fmt.Errorf("storage/gcs: chunk_size %d outside %d..%d", opts.ChunkSize, MinChunkSize, MaxChunkSize)
	}
	return validateEndpoint(opts.Endpoint)
}

func validateEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	if u, err := url.Parse(endpoint); err != nil || u.Host == "" {
		return fmt.Errorf("storage/gcs: endpoint %q is not an absolute URL", endpoint)
	}
	return nil
}

func connect(ctx context.Context, opts Options) (*gstorage.Client, *urlSigner, error) {
	if opts.Endpoint != "" {
		client, err := gstorage.NewClient(ctx, option.WithEndpoint(opts.Endpoint), option.WithoutAuthentication())
		if err != nil {
			return nil, nil, fmt.Errorf("storage/gcs: new emulator client: %w", err)
		}
		signer, err := newEphemeralSigner()
		if err != nil {
			return nil, nil, errors.Join(err, client.Close())
		}
		return client, signer, nil
	}
	creds, err := credentials.DetectDefault(&credentials.DetectOptions{Scopes: []string{cloudPlatform}})
	if err != nil {
		return nil, nil, fmt.Errorf("storage/gcs: detect credentials: %w", err)
	}
	client, err := gstorage.NewClient(ctx, option.WithAuthCredentials(creds))
	if err != nil {
		return nil, nil, fmt.Errorf("storage/gcs: new client: %w", err)
	}
	signer, err := newCredentialSigner(ctx, creds, opts.SignerEmail)
	if err != nil {
		return nil, nil, errors.Join(err, client.Close())
	}
	return client, signer, nil
}

func newBucket(client *gstorage.Client, signer *urlSigner, opts Options, clk clock.Clock) *Bucket {
	b := &Bucket{
		client:     client,
		handle:     client.Bucket(opts.Bucket),
		clock:      clk,
		signer:     signer,
		presignTTL: opts.PresignTTL,
		chunkSize:  opts.ChunkSize,
	}
	if b.presignTTL == 0 {
		b.presignTTL = defaultPresignTTL
	}
	if b.chunkSize == 0 {
		b.chunkSize = defaultChunkSize
	}
	if u, err := url.Parse(opts.Endpoint); err == nil && u.Scheme == "http" {
		b.insecure = true
	}
	return b
}

// Close releases the underlying client.
func (b *Bucket) Close() error {
	if err := b.client.Close(); err != nil {
		return fmt.Errorf("storage/gcs: close: %w", err)
	}
	return nil
}

// Put implements [storage.Bucket] as a resumable upload in ChunkSize chunks,
// so bodies of unknown length stream with bounded memory. A body read error
// cancels the upload; GCS discards the partial object.
func (b *Bucket) Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.Object, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return storage.Object{}, err
	}
	if r == nil {
		return storage.Object{}, errors.New("storage/gcs: body reader is nil")
	}
	contentType := opts.ContentType
	if contentType == "" {
		contentType = storage.DefaultContentType
	}
	writeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := b.handle.Object(clean).NewWriter(writeCtx)
	w.ContentType = contentType
	w.Metadata = maps.Clone(opts.Metadata)
	w.ChunkSize = b.chunkSize
	if _, err = io.Copy(w, r); err != nil {
		cancel()
		return storage.Object{}, errors.Join(fmt.Errorf("storage/gcs: put %q: %w", clean, err), w.Close())
	}
	if err = w.Close(); err != nil {
		return storage.Object{}, fmt.Errorf("storage/gcs: put %q: %w", clean, err)
	}
	return objectFromAttrs(w.Attrs()), nil
}

// Get implements [storage.Bucket]. It reads the generation Stat observed, so
// the returned attributes always describe the returned body.
func (b *Bucket) Get(ctx context.Context, key string) (io.ReadCloser, storage.Object, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return nil, storage.Object{}, err
	}
	obj := b.handle.Object(clean)
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		return nil, storage.Object{}, mapError("get", clean, err)
	}
	rc, err := obj.Generation(attrs.Generation).NewReader(ctx)
	if err != nil {
		return nil, storage.Object{}, mapError("get", clean, err)
	}
	return rc, objectFromAttrs(attrs), nil
}

// Delete implements [storage.Bucket]; deleting a missing key is not an error.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	clean, err := cleanKey(key)
	if err != nil {
		return err
	}
	if err = b.handle.Object(clean).Delete(ctx); err != nil && !errors.Is(err, gstorage.ErrObjectNotExist) {
		return fmt.Errorf("storage/gcs: delete %q: %w", clean, err)
	}
	return nil
}

// Exists implements [storage.Bucket].
func (b *Bucket) Exists(ctx context.Context, key string) (bool, error) {
	_, err := b.Stat(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Stat implements [storage.Bucket].
func (b *Bucket) Stat(ctx context.Context, key string) (storage.Object, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return storage.Object{}, err
	}
	attrs, err := b.handle.Object(clean).Attrs(ctx)
	if err != nil {
		return storage.Object{}, mapError("stat", clean, err)
	}
	return objectFromAttrs(attrs), nil
}

// List implements [storage.Bucket] as one bounded page of at most Limit objects.
func (b *Bucket) List(ctx context.Context, opts storage.ListOptions) ([]storage.Object, error) {
	limit, err := storage.NormalizeListLimit(opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("storage/gcs: list: %w", err)
	}
	prefix, err := storage.CleanListPrefix(opts.Prefix)
	if err != nil {
		return nil, fmt.Errorf("storage/gcs: validate list prefix: %w", err)
	}
	query := &gstorage.Query{Prefix: prefix}
	if err = query.SetAttrSelection([]string{"Name", "Size", "Etag", "Updated"}); err != nil {
		return nil, fmt.Errorf("storage/gcs: list %q: %w", prefix, err)
	}
	it := b.handle.Objects(ctx, query)
	it.PageInfo().MaxSize = limit
	out := make([]storage.Object, 0, limit)
	for range limit {
		attrs, nextErr := it.Next()
		if errors.Is(nextErr, iterator.Done) {
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("storage/gcs: list %q: %w", prefix, nextErr)
		}
		if _, keyErr := cleanKey(attrs.Name); keyErr != nil {
			return nil, fmt.Errorf("storage/gcs: unsafe listed key: %w", keyErr)
		}
		out = append(out, storage.Object{
			Key: attrs.Name, Size: attrs.Size, ETag: attrs.Etag, LastModified: attrs.Updated,
		})
	}
	return out, nil
}

// Copy implements [storage.Copier] with a server-side rewrite pinned to the
// source generation, so a concurrent overwrite fails the copy instead of
// mixing versions. Content type and metadata are kept.
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
		return storage.Object{}, fmt.Errorf("storage/gcs: copy %q: %w", src, storage.ErrCopySameKey)
	}
	source := b.handle.Object(src)
	attrs, err := source.Attrs(ctx)
	if err != nil {
		return storage.Object{}, mapError("copy", src, err)
	}
	pinned := source.If(gstorage.Conditions{GenerationMatch: attrs.Generation})
	out, err := b.handle.Object(dst).CopierFrom(pinned).Run(ctx)
	if err != nil {
		return storage.Object{}, mapError("copy", src, err)
	}
	return objectFromAttrs(out), nil
}

func cleanKey(key string) (string, error) {
	clean, err := storage.CleanKey(key, storage.MaxKeyBytes)
	if err != nil {
		return "", fmt.Errorf("storage/gcs: validate key: %w", err)
	}
	return clean, nil
}

func mapError(op, key string, err error) error {
	if errors.Is(err, gstorage.ErrObjectNotExist) {
		return fmt.Errorf("storage/gcs: %s %q: %w", op, key, storage.ErrNotFound)
	}
	return fmt.Errorf("storage/gcs: %s %q: %w", op, key, err)
}

func objectFromAttrs(attrs *gstorage.ObjectAttrs) storage.Object {
	if attrs == nil {
		return storage.Object{}
	}
	return storage.Object{
		Key:          attrs.Name,
		Size:         attrs.Size,
		ContentType:  attrs.ContentType,
		Metadata:     maps.Clone(attrs.Metadata),
		ETag:         attrs.Etag,
		LastModified: attrs.Updated,
	}
}

var (
	_ storage.Bucket       = (*Bucket)(nil)
	_ storage.Copier       = (*Bucket)(nil)
	_ storage.PutPresigner = (*Bucket)(nil)
)
