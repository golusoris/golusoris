// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"path"
	"strings"

	tusd "github.com/tus/tusd/v2/pkg/handler"

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

// deliveryFn runs the durable callback chain for one upload id.
type deliveryFn func(ctx context.Context, id string) error

// bucketStore implements tusd DataStore + TerminaterDataStore +
// LengthDeferrerDataStore, backed by an append-capable scratch area during the
// upload and the final storage.Bucket on FinishUpload.
type bucketStore struct {
	scratch scratchStore
	bucket  storage.Bucket
	keyFn   KeyFunc
	log     *slog.Logger
	deliver deliveryFn
}

func newBucketStore(
	scratch scratchStore,
	bucket storage.Bucket,
	keyFn KeyFunc,
	log *slog.Logger,
	deliver deliveryFn,
) *bucketStore {
	return &bucketStore{
		scratch: scratch,
		bucket:  bucket,
		keyFn:   keyFn,
		log:     log,
		deliver: deliver,
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

// ResumeCompletion advances a durable completion stage after restart. A
// persisted receipt no longer requires the scratch bytes.
func (s *bucketStore) ResumeCompletion(ctx context.Context, id string) error {
	entry, err := s.scratch.Get(ctx, id)
	if err == nil {
		return (&bucketUpload{store: s, entry: entry, id: id}).FinishUpload(ctx)
	}
	record, recordErr := s.scratch.Completion(ctx, id)
	if recordErr != nil {
		if errors.Is(err, tusd.ErrNotFound) && errors.Is(recordErr, tusd.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("tus: resume completion %s: %w", id, errors.Join(err, recordErr))
	}
	if record.normalizedStage() != completionStagePersisted {
		return fmt.Errorf("tus: resume %s completion %s without scratch: %w", record.normalizedStage(), id, err)
	}
	return s.acknowledgeCompletion(ctx, id)
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

// FinishUpload first records a durable intent, then reconciles the object and
// advances the receipt before invoking the checkpointed callback chain.
func (u *bucketUpload) FinishUpload(ctx context.Context) error {
	record, err := u.store.scratch.Completion(ctx, u.id)
	if err == nil {
		return u.resumeCompletion(ctx, record)
	}
	if !errors.Is(err, tusd.ErrNotFound) {
		return fmt.Errorf("tus: load completion for %s: %w", u.id, err)
	}
	info, err := u.entry.GetInfo(ctx)
	if err != nil {
		return fmt.Errorf("tus: get info for %s: %w", u.id, err)
	}
	key, err := u.resolveKey(info)
	if err != nil {
		return err
	}
	scratchReader, err := u.entry.GetReader(ctx)
	if err != nil {
		return fmt.Errorf("tus: open scratch fingerprint for %s: %w", u.id, err)
	}
	digest, _, err := hashAndClose(ctx, scratchReader, info.Offset)
	if err != nil {
		return fmt.Errorf("tus: fingerprint scratch for %s: %w", u.id, err)
	}
	record = newPreparedCompletionRecord(CompletedUpload{
		ID: u.id, Key: key, Size: info.Offset, MetaData: map[string]string(info.MetaData),
	}, hex.EncodeToString(digest[:]))
	if err = u.store.scratch.SaveCompletion(ctx, record); err != nil {
		return fmt.Errorf("tus: prepare completion for %s: %w", u.id, err)
	}
	return u.resumeCompletion(ctx, record)
}

func (u *bucketUpload) resumeCompletion(ctx context.Context, record completionRecord) error {
	switch record.normalizedStage() {
	case completionStagePrepared:
		record.Stage = completionStagePutting
		if err := u.store.scratch.SaveCompletion(ctx, record); err != nil {
			return fmt.Errorf("tus: checkpoint Put attempt for %s: %w", u.id, err)
		}
		fallthrough
	case completionStagePutting:
		obj, err := u.reconcileObject(ctx, record)
		if err != nil {
			return fmt.Errorf("tus: persist upload %s: %w", u.id, err)
		}
		if obj.Key != record.Upload.Key || obj.Size != record.Upload.Size {
			return fmt.Errorf(
				"tus: persisted upload identity mismatch: key=%q size=%d, want key=%q size=%d",
				obj.Key, obj.Size, record.Upload.Key, record.Upload.Size,
			)
		}
		record.Stage = completionStagePersisted
		if err = u.store.scratch.SaveCompletion(ctx, record); err != nil {
			return fmt.Errorf("tus: checkpoint persisted completion for %s: %w", u.id, err)
		}
	case completionStagePersisted:
	default:
		return fmt.Errorf("tus: invalid completion stage %q for %s", record.Stage, u.id)
	}
	return u.store.acknowledgeCompletion(ctx, u.id)
}

// reconcileObject skips Put only when the destination can be read back and is
// byte-identical to scratch. An absent or different object is overwritten.
func (u *bucketUpload) reconcileObject(
	ctx context.Context, record completionRecord,
) (storage.Object, error) {
	if err := u.verifyScratchIdentity(ctx, record.Upload); err != nil {
		return storage.Object{}, err
	}
	obj, matches, err := u.objectMatchesScratch(ctx, record.Upload)
	if err != nil {
		return storage.Object{}, err
	}
	if matches {
		return obj, nil
	}
	rc, err := u.entry.GetReader(ctx)
	if err != nil {
		return storage.Object{}, fmt.Errorf("get scratch reader: %w", err)
	}
	info := tusd.FileInfo{MetaData: tusd.MetaData(maps.Clone(record.Upload.MetaData))}
	return u.putAndClose(ctx, record.Upload.Key, rc, info)
}

func (u *bucketUpload) verifyScratchIdentity(ctx context.Context, expected completionUpload) error {
	want, err := completionDigest(expected)
	if err != nil {
		return err
	}
	scratchReader, err := u.entry.GetReader(ctx)
	if err != nil {
		return fmt.Errorf("open scratch integrity check: %w", err)
	}
	got, _, err := hashAndClose(ctx, scratchReader, expected.Size)
	if err != nil {
		return fmt.Errorf("verify scratch integrity: %w", err)
	}
	if got != want {
		return errors.New("scratch content changed after completion checkpoint")
	}
	return nil
}

func completionDigest(expected completionUpload) ([sha256.Size]byte, error) {
	decoded, err := hex.DecodeString(expected.SHA256)
	if err != nil || len(decoded) != sha256.Size {
		return [sha256.Size]byte{}, errors.New("invalid completion digest")
	}
	var digest [sha256.Size]byte
	copy(digest[:], decoded)
	return digest, nil
}

func (u *bucketUpload) objectMatchesScratch(
	ctx context.Context, expected completionUpload,
) (storage.Object, bool, error) {
	bucketReader, obj, err := u.store.bucket.Get(ctx, expected.Key)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Object{}, false, nil
	}
	if err != nil {
		return storage.Object{}, false, fmt.Errorf("probe destination: %w", err)
	}
	if obj.Size != expected.Size || !objectAttributesMatch(obj, expected.MetaData) {
		if closeErr := bucketReader.Close(); closeErr != nil {
			return storage.Object{}, false, fmt.Errorf("close destination probe: %w", closeErr)
		}
		return storage.Object{}, false, nil
	}
	want, err := completionDigest(expected)
	if err != nil {
		if closeErr := bucketReader.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close destination probe: %w", closeErr))
		}
		return storage.Object{}, false, err
	}
	bucketHash, bucketSize, bucketErr := hashAndClose(ctx, bucketReader, expected.Size)
	if bucketErr != nil {
		return storage.Object{}, false, fmt.Errorf("compare destination: %w", bucketErr)
	}
	return obj, bucketSize == expected.Size && bucketHash == want, nil
}

func objectAttributesMatch(obj storage.Object, metadata map[string]string) bool {
	opts := putOptions(tusd.FileInfo{MetaData: tusd.MetaData(metadata)})
	contentType := opts.ContentType
	if contentType == "" {
		contentType = storage.DefaultContentType
	}
	return obj.ContentType == contentType && maps.Equal(obj.Metadata, opts.Metadata)
}

func hashAndClose(
	ctx context.Context, r io.ReadCloser, expected int64,
) (sum [sha256.Size]byte, size int64, err error) {
	sum, size, err = hashExact(ctx, r, expected)
	if closeErr := r.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	return sum, size, err
}

func hashExact(ctx context.Context, r io.Reader, expected int64) ([sha256.Size]byte, int64, error) {
	if expected < 0 || expected == math.MaxInt64 {
		return [sha256.Size]byte{}, 0, fmt.Errorf("invalid expected size %d", expected)
	}
	h := sha256.New()
	checked := &boundedHashReader{check: ctx.Err, r: r, remaining: expected + 1}
	limited := &io.LimitedReader{R: checked, N: expected + 1}
	var buf [32 * 1024]byte
	size, err := io.CopyBuffer(h, limited, buf[:])
	if err != nil {
		return [sha256.Size]byte{}, size, fmt.Errorf("hash bounded input: %w", err)
	}
	if size != expected {
		return [sha256.Size]byte{}, size, fmt.Errorf("input size %d, expected %d", size, expected)
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum, size, nil
}

type boundedHashReader struct {
	check     func() error
	r         io.Reader
	remaining int64
}

func (r *boundedHashReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.ErrNoProgress
	}
	r.remaining--
	if err := r.check(); err != nil {
		return 0, fmt.Errorf("hash input context: %w", err)
	}
	n, err := r.r.Read(p)
	if ctxErr := r.check(); ctxErr != nil {
		return 0, fmt.Errorf("hash input context: %w", ctxErr)
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("read hash input: %w", err)
	}
	return n, nil
}

func (s *bucketStore) acknowledgeCompletion(ctx context.Context, id string) error {
	if s.deliver != nil {
		if err := s.deliver(ctx, id); err != nil {
			return fmt.Errorf("tus: completion hook for %s: %w", id, err)
		}
		return nil
	}
	if err := s.scratch.RemoveUpload(ctx, id); err != nil {
		s.log.WarnContext(ctx, "tus: scratch cleanup failed", "id", id, "err", err)
	}
	if err := s.scratch.DeleteCompletion(ctx, id); err != nil {
		return fmt.Errorf("tus: acknowledge completion for %s: %w", id, err)
	}
	return nil
}

// resolveKey derives the final storage key for info via the store's KeyFunc,
// then applies the sanitizeKey traversal/absolute-path guard.
func (u *bucketUpload) resolveKey(info tusd.FileInfo) (string, error) {
	key, err := u.store.keyFn(info)
	if err != nil {
		return "", err
	}
	return sanitizeKey(key)
}

// putAndClose streams rc into the bucket under key, always closing rc. A
// close error is only surfaced when the Put itself succeeded, matching
// FinishUpload's prior inline ordering (Put's own error takes precedence).
func (u *bucketUpload) putAndClose(
	ctx context.Context, key string, rc io.ReadCloser, info tusd.FileInfo,
) (storage.Object, error) {
	obj, putErr := u.store.bucket.Put(ctx, key, rc, putOptions(info))
	if closeErr := rc.Close(); closeErr != nil && putErr == nil {
		putErr = closeErr
	}
	return obj, putErr
}

// cleanupScratch removes the persisted upload's local bytes best-effort.
func (u *bucketUpload) cleanupScratch(ctx context.Context) {
	if termErr := u.entry.Terminate(ctx); termErr != nil {
		u.store.log.WarnContext(ctx, "tus: scratch cleanup failed", "id", u.id, "err", termErr)
	}
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
