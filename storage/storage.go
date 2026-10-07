// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/internal/dirsync"
)

var (
	// ErrNotFound is returned when an object does not exist in the bucket.
	ErrNotFound = errors.New("storage: object not found")
	// ErrListWorkLimit is returned when a local listing exhausts its bounded
	// traversal budget before it can produce the requested bounded snapshot.
	ErrListWorkLimit = errors.New("storage: local list work limit reached")
)

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
	// DefaultListLimit is the object cap used when [ListOptions.Limit] is zero.
	DefaultListLimit = 1000
	// MaxListLimit is the largest object cap accepted by a single List call.
	MaxListLimit = 1000
)

// ListOptions filters a listing.
type ListOptions struct {
	Prefix string
	Limit  int // 0 uses DefaultListLimit; values above MaxListLimit are rejected
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
	// List returns at most opts.Limit objects whose keys begin with opts.Prefix.
	// A zero limit uses [DefaultListLimit]. ContentType and Metadata may be
	// omitted; use Stat when those fields are required. Local listings may
	// return [ErrListWorkLimit] for sparse scans; use a narrower prefix.
	List(ctx context.Context, opts ListOptions) ([]Object, error)
	// URL returns a publicly accessible URL for key. May return an error
	// when the backend does not support public URLs.
	URL(ctx context.Context, key string) (string, error)
}

// --- LocalBucket ---

// LocalBucket stores objects as files under a base directory. Suitable for
// development and single-node deployments; not suitable for multi-replica.
type LocalBucket struct {
	base       string
	syncParent *localParentSyncer
}

type localParentSyncer struct {
	run func(root *os.Root, parent string) error
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
	return &LocalBucket{base: abs, syncParent: &localParentSyncer{run: syncLocalParent}}, nil
}

func localKey(key string) (string, string, error) {
	clean, err := CleanKey(key, MaxKeyBytes)
	if err != nil {
		return "", "", fmt.Errorf("storage: validate local key: %w", err)
	}
	return clean, filepath.FromSlash(clean), nil
}

func (b *LocalBucket) openRoot() (*os.Root, error) {
	root, err := os.OpenRoot(b.base)
	if err != nil {
		return nil, fmt.Errorf("storage: open base root: %w", err)
	}
	return root, nil
}

// Put implements [Bucket].
func (b *LocalBucket) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (obj Object, err error) {
	if err = checkLocalContext(ctx, "put preflight"); err != nil {
		return Object{}, err
	}
	clean, name, err := localKey(key)
	if err != nil {
		return Object{}, err
	}
	attrs := snapshotLocalAttributes(clean, opts)
	root, err := b.openRoot()
	if err != nil {
		return Object{}, err
	}
	defer gerr.CloseInto(root, &err, "storage: close base root")
	operationLock, err := b.lockAndRecover(ctx, root, "put before staging")
	if err != nil {
		return Object{}, err
	}
	defer gerr.CloseInto(operationLock, &err, "storage: release local put lock")
	return b.putLocalObject(ctx, root, clean, name, r, attrs)
}

func (b *LocalBucket) putLocalObject(
	ctx context.Context,
	root *os.Root,
	clean string,
	name string,
	src io.Reader,
	attrs localAttributes,
) (obj Object, err error) {
	if ctxErr := checkLocalContext(ctx, "put before mkdir"); ctxErr != nil {
		return Object{}, ctxErr
	}
	parent := filepath.Dir(name)
	createdDirectories, err := ensureLocalDirectories(ctx, root, parent)
	if err != nil {
		return Object{}, err
	}
	tempName, n, digest, err := stageLocalObject(ctx, root, src)
	if err != nil {
		return Object{}, err
	}
	attrs, encodedAttrs, err := bindLocalAttributes(attrs, n, digest)
	if err != nil {
		return Object{}, errors.Join(err, removeLocalTemp(root, tempName))
	}
	metadataTemp, err := stageLocalAttributes(ctx, root, encodedAttrs)
	if err != nil {
		return Object{}, errors.Join(err, removeLocalTemp(root, tempName))
	}
	if err = b.publishStagedLocalObject(
		ctx, root, clean, name, parent, tempName, metadataTemp,
		n, digest, encodedAttrs, createdDirectories,
	); err != nil {
		return Object{}, err
	}
	info, err := root.Stat(name)
	if err != nil {
		return Object{}, fmt.Errorf("storage: stat written object: %w", err)
	}
	return objectWithAttributes(Object{Key: clean, Size: n, LastModified: info.ModTime()}, attrs), nil
}

func (b *LocalBucket) publishStagedLocalObject(
	ctx context.Context,
	root *os.Root,
	key, objectTarget, parent, objectStage, metadataStage string,
	bodySize int64,
	bodyDigest string,
	metadata []byte,
	createdDirectories []string,
) (err error) {
	if err = checkLocalContext(ctx, "put before publish"); err != nil {
		return errors.Join(err, cleanupLocalStages(root, objectStage, metadataStage))
	}
	tx, err := prepareLocalTransaction(
		root, key, objectTarget, objectStage, metadataStage, bodySize, bodyDigest, metadata,
	)
	if err != nil {
		return errors.Join(err, cleanupLocalStages(root, objectStage, metadataStage))
	}
	started, beginErr := beginLocalTransaction(ctx, root, tx)
	if beginErr != nil {
		if !started {
			return errors.Join(beginErr, cleanupLocalStages(root, objectStage, metadataStage))
		}
		recoveryErr := recoverLocalTransactionAfterFailure(ctx, root)
		return errors.Join(beginErr, recoveryErr)
	}
	if err = publishLocalTransaction(root, tx); err != nil {
		recoveryErr := recoverLocalTransactionAfterFailure(ctx, root)
		return errors.Join(err, recoveryErr)
	}
	if syncErr := b.syncPublishedDirectories(root, parent, createdDirectories); syncErr != nil {
		return syncErr
	}
	return finishLocalTransaction(root, tx)
}

func prepareLocalTransaction(
	root *os.Root,
	key, objectTarget, objectStage, metadataStage string,
	bodySize int64,
	bodyDigest string,
	metadata []byte,
) (localTransaction, error) {
	hadObject, err := localRegularPathExists(root, objectTarget, "object")
	if err != nil {
		return localTransaction{}, err
	}
	metadataTarget := localMetadataPath(key, objectTarget)
	hadMetadata, err := localRegularPathExists(root, metadataTarget, "attributes")
	if err != nil {
		return localTransaction{}, err
	}
	return newLocalTransaction(
		key, objectTarget, objectStage, metadataStage,
		bodySize, bodyDigest, metadata, hadObject, hadMetadata,
	), nil
}

func ensureLocalDirectories(ctx context.Context, root *os.Root, parent string) ([]string, error) {
	if parent == "." {
		return nil, nil
	}
	created := make([]string, 0, strings.Count(filepath.ToSlash(parent), "/")+1)
	current := ""
	for segment := range strings.SplitSeq(filepath.ToSlash(parent), "/") {
		if err := checkLocalContext(ctx, "put while creating directories"); err != nil {
			return nil, err
		}
		current = filepath.Join(current, segment)
		err := root.Mkdir(current, 0o750)
		if err == nil {
			created = append(created, current)
			continue
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("storage: mkdir %q: %w", current, err)
		}
		info, statErr := root.Lstat(current)
		if statErr != nil {
			return nil, fmt.Errorf("storage: inspect existing directory %q: %w", current, statErr)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("storage: mkdir %q: existing path is not a directory", current)
		}
	}
	return created, nil
}

func (b *LocalBucket) syncPublishedDirectories(root *os.Root, parent string, created []string) error {
	if err := b.syncPublishedDirectory(root, parent); err != nil {
		return err
	}
	lastSynced := parent
	for _, c := range slices.Backward(created) {
		ancestor := filepath.Dir(c)
		if ancestor == lastSynced {
			continue
		}
		if err := b.syncPublishedDirectory(root, ancestor); err != nil {
			return err
		}
		lastSynced = ancestor
	}
	return nil
}

func (b *LocalBucket) syncPublishedDirectory(root *os.Root, directory string) error {
	if err := b.syncParent.run(root, directory); err != nil {
		return fmt.Errorf("storage: sync published directory %q: %w", directory, err)
	}
	return nil
}

func checkLocalContext(ctx context.Context, phase string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: local %s: %w", phase, err)
	}
	return nil
}

func (b *LocalBucket) lockAndRecover(
	ctx context.Context, root *os.Root, phase string,
) (*localOperationLock, error) {
	operationLock, err := acquireLocalOperationLock(ctx, root, phase)
	if err != nil {
		return nil, err
	}
	if err = recoverPendingLocalTransaction(ctx, root); err != nil {
		return nil, errors.Join(err, operationLock.Close())
	}
	if err = reclaimLocalOrphanStages(ctx, root); err != nil {
		return nil, errors.Join(err, operationLock.Close())
	}
	return operationLock, nil
}

func stageLocalObject(
	ctx context.Context, root *os.Root, src io.Reader,
) (string, int64, string, error) {
	f, tempName, err := openLocalTemp(root)
	if err != nil {
		return "", 0, "", err
	}
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hasher), checkedReader{check: ctx.Err, src: src})
	if copyErr != nil {
		return "", 0, "", closeAndRemoveLocalTemp(
			root, f, tempName, fmt.Errorf("storage: stage write: %w", copyErr),
		)
	}
	if syncErr := f.Sync(); syncErr != nil {
		return "", 0, "", closeAndRemoveLocalTemp(
			root, f, tempName, fmt.Errorf("storage: sync staged object: %w", syncErr),
		)
	}
	if closeErr := f.Close(); closeErr != nil {
		cleanupErr := removeLocalTemp(root, tempName)
		return "", 0, "", errors.Join(fmt.Errorf("storage: close staged object: %w", closeErr), cleanupErr)
	}
	return tempName, n, hex.EncodeToString(hasher.Sum(nil)), nil
}

func openLocalTemp(root *os.Root) (*os.File, string, error) {
	const maxAttempts = 4
	for range maxAttempts {
		name := filepath.Join(localStagingDirectoryName, ".golusoris-put-"+rand.Text()+".tmp")
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return f, name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", fmt.Errorf("storage: create staged object: %w", err)
		}
	}
	return nil, "", fmt.Errorf("storage: create staged object: %w", fs.ErrExist)
}

func closeAndRemoveLocalTemp(root *os.Root, f *os.File, name string, cause error) error {
	if closeErr := f.Close(); closeErr != nil {
		cause = errors.Join(cause, fmt.Errorf("storage: close failed staged object: %w", closeErr))
	}
	return errors.Join(cause, removeLocalTemp(root, name))
}

func removeLocalTemp(root *os.Root, name string) error {
	if name == "" {
		return nil
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: remove staged object: %w", err)
	}
	return nil
}

func cleanupLocalStages(root *os.Root, names ...string) error {
	var errs []error
	for _, name := range names {
		if err := removeLocalTemp(root, name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func syncLocalParent(root *os.Root, parent string) (err error) {
	dir, err := root.Open(parent)
	if err != nil {
		return fmt.Errorf("storage: open parent for sync: %w", err)
	}
	defer gerr.CloseInto(dir, &err, "storage: close synced parent")
	if err = dirsync.Sync(dir); err != nil {
		return fmt.Errorf("storage: fsync parent: %w", err)
	}
	return nil
}

type checkedReader struct {
	check func() error
	src   io.Reader
}

func (r checkedReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, fmt.Errorf("storage: read context: %w", err)
	}
	n, err := r.src.Read(p)
	if contextErr := r.check(); contextErr != nil {
		return n, fmt.Errorf("storage: read context: %w", contextErr)
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("storage: read source: %w", err)
	}
	return n, nil
}

// Get implements [Bucket].
func (b *LocalBucket) Get(ctx context.Context, key string) (rc io.ReadCloser, obj Object, err error) {
	if err = checkLocalContext(ctx, "get preflight"); err != nil {
		return nil, Object{}, err
	}
	clean, name, err := localKey(key)
	if err != nil {
		return nil, Object{}, err
	}
	root, err := b.openRoot()
	if err != nil {
		return nil, Object{}, err
	}
	operationLock, err := b.lockAndRecover(ctx, root, "get before open")
	if err != nil {
		gerr.CloseJoin(root, &err, "storage: close base root")
		return nil, Object{}, err
	}
	defer gerr.CloseInto(operationLock, &err, "storage: release local get lock")
	defer func() {
		if err != nil && rc != nil {
			gerr.CloseJoin(rc, &err, "storage: close object after root failure")
			rc = nil
			obj = Object{}
		}
	}()
	defer gerr.CloseInto(root, &err, "storage: close base root")
	return openLocalObject(ctx, root, clean, name)
}

func openLocalObject(
	ctx context.Context, root *os.Root, clean string, name string,
) (io.ReadCloser, Object, error) {
	if ctxErr := checkLocalContext(ctx, "get before object open"); ctxErr != nil {
		return nil, Object{}, ctxErr
	}
	f, found, err := openBoundedLocalFile(root, name, "local object")
	if err != nil {
		return nil, Object{}, err
	}
	if !found {
		return nil, Object{}, ErrNotFound
	}
	obj, err := inspectLocalObject(ctx, root, clean, name, f)
	if err != nil {
		return nil, Object{}, closeLocalObjectAfterError(f, err)
	}
	if ctxErr := checkLocalContext(ctx, "get after inspect"); ctxErr != nil {
		return nil, Object{}, closeLocalObjectAfterError(f, ctxErr)
	}
	return f, obj, nil
}

func closeLocalObjectAfterError(file *os.File, cause error) error {
	if closeErr := file.Close(); closeErr != nil {
		return errors.Join(cause, closeErr)
	}
	return cause
}

func inspectLocalObject(
	ctx context.Context, root *os.Root, clean, name string, file *os.File,
) (Object, error) {
	info, err := file.Stat()
	if err != nil {
		return Object{}, fmt.Errorf("storage: stat open object: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Object{}, errors.New("storage: local object must be a regular file")
	}
	attrs, err := readLocalObjectAttributes(ctx, root, clean, name)
	if err != nil {
		return Object{}, err
	}
	if err = verifyLocalObjectAttributes(ctx, file, info, attrs); err != nil {
		return Object{}, err
	}
	return objectWithAttributes(Object{Key: clean, Size: info.Size(), LastModified: info.ModTime()}, attrs), nil
}

func verifyLocalObjectAttributes(
	ctx context.Context, file *os.File, info os.FileInfo, attrs localAttributes,
) error {
	if attrs.BodySHA256 == "" {
		return nil
	}
	if info.Size() != attrs.BodySize {
		return fmt.Errorf(
			"storage: local object size %d does not match attributes size %d", info.Size(), attrs.BodySize,
		)
	}
	digest, err := localOpenFileDigest(ctx, file)
	if err != nil {
		return err
	}
	if digest != attrs.BodySHA256 {
		return errors.New("storage: local object digest does not match attributes")
	}
	return nil
}

func localOpenFileDigest(ctx context.Context, file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("storage: seek local object before digest: %w", err)
	}
	hasher := sha256.New()
	_, hashErr := io.Copy(hasher, checkedReader{check: ctx.Err, src: file})
	_, seekErr := file.Seek(0, io.SeekStart)
	if seekErr != nil {
		seekErr = fmt.Errorf("storage: restore local object offset: %w", seekErr)
	}
	if hashErr != nil || seekErr != nil {
		return "", errors.Join(hashErr, seekErr)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func localBodyMatches(
	ctx context.Context, root *os.Root, name string, size int64, digest string,
) (_ bool, err error) {
	before, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: inspect local transaction body: %w", err)
	}
	if !before.Mode().IsRegular() {
		return false, nil
	}
	file, err := root.Open(name)
	if err != nil {
		return false, fmt.Errorf("storage: open local transaction body: %w", err)
	}
	defer gerr.CloseInto(file, &err, "storage: close local transaction body")
	opened, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("storage: stat local transaction body: %w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return false, errors.New("storage: local transaction body changed during open")
	}
	if opened.Size() != size {
		return false, nil
	}
	gotDigest, err := localOpenFileDigest(ctx, file)
	if err != nil {
		return false, err
	}
	return gotDigest == digest, nil
}

func readLocalObjectAttributes(
	ctx context.Context, root *os.Root, clean, name string,
) (localAttributes, error) {
	return readLocalAttributes(ctx, root, clean, name)
}

// Delete implements [Bucket].
func (b *LocalBucket) Delete(ctx context.Context, key string) (err error) {
	if err = checkLocalContext(ctx, "delete preflight"); err != nil {
		return err
	}
	clean, name, err := localKey(key)
	if err != nil {
		return err
	}
	root, err := b.openRoot()
	if err != nil {
		return err
	}
	defer gerr.CloseInto(root, &err, "storage: close base root")
	operationLock, err := b.lockAndRecover(ctx, root, "delete before remove")
	if err != nil {
		return err
	}
	defer gerr.CloseInto(operationLock, &err, "storage: release local delete lock")
	if err = checkLocalContext(ctx, "delete before remove"); err != nil {
		return err
	}
	objectRemoved, err := removeLocalObject(root, name)
	if err != nil {
		return err
	}
	metadataRemoved, metadataErr := removeLocalAttributes(root, localMetadataPath(clean, name))
	if !objectRemoved && !metadataRemoved {
		return metadataErr
	}
	syncErr := b.syncParent.run(root, filepath.Dir(name))
	if syncErr != nil {
		syncErr = fmt.Errorf("storage: sync deleted parent: %w", syncErr)
	}
	return errors.Join(metadataErr, syncErr)
}

func removeLocalObject(root *os.Root, name string) (bool, error) {
	err := root.Remove(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: remove: %w", err)
	}
	return true, nil
}

// Exists implements [Bucket].
func (b *LocalBucket) Exists(ctx context.Context, key string) (exists bool, err error) {
	if err = checkLocalContext(ctx, "exists preflight"); err != nil {
		return false, err
	}
	_, name, err := localKey(key)
	if err != nil {
		return false, err
	}
	root, err := b.openRoot()
	if err != nil {
		return false, err
	}
	defer gerr.CloseInto(root, &err, "storage: close base root")
	operationLock, err := b.lockAndRecover(ctx, root, "exists before stat")
	if err != nil {
		return false, err
	}
	defer gerr.CloseInto(operationLock, &err, "storage: release local exists lock")
	if err = checkLocalContext(ctx, "exists before stat"); err != nil {
		return false, err
	}
	file, found, openErr := openBoundedLocalFile(root, name, "local object")
	if openErr != nil {
		return false, openErr
	}
	if !found {
		return false, nil
	}
	if closeErr := file.Close(); closeErr != nil {
		return false, fmt.Errorf("storage: close exists object: %w", closeErr)
	}
	if err = checkLocalContext(ctx, "exists after stat"); err != nil {
		return false, err
	}
	return true, nil
}

// Stat implements [Bucket].
func (b *LocalBucket) Stat(ctx context.Context, key string) (obj Object, err error) {
	if err = checkLocalContext(ctx, "stat preflight"); err != nil {
		return Object{}, err
	}
	clean, name, err := localKey(key)
	if err != nil {
		return Object{}, err
	}
	root, err := b.openRoot()
	if err != nil {
		return Object{}, err
	}
	operationLock, err := b.lockAndRecover(ctx, root, "stat before open")
	if err != nil {
		gerr.CloseJoin(root, &err, "storage: close base root")
		return Object{}, err
	}
	defer gerr.CloseInto(operationLock, &err, "storage: release local stat lock")
	defer gerr.CloseInto(root, &err, "storage: close base root")
	if err = checkLocalContext(ctx, "stat before object open"); err != nil {
		return Object{}, err
	}
	file, found, err := openBoundedLocalFile(root, name, "local object")
	if err != nil {
		return Object{}, err
	}
	if !found {
		return Object{}, ErrNotFound
	}
	defer gerr.CloseInto(file, &err, "storage: close stat object")
	obj, err = inspectLocalObject(ctx, root, clean, name, file)
	if err != nil {
		return Object{}, err
	}
	if err = checkLocalContext(ctx, "stat after inspect"); err != nil {
		return Object{}, err
	}
	return obj, nil
}

// List implements [Bucket].
func (b *LocalBucket) List(ctx context.Context, opts ListOptions) (out []Object, err error) {
	limit, err := normalizeListLimit(opts.Limit)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: list local objects: %w", err)
	}
	root, err := b.openRoot()
	if err != nil {
		return nil, err
	}
	defer gerr.CloseInto(root, &err, "storage: close base root")
	operationLock, err := b.lockAndRecover(ctx, root, "list before walk")
	if err != nil {
		return nil, err
	}
	defer gerr.CloseInto(operationLock, &err, "storage: release local list lock")
	out, err = walkLocalObjects(ctx, root.FS(), opts.Prefix, limit)
	if errors.Is(err, errListLimitReached) {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: list local objects: %w", err)
	}
	return out, nil
}

var errListLimitReached = errors.New("storage: list limit reached")

const (
	localListReadBatch     = 32
	localListBaseWork      = 64
	localListWorkPerObject = 16
)

func normalizeListLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultListLimit, nil
	}
	if limit < 0 || limit > MaxListLimit {
		return 0, fmt.Errorf("storage: list limit %d outside range 1..%d", limit, MaxListLimit)
	}
	return limit, nil
}

func walkLocalObjects(ctx context.Context, rootFS fs.FS, prefix string, limit int) ([]Object, error) {
	cleanPrefix, err := cleanListPrefix(prefix)
	if err != nil {
		return nil, fmt.Errorf("storage: validate local list prefix: %w", err)
	}
	walker := localListWalker{
		check:     ctx.Err,
		rootFS:    rootFS,
		prefix:    cleanPrefix,
		limit:     limit,
		remaining: localListWorkLimit(limit),
		out:       make([]Object, 0, min(limit, 16)),
	}
	err = walker.walk(localListStart(cleanPrefix))
	return walker.out, err
}

type localListWalker struct {
	check     func() error
	rootFS    fs.FS
	prefix    string
	limit     int
	remaining int
	out       []Object
}

func (w *localListWalker) walk(start string) error {
	if err := w.check(); err != nil {
		return err
	}
	info, err := fs.Lstat(w.rootFS, start)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: inspect list root %q: %w", start, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil
	}
	if !info.IsDir() {
		return w.appendInfo(start, info)
	}
	directories := []string{start}
	for len(directories) > 0 {
		if err = w.check(); err != nil {
			return err
		}
		children, scanErr := w.scanDirectory(directories[0])
		if scanErr != nil {
			return scanErr
		}
		directories = append(directories[1:], children...)
	}
	return nil
}

func (w *localListWalker) scanDirectory(name string) (children []string, err error) {
	file, err := w.rootFS.Open(name)
	if err != nil {
		return nil, fmt.Errorf("storage: open listed directory %q: %w", name, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("storage: close listed directory %q: %w", name, closeErr))
		}
	}()
	directory, ok := file.(fs.ReadDirFile)
	if !ok {
		return nil, fmt.Errorf("storage: listed directory %q does not support bounded reads", name)
	}
	return w.readDirectory(name, directory)
}

func (w *localListWalker) readDirectory(name string, directory fs.ReadDirFile) ([]string, error) {
	var children []string
	for w.remaining > 0 {
		if err := w.check(); err != nil {
			return nil, err
		}
		entries, readErr := directory.ReadDir(min(localListReadBatch, w.remaining))
		w.remaining -= len(entries)
		for _, entry := range entries {
			if err := w.visitEntry(name, entry, &children); err != nil {
				return nil, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return children, nil
		}
		if readErr != nil {
			return nil, fmt.Errorf("storage: read listed directory %q: %w", name, readErr)
		}
		if len(entries) == 0 {
			return nil, fmt.Errorf("storage: read listed directory %q: %w", name, io.ErrNoProgress)
		}
	}
	return nil, ErrListWorkLimit
}

func (w *localListWalker) visitEntry(directory string, entry fs.DirEntry, children *[]string) error {
	if entry.Type()&fs.ModeSymlink != 0 || isLocalInternalName(entry.Name()) {
		return nil
	}
	name := pathpkg.Join(directory, entry.Name())
	if entry.IsDir() {
		if localDirectoryCanMatch(name, w.prefix) {
			*children = append(*children, name)
		}
		return nil
	}
	if !strings.HasPrefix(name, w.prefix) {
		return nil
	}
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("storage: stat listed object %q: %w", name, err)
	}
	return w.appendInfo(name, info)
}

func (w *localListWalker) appendInfo(name string, info fs.FileInfo) error {
	if isLocalInternalName(pathpkg.Base(name)) || !strings.HasPrefix(name, w.prefix) {
		return nil
	}
	w.out = append(w.out, Object{Key: name, Size: info.Size(), LastModified: info.ModTime()})
	if len(w.out) >= w.limit {
		return errListLimitReached
	}
	return nil
}

func isLocalInternalName(name string) bool {
	return isLocalTempName(name) || isLocalMetadataName(name) || isLocalControlName(name)
}

func localListStart(prefix string) string {
	if before, ok := strings.CutSuffix(prefix, "/"); ok {
		return before
	}
	if separator := strings.LastIndexByte(prefix, '/'); separator >= 0 {
		return prefix[:separator]
	}
	return "."
}

func localDirectoryCanMatch(directory, prefix string) bool {
	if directory == "." || prefix == "" {
		return true
	}
	directoryPrefix := directory + "/"
	return strings.HasPrefix(prefix, directoryPrefix) || strings.HasPrefix(directoryPrefix, prefix)
}

func localListWorkLimit(limit int) int {
	return localListBaseWork + localListWorkPerObject*limit
}

// URL returns a file:// URL for the object. For a public HTTP URL, configure
// an S3 or CDN-backed Bucket instead.
func (b *LocalBucket) URL(ctx context.Context, key string) (value string, err error) {
	if err = checkLocalContext(ctx, "url preflight"); err != nil {
		return "", err
	}
	clean, name, err := localKey(key)
	if err != nil {
		return "", err
	}
	root, err := b.openRoot()
	if err != nil {
		return "", err
	}
	defer gerr.CloseInto(root, &err, "storage: close base root")
	operationLock, err := b.lockAndRecover(ctx, root, "url before stat")
	if err != nil {
		return "", err
	}
	defer gerr.CloseInto(operationLock, &err, "storage: release local URL lock")
	if err = checkLocalContext(ctx, "url before stat"); err != nil {
		return "", err
	}
	if err = validateLocalURLTarget(root, name); err != nil {
		return "", err
	}
	if err = checkLocalContext(ctx, "url after stat"); err != nil {
		return "", err
	}
	path := filepath.ToSlash(filepath.Join(b.base, filepath.FromSlash(clean)))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String(), nil
}

func validateLocalURLTarget(root *os.Root, name string) error {
	file, found, err := openBoundedLocalFile(root, name, "local URL object")
	if err != nil || !found {
		return err
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("storage: close local URL object: %w", err)
	}
	return nil
}
