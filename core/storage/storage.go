// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned when an object does not exist in the bucket.
var ErrNotFound = errors.New("storage: object not found")

// ErrListWorkLimit is returned when a local listing exhausts its bounded
// traversal budget before it can produce the requested bounded snapshot.
var ErrListWorkLimit = errors.New("storage: local list work limit reached")

// Object describes stored data. List implementations may omit ContentType and
// Metadata when their native listing API does not return those fields.
type Object struct {
	Key          string
	Size         int64
	ContentType  string
	Metadata     map[string]string
	ETag         string
	LastModified time.Time
}

// PutOptions controls how an object is stored. Metadata is snapshotted before
// backend I/O; callers may mutate their input map after Put returns.
type PutOptions struct {
	ContentType string // default: "application/octet-stream"
	Metadata    map[string]string
}

const (
	// DefaultContentType is applied when PutOptions.ContentType is empty.
	DefaultContentType = "application/octet-stream"
	// DefaultListLimit is the object cap used when [ListOptions.Limit] is zero.
	DefaultListLimit = 1000
	// MaxListLimit is the largest object cap accepted by a single List call.
	MaxListLimit = 1000
)

// ListOptions filters a listing.
type ListOptions struct {
	Prefix string
	Limit  int // 0 uses DefaultListLimit; values above MaxListLimit are rejected
	// StartAfter keeps only keys sorting strictly after it, byte-wise; it need
	// not name an object. Pass a page's last key to list the next page, or use
	// [Walk]. Validated like Prefix.
	StartAfter string
}

// Bucket is the storage abstraction. Implementations must be safe for
// concurrent use.
type Bucket interface {
	// Put stores the data from r under key. The key should use forward
	// slashes as path separators regardless of OS.
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Object, error)
	// Get opens key for reading. Caller must close the returned ReadCloser.
	Get(ctx context.Context, key string) (io.ReadCloser, Object, error)
	// Delete removes key. Returns nil when the key does not exist.
	Delete(ctx context.Context, key string) error
	// Exists reports whether key exists.
	Exists(ctx context.Context, key string) (bool, error)
	// Stat returns metadata for key without fetching its body. Returns
	// [ErrNotFound] when key does not exist.
	Stat(ctx context.Context, key string) (Object, error)
	// List returns at most opts.Limit objects whose keys begin with opts.Prefix
	// and sort after opts.StartAfter, in ascending byte-wise key order. A zero
	// limit uses [DefaultListLimit]. A page may hold fewer objects than the
	// limit; only an empty page means no further keys match. ContentType and
	// Metadata may be omitted; use Stat when those fields are required. Local
	// listings may return [ErrListWorkLimit] for sparse scans; use a narrower
	// prefix.
	List(ctx context.Context, opts ListOptions) ([]Object, error)
	// URL returns a publicly accessible URL for key. May return an error
	// when the backend does not support public URLs.
	URL(ctx context.Context, key string) (string, error)
}

// ExistsFromStat maps a Stat result to Exists semantics: [ErrNotFound]
// becomes (false, nil), so backends without a cheaper probe share one rule.
func ExistsFromStat(_ Object, err error) (bool, error) {
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
