// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package azblob implements [storage.Bucket], [storage.Copier], and
// [storage.PutPresigner] on Azure Blob Storage block blobs via the Azure SDK
// for Go.
//
// Usage:
//
//	fx.New(golusoris.Core, azblob.Module) // provides storage.Bucket
//
// Config keys (koanf prefix "storage.azblob"):
//
//	service_url:  "https://account.blob.core.windows.net/"
//	container:    "uploads"
//	account_name: ""   # with account_key: shared-key auth (Azurite, legacy)
//	account_key:  ""
//	client_id:    ""   # workload / user-assigned managed identity
//	tenant_id:    ""
//	federated_token_file: ""
//	presign_ttl:  15m
//	block_size:   8388608
//	concurrency:  5
//
// Without an account key the bucket authenticates with Microsoft Entra ID:
// Kubernetes workload identity when a federated token file is configured or
// injected by the AKS webhook, otherwise managed identity. Presigned URLs are
// then user delegation SAS tokens, so no account key is ever needed.
package azblob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/storage"
)

const (
	// MinBlockSize is the smallest staged block the SDK uploads.
	MinBlockSize = 1 << 20
	// MaxBlockSize is the largest block Azure accepts (4000 MiB).
	MaxBlockSize = 4000 << 20
	// MaxConcurrency bounds parallel block uploads per Put.
	MaxConcurrency = 32

	defaultBlockSize   = 8 << 20
	defaultConcurrency = 5
	defaultPresignTTL  = 15 * time.Minute
)

// Options configures a [Bucket].
type Options struct {
	// ServiceURL is the blob endpoint, e.g. "https://account.blob.core.windows.net/"
	// or "http://127.0.0.1:10000/devstoreaccount1/" for Azurite. Required.
	ServiceURL string `koanf:"service_url"`
	// Container is the blob container name. Required.
	Container string `koanf:"container"`
	// AccountName and AccountKey select shared-key auth; both or neither.
	AccountName string `koanf:"account_name"`
	AccountKey  string `koanf:"account_key"`
	// ClientID selects the workload identity app or user-assigned managed
	// identity (default: AZURE_CLIENT_ID / system-assigned identity).
	ClientID string `koanf:"client_id"`
	// TenantID overrides AZURE_TENANT_ID for workload identity.
	TenantID string `koanf:"tenant_id"`
	// FederatedTokenFile overrides AZURE_FEDERATED_TOKEN_FILE for workload identity.
	FederatedTokenFile string `koanf:"federated_token_file"`
	// PresignTTL is the lifetime of [Bucket.URL] SAS URLs (default 15m, at
	// most [storage.MaxPresignTTL]).
	PresignTTL time.Duration `koanf:"presign_ttl"`
	// BlockSize is the staged block size in bytes (default 8 MiB, range
	// [MinBlockSize]..[MaxBlockSize]). Azure caps a blob at 50000 blocks.
	BlockSize int64 `koanf:"block_size"`
	// Concurrency bounds parallel block uploads per Put (default 5, at most
	// [MaxConcurrency]). Peak Put buffer memory is Concurrency*BlockSize.
	Concurrency int `koanf:"concurrency"`
}

// Bucket implements [storage.Bucket] on one container. Safe for concurrent use.
type Bucket struct {
	service     *service.Client
	container   *container.Client
	name        string
	sharedKey   *service.SharedKeyCredential
	clock       clock.Clock
	protocol    sas.Protocol
	presignTTL  time.Duration
	blockSize   int64
	concurrency int
}

// New builds a [Bucket]. It performs no I/O; credentials resolve on first use.
func New(opts Options, clk clock.Clock) (*Bucket, error) {
	if err := validate(opts, clk); err != nil {
		return nil, err
	}
	svc, sharedKey, err := newServiceClient(opts)
	if err != nil {
		return nil, err
	}
	b := &Bucket{
		service:     svc,
		container:   svc.NewContainerClient(opts.Container),
		name:        opts.Container,
		sharedKey:   sharedKey,
		clock:       clk,
		protocol:    sas.ProtocolHTTPS,
		presignTTL:  defaultPresignTTL,
		blockSize:   defaultBlockSize,
		concurrency: defaultConcurrency,
	}
	if strings.HasPrefix(opts.ServiceURL, "http://") {
		b.protocol = sas.ProtocolHTTPSandHTTP
	}
	if opts.PresignTTL != 0 {
		b.presignTTL = opts.PresignTTL
	}
	if opts.BlockSize != 0 {
		b.blockSize = opts.BlockSize
	}
	if opts.Concurrency != 0 {
		b.concurrency = opts.Concurrency
	}
	return b, nil
}

func validate(opts Options, clk clock.Clock) error {
	if clk == nil {
		return errors.New("storage/azblob: clock is required")
	}
	if opts.Container == "" {
		return errors.New("storage/azblob: container is required")
	}
	if u, err := url.Parse(opts.ServiceURL); err != nil || u.Host == "" {
		return fmt.Errorf("storage/azblob: service_url %q is not an absolute URL", opts.ServiceURL)
	}
	if (opts.AccountName == "") != (opts.AccountKey == "") {
		return errors.New("storage/azblob: account_name and account_key must be set together")
	}
	return validateTuning(opts)
}

func validateTuning(opts Options) error {
	if opts.PresignTTL != 0 {
		if err := storage.ValidatePresignTTL(opts.PresignTTL); err != nil {
			return fmt.Errorf("storage/azblob: presign_ttl: %w", err)
		}
	}
	if opts.BlockSize != 0 && (opts.BlockSize < MinBlockSize || opts.BlockSize > MaxBlockSize) {
		return fmt.Errorf("storage/azblob: block_size %d outside %d..%d", opts.BlockSize, MinBlockSize, MaxBlockSize)
	}
	if opts.Concurrency < 0 || opts.Concurrency > MaxConcurrency {
		return fmt.Errorf("storage/azblob: concurrency %d outside 1..%d", opts.Concurrency, MaxConcurrency)
	}
	return nil
}

// Put implements [storage.Bucket] by staging Concurrency parallel blocks of
// BlockSize and committing the block list, so bodies of unknown length stream
// with bounded memory. Uncommitted blocks of a failed Put expire server-side.
func (b *Bucket) Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.Object, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return storage.Object{}, err
	}
	if r == nil {
		return storage.Object{}, errors.New("storage/azblob: body reader is nil")
	}
	metadata, err := toMetadata(opts.Metadata)
	if err != nil {
		return storage.Object{}, err
	}
	contentType := opts.ContentType
	if contentType == "" {
		contentType = storage.DefaultContentType
	}
	body := &countingReader{src: r}
	resp, err := b.container.NewBlockBlobClient(clean).UploadStream(ctx, body, &blockblob.UploadStreamOptions{
		BlockSize:   b.blockSize,
		Concurrency: b.concurrency,
		HTTPHeaders: &blob.HTTPHeaders{BlobContentType: new(contentType)},
		Metadata:    metadata,
	})
	if err != nil {
		return storage.Object{}, fmt.Errorf("storage/azblob: put %q: %w", clean, err)
	}
	return storage.Object{
		Key:          clean,
		Size:         body.n,
		ContentType:  contentType,
		Metadata:     fromMetadata(metadata),
		ETag:         etagString(resp.ETag),
		LastModified: deref(resp.LastModified),
	}, nil
}

// Get implements [storage.Bucket]; one Get Blob returns body and attributes.
func (b *Bucket) Get(ctx context.Context, key string) (io.ReadCloser, storage.Object, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return nil, storage.Object{}, err
	}
	resp, err := b.container.NewBlobClient(clean).DownloadStream(ctx, nil)
	if err != nil {
		return nil, storage.Object{}, mapError("get", clean, err)
	}
	return resp.Body, storage.Object{
		Key:          clean,
		Size:         deref(resp.ContentLength),
		ContentType:  deref(resp.ContentType),
		Metadata:     fromMetadata(resp.Metadata),
		ETag:         etagString(resp.ETag),
		LastModified: deref(resp.LastModified),
	}, nil
}

// Delete implements [storage.Bucket], removing snapshots too; deleting a
// missing key is not an error.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	clean, err := cleanKey(key)
	if err != nil {
		return err
	}
	_, err = b.container.NewBlobClient(clean).Delete(ctx, &blob.DeleteOptions{
		DeleteSnapshots: to.Ptr(blob.DeleteSnapshotsOptionTypeInclude),
	})
	if err != nil && !bloberror.HasCode(err, bloberror.BlobNotFound) {
		return fmt.Errorf("storage/azblob: delete %q: %w", clean, err)
	}
	return nil
}

// Exists implements [storage.Bucket].
func (b *Bucket) Exists(ctx context.Context, key string) (bool, error) {
	return storage.ExistsFromStat(b.Stat(ctx, key)) //nolint:wrapcheck // Stat already wrapped the error; the helper only maps ErrNotFound.
}

// Stat implements [storage.Bucket] via Get Blob Properties.
func (b *Bucket) Stat(ctx context.Context, key string) (storage.Object, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return storage.Object{}, err
	}
	props, err := b.container.NewBlobClient(clean).GetProperties(ctx, nil)
	if err != nil {
		return storage.Object{}, mapError("stat", clean, err)
	}
	return objectFromProperties(clean, &props), nil
}

// maxEmptyListPages bounds the List Blobs requests one List sends while the
// service returns empty pages with a continuation marker, as it may at
// partition boundaries, so an empty page always means the listing is complete.
const maxEmptyListPages = 256

// List implements [storage.Bucket] as one List Blobs page of at most Limit
// blobs, continued only past empty pages. StartAfter maps to the inclusive
// startFrom parameter (service version 2023-05-03 and later), so a blob named
// exactly StartAfter is skipped; an earlier name, as from a service that
// ignores startFrom, fails with [storage.ErrListOrder].
func (b *Bucket) List(ctx context.Context, opts storage.ListOptions) ([]storage.Object, error) {
	query, empty, err := storage.NormalizeListOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("storage/azblob: list: %w", err)
	}
	if empty {
		return nil, nil
	}
	items, err := b.listNonEmptyPage(ctx, query)
	if err != nil {
		return nil, err
	}
	return listedObjects(items, query)
}

func listBlobsOptions(query storage.ListOptions) *container.ListBlobsFlatOptions {
	pageSize := query.Limit
	if query.StartAfter != "" {
		pageSize++ // room for the skipped StartAfter blob
	}
	listOpts := &container.ListBlobsFlatOptions{
		MaxResults: new(int32(pageSize)), // #nosec G115 -- NormalizeListOptions proves 1..1000, plus one.
	}
	if query.Prefix != "" {
		listOpts.Prefix = new(query.Prefix)
	}
	if query.StartAfter != "" {
		listOpts.StartFrom = new(query.StartAfter)
	}
	return listOpts
}

func (b *Bucket) listNonEmptyPage(ctx context.Context, query storage.ListOptions) ([]*container.BlobItem, error) {
	pager := b.container.NewListBlobsFlatPager(listBlobsOptions(query))
	for range maxEmptyListPages {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("storage/azblob: list %q: %w", query.Prefix, err)
		}
		var items []*container.BlobItem
		if page.Segment != nil {
			items = page.Segment.BlobItems
		}
		if len(items) > 0 || !pager.More() {
			return items, nil
		}
	}
	return nil, fmt.Errorf("storage/azblob: list %q: %d consecutive empty pages", query.Prefix, maxEmptyListPages)
}

func listedObjects(items []*container.BlobItem, query storage.ListOptions) ([]storage.Object, error) {
	out := make([]storage.Object, 0, min(query.Limit, len(items)))
	for _, item := range items {
		if len(out) == query.Limit {
			break
		}
		name := deref(item.Name)
		if _, keyErr := cleanKey(name); keyErr != nil {
			return nil, fmt.Errorf("storage/azblob: unsafe listed key: %w", keyErr)
		}
		if name == query.StartAfter {
			continue
		}
		if name < query.StartAfter {
			return nil, fmt.Errorf(
				"storage/azblob: list %q: key %q after %q: %w", query.Prefix, name, query.StartAfter, storage.ErrListOrder,
			)
		}
		out = append(out, listedObject(name, item.Properties))
	}
	return out, nil
}

func listedObject(name string, p *container.BlobProperties) storage.Object {
	obj := storage.Object{Key: name}
	if p != nil {
		obj.Size = deref(p.ContentLength)
		obj.ETag = etagString(p.ETag)
		obj.LastModified = deref(p.LastModified)
	}
	return obj
}

func cleanKey(key string) (string, error) {
	clean, err := storage.CleanKey(key, storage.MaxKeyBytes)
	if err != nil {
		return "", fmt.Errorf("storage/azblob: validate key: %w", err)
	}
	return clean, nil
}

func mapError(op, key string, err error) error {
	if bloberror.HasCode(err, bloberror.BlobNotFound) {
		return fmt.Errorf("storage/azblob: %s %q: %w", op, key, storage.ErrNotFound)
	}
	return fmt.Errorf("storage/azblob: %s %q: %w", op, key, err)
}

func objectFromProperties(key string, props *blob.GetPropertiesResponse) storage.Object {
	return storage.Object{
		Key:          key,
		Size:         deref(props.ContentLength),
		ContentType:  deref(props.ContentType),
		Metadata:     fromMetadata(props.Metadata),
		ETag:         etagString(props.ETag),
		LastModified: deref(props.LastModified),
	}
}

type countingReader struct {
	src io.Reader
	n   int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	c.n += int64(n)
	return n, err //nolint:wrapcheck // io.Reader contract: io.EOF must pass through unwrapped.
}

var (
	_ storage.Bucket       = (*Bucket)(nil)
	_ storage.Copier       = (*Bucket)(nil)
	_ storage.PutPresigner = (*Bucket)(nil)
)
