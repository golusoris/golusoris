package tus

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"

	tusd "github.com/tus/tusd/v2/pkg/handler"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/storage"
)

// KeyFunc maps a finished tus FileInfo to the final storage.Bucket key. The
// default sanitizes the upload id under a configured prefix; override for
// tenant-scoped layouts. Implementations MUST reject traversal (no "..") and
// NUL bytes — keys flow into the Bucket boundary untrusted.
type KeyFunc func(info tusd.FileInfo) (string, error)

// defaultKeyFunc writes finished objects under prefix + sanitized id.
func defaultKeyFunc(prefix string) KeyFunc {
	clean := strings.Trim(prefix, "/")
	return func(info tusd.FileInfo) (string, error) {
		if err := validKeySegment(info.ID); err != nil {
			return "", err
		}
		if clean == "" {
			return info.ID, nil
		}
		return clean + "/" + info.ID, nil
	}
}

// validKeySegment rejects empty, traversal, separator and NUL segments.
func validKeySegment(seg string) error {
	if seg == "" || seg == "." || seg == ".." {
		return fmt.Errorf("tus: invalid key segment %q", seg)
	}
	if strings.ContainsAny(seg, "/\\\x00") || strings.Contains(seg, "..") {
		return fmt.Errorf("tus: invalid key segment %q", seg)
	}
	return nil
}

// sanitizeKey is the final guard applied to every KeyFunc result before it
// reaches the Bucket: it forbids traversal and absolute keys.
func sanitizeKey(key string) (string, error) {
	if key == "" || strings.ContainsAny(key, "\\\x00") {
		return "", fmt.Errorf("tus: invalid storage key %q", key)
	}
	clean := path.Clean("/" + key)
	if strings.Contains(key, "..") || clean == "/" {
		return "", fmt.Errorf("tus: invalid storage key %q", key)
	}
	return strings.TrimPrefix(clean, "/"), nil
}

// completionFn receives a finished upload after its bytes land in the Bucket.
type completionFn func(ctx context.Context, c CompletedUpload) error

// bucketStore implements tusd DataStore + TerminaterDataStore +
// LengthDeferrerDataStore, backed by an append-capable scratch area during the
// upload and the final storage.Bucket on FinishUpload.
type bucketStore struct {
	scratch  scratchStore
	bucket   storage.Bucket
	keyFn    KeyFunc
	clk      clock.Clock
	log      *slog.Logger
	onFinish completionFn
}

func newBucketStore(
	scratch scratchStore,
	bucket storage.Bucket,
	keyFn KeyFunc,
	clk clock.Clock,
	log *slog.Logger,
	onFinish completionFn,
) *bucketStore {
	return &bucketStore{
		scratch:  scratch,
		bucket:   bucket,
		keyFn:    keyFn,
		clk:      clk,
		log:      log,
		onFinish: onFinish,
	}
}

// NewUpload implements tusd.DataStore.
func (s *bucketStore) NewUpload(ctx context.Context, info tusd.FileInfo) (tusd.Upload, error) {
	if info.ID == "" {
		id, err := newUploadID()
		if err != nil {
			return nil, err
		}
		info.ID = id
	}
	entry, err := s.scratch.Create(ctx, info)
	if err != nil {
		return nil, fmt.Errorf("tus: create scratch upload: %w", err)
	}
	return &bucketUpload{store: s, entry: entry, id: info.ID}, nil
}

// GetUpload implements tusd.DataStore.
func (s *bucketStore) GetUpload(ctx context.Context, id string) (tusd.Upload, error) {
	entry, err := s.scratch.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("tus: get scratch upload %s: %w", id, err)
	}
	return &bucketUpload{store: s, entry: entry, id: id}, nil
}

// AsTerminatableUpload implements tusd.TerminaterDataStore.
func (s *bucketStore) AsTerminatableUpload(upload tusd.Upload) tusd.TerminatableUpload {
	return upload.(*bucketUpload)
}

// AsLengthDeclarableUpload implements tusd.LengthDeferrerDataStore.
func (s *bucketStore) AsLengthDeclarableUpload(upload tusd.Upload) tusd.LengthDeclarableUpload {
	return upload.(*bucketUpload)
}

// bucketUpload implements tusd.Upload (+ Terminatable + LengthDeclarable).
type bucketUpload struct {
	store *bucketStore
	entry scratchEntry
	id    string
}

func (u *bucketUpload) WriteChunk(ctx context.Context, offset int64, src io.Reader) (int64, error) {
	n, err := u.entry.WriteChunk(ctx, offset, src)
	if err != nil {
		return n, fmt.Errorf("tus: write chunk for %s: %w", u.id, err)
	}
	return n, nil
}

func (u *bucketUpload) GetInfo(ctx context.Context) (tusd.FileInfo, error) {
	info, err := u.entry.GetInfo(ctx)
	if err != nil {
		return info, fmt.Errorf("tus: get info for %s: %w", u.id, err)
	}
	return info, nil
}

func (u *bucketUpload) GetReader(ctx context.Context) (io.ReadCloser, error) {
	rc, err := u.entry.GetReader(ctx)
	if err != nil {
		return rc, fmt.Errorf("tus: get reader for %s: %w", u.id, err)
	}
	return rc, nil
}

func (u *bucketUpload) DeclareLength(ctx context.Context, length int64) error {
	if err := u.entry.DeclareLength(ctx, length); err != nil {
		return fmt.Errorf("tus: declare length for %s: %w", u.id, err)
	}
	return nil
}

func (u *bucketUpload) Terminate(ctx context.Context) error {
	if err := u.entry.Terminate(ctx); err != nil {
		return fmt.Errorf("tus: terminate %s: %w", u.id, err)
	}
	return nil
}

// FinishUpload streams the assembled scratch bytes into the Bucket under the
// KeyFunc key, then notifies the completion drain. Scratch is removed only
// after a successful Put so an interrupted finish can be retried.
func (u *bucketUpload) FinishUpload(ctx context.Context) error {
	info, err := u.entry.GetInfo(ctx)
	if err != nil {
		return fmt.Errorf("tus: get info for %s: %w", u.id, err)
	}
	key, err := u.store.keyFn(info)
	if err != nil {
		return err
	}
	key, err = sanitizeKey(key)
	if err != nil {
		return err
	}
	rc, err := u.entry.GetReader(ctx)
	if err != nil {
		return fmt.Errorf("tus: get reader for %s: %w", u.id, err)
	}
	obj, putErr := u.store.bucket.Put(ctx, key, rc, putOptions(info))
	if closeErr := rc.Close(); closeErr != nil && putErr == nil {
		putErr = closeErr
	}
	if putErr != nil {
		return fmt.Errorf("tus: persist upload %s: %w", u.id, putErr)
	}
	if termErr := u.entry.Terminate(ctx); termErr != nil {
		u.store.log.WarnContext(ctx, "tus: scratch cleanup failed", "id", u.id, "err", termErr)
	}
	completed := CompletedUpload{ID: u.id, Key: obj.Key, Size: obj.Size, MetaData: map[string]string(info.MetaData)}
	if u.store.onFinish != nil {
		if hookErr := u.store.onFinish(ctx, completed); hookErr != nil {
			return fmt.Errorf("tus: completion hook for %s: %w", u.id, hookErr)
		}
	}
	return nil
}

// putOptions maps tus metadata onto a storage PutOptions, surfacing the
// client-declared content type when present.
func putOptions(info tusd.FileInfo) storage.PutOptions {
	opts := storage.PutOptions{Metadata: map[string]string(info.MetaData)}
	if ct := info.MetaData["filetype"]; ct != "" {
		opts.ContentType = ct
	} else if ct := info.MetaData["type"]; ct != "" {
		opts.ContentType = ct
	}
	return opts
}
