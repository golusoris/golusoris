// Package storage provides a Bucket abstraction for object storage with
// a local-filesystem backend included. Cloud backends (S3, GCS, Azure Blob)
// implement the same interface so apps swap backends via fx.
//
// Usage:
//
//	bucket := storage.NewLocalBucket("/var/data/uploads")
//	obj, err := bucket.Put(ctx, "avatars/user-42.png", r, storage.PutOptions{
//	    ContentType: "image/png",
//	})
//	url, _ := bucket.URL(ctx, obj.Key)
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotFound is returned when an object does not exist in the bucket.
var ErrNotFound = errors.New("storage: object not found")

// Object is returned after a successful Put.
type Object struct {
	Key         string
	Size        int64
	ContentType string
	ETag        string
	LastModified time.Time
}

// PutOptions controls how an object is stored.
type PutOptions struct {
	ContentType string // default: "application/octet-stream"
	Metadata    map[string]string
}

// ListOptions filters a listing.
type ListOptions struct {
	Prefix string
	Limit  int // 0 = no limit
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
	// List returns objects whose keys begin with opts.Prefix.
	List(ctx context.Context, opts ListOptions) ([]Object, error)
	// URL returns a publicly accessible URL for key. May return an error
	// when the backend does not support public URLs.
	URL(ctx context.Context, key string) (string, error)
}

// --- LocalBucket ---

// LocalBucket stores objects as files under a base directory. Suitable for
// development and single-node deployments; not suitable for multi-replica.
type LocalBucket struct {
	base string
}

// NewLocalBucket returns a LocalBucket rooted at base. The directory is
// created if it does not exist.
func NewLocalBucket(base string) (*LocalBucket, error) {
	if err := os.MkdirAll(base, 0o750); err != nil {
		return nil, fmt.Errorf("storage: create base dir: %w", err)
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve base: %w", err)
	}
	return &LocalBucket{base: abs}, nil
}

func (b *LocalBucket) abs(key string) (string, error) {
	// Prevent path traversal.
	clean := filepath.Join(b.base, filepath.FromSlash(key))
	if !strings.HasPrefix(clean, b.base+string(os.PathSeparator)) && clean != b.base {
		return "", fmt.Errorf("storage: path traversal attempt: %q", key)
	}
	return clean, nil
}

func (b *LocalBucket) Put(_ context.Context, key string, r io.Reader, _ PutOptions) (Object, error) {
	path, err := b.abs(key)
	if err != nil {
		return Object{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return Object{}, fmt.Errorf("storage: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return Object{}, fmt.Errorf("storage: create: %w", err)
	}
	n, err := io.Copy(f, r)
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return Object{}, fmt.Errorf("storage: write: %w", err)
	}
	info, _ := os.Stat(path)
	return Object{Key: key, Size: n, LastModified: info.ModTime()}, nil
}

func (b *LocalBucket) Get(_ context.Context, key string) (io.ReadCloser, Object, error) {
	path, err := b.abs(key)
	if err != nil {
		return nil, Object{}, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, Object{}, ErrNotFound
	}
	if err != nil {
		return nil, Object{}, fmt.Errorf("storage: open: %w", err)
	}
	info, _ := f.Stat()
	obj := Object{Key: key, Size: info.Size(), LastModified: info.ModTime()}
	return f, obj, nil
}

func (b *LocalBucket) Delete(_ context.Context, key string) error {
	path, err := b.abs(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (b *LocalBucket) Exists(_ context.Context, key string) (bool, error) {
	path, err := b.abs(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (b *LocalBucket) List(_ context.Context, opts ListOptions) ([]Object, error) {
	var out []Object
	prefix := filepath.Join(b.base, filepath.FromSlash(opts.Prefix))
	err := filepath.WalkDir(b.base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasPrefix(path, prefix) {
			return nil
		}
		info, _ := d.Info()
		key := filepath.ToSlash(strings.TrimPrefix(path, b.base+string(os.PathSeparator)))
		out = append(out, Object{Key: key, Size: info.Size(), LastModified: info.ModTime()})
		if opts.Limit > 0 && len(out) >= opts.Limit {
			return io.EOF // sentinel to stop walk
		}
		return nil
	})
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return out, err
}

// URL returns a file:// URL for the object. For a public HTTP URL, configure
// an S3 or CDN-backed Bucket instead.
func (b *LocalBucket) URL(_ context.Context, key string) (string, error) {
	path, err := b.abs(key)
	if err != nil {
		return "", err
	}
	return "file://" + filepath.ToSlash(path), nil
}
