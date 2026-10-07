// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tusd "github.com/tus/tusd/v2/pkg/handler"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/internal/dirsync"
)

// scratchEntry is the append-capable, offset-tracking view of one in-progress
// upload. WriteChunk appends; GetInfo reflects the running offset.
type scratchEntry interface {
	WriteChunk(ctx context.Context, offset int64, src io.Reader) (int64, error)
	GetInfo(ctx context.Context) (tusd.FileInfo, error)
	GetReader(ctx context.Context) (io.ReadCloser, error)
	DeclareLength(ctx context.Context, length int64) error
	Terminate(ctx context.Context) error
}

// scratchStore is the in-progress chunk area backing the tus DataStore until
// FinishUpload streams the assembled bytes into the final storage.Bucket.
type scratchStore interface {
	Create(ctx context.Context, info tusd.FileInfo) (scratchEntry, error)
	Get(ctx context.Context, id string) (scratchEntry, error)
	// Expired returns the ids whose in-progress info is older than now-ttl.
	Expired(ctx context.Context, now time.Time, ttl time.Duration) ([]string, error)
	IsExpired(ctx context.Context, id string, now time.Time, ttl time.Duration) (bool, error)
	SaveCompletion(ctx context.Context, record completionRecord) error
	Completion(ctx context.Context, id string) (completionRecord, error)
	CompletionIDs(ctx context.Context, limit int) ([]string, error)
	DeleteCompletion(ctx context.Context, id string) error
	RemoveUpload(ctx context.Context, id string) error
}

type completionStage string

const (
	completionStagePrepared  completionStage = "prepared"
	completionStagePutting   completionStage = "putting"
	completionStagePersisted completionStage = "persisted"
)

const completionRecordVersion = 1

// completionRecord is the durable hand-off around Bucket persistence and
// OnComplete delivery. CompletedCallbackIDs survives callback reordering.
type completionRecord struct {
	Version              int              `json:"version"`
	Stage                completionStage  `json:"stage"`
	Upload               completionUpload `json:"upload"`
	CompletedCallbackIDs []string         `json:"completed_callback_ids,omitempty"`
}

type completionUpload struct {
	ID       string            `json:"id"`
	Key      string            `json:"key"`
	Size     int64             `json:"size"`
	SHA256   string            `json:"sha256,omitempty"`
	MetaData map[string]string `json:"metadata"`
}

func newCompletionRecord(upload CompletedUpload) completionRecord {
	return completionRecord{
		Version: completionRecordVersion,
		Stage:   completionStagePersisted,
		Upload:  cloneCompletionUpload(upload),
	}
}

func newPreparedCompletionRecord(upload CompletedUpload, digest string) completionRecord {
	cloned := cloneCompletionUpload(upload)
	cloned.SHA256 = digest
	return completionRecord{
		Version: completionRecordVersion,
		Stage:   completionStagePrepared,
		Upload:  cloned,
	}
}

func cloneCompletionUpload(upload CompletedUpload) completionUpload {
	return completionUpload{
		ID: upload.ID, Key: upload.Key, Size: upload.Size, MetaData: maps.Clone(upload.MetaData),
	}
}

func (r completionRecord) normalizedStage() completionStage {
	return r.Stage
}

func (r completionRecord) validate() error {
	if r.Version != completionRecordVersion {
		return fmt.Errorf("tus: unsupported completion version %d", r.Version)
	}
	if !validCompletionStage(r.Stage) {
		return fmt.Errorf("tus: invalid completion stage %q", r.Stage)
	}
	if err := validateCompletionUpload(r.Upload, r.Stage != completionStagePersisted); err != nil {
		return err
	}
	return validateCompletedCallbackIDs(r.CompletedCallbackIDs)
}

func validCompletionStage(stage completionStage) bool {
	switch stage {
	case completionStagePrepared, completionStagePutting, completionStagePersisted:
		return true
	default:
		return false
	}
}

func validateCompletionUpload(upload completionUpload, requireDigest bool) error {
	if upload.ID == "" {
		return errors.New("tus: completion upload id is empty")
	}
	if requireDigest && upload.SHA256 == "" {
		return errors.New("tus: completion upload digest is empty")
	}
	if upload.SHA256 != "" {
		digest, err := hex.DecodeString(upload.SHA256)
		if err != nil || len(digest) != sha256.Size {
			return errors.New("tus: completion upload digest is invalid")
		}
	}
	return nil
}

func validateCompletedCallbackIDs(ids []string) error {
	if len(ids) > maxCompletionCallbacks {
		return fmt.Errorf("tus: too many completed callback ids: %d", len(ids))
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if err := validateCallbackID(id); err != nil {
			return fmt.Errorf("tus: invalid completed callback id: %w", err)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("tus: duplicate completed callback id %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (u completionUpload) completed() CompletedUpload {
	return CompletedUpload{
		ID: u.ID, Key: u.Key, Size: u.Size, MetaData: maps.Clone(u.MetaData),
	}
}

// newUploadID returns a 128-bit URL-safe hex id (no slashes, no NUL).
func newUploadID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("tus: generate upload id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// --- localScratch: append-capable temp-dir store (node-local). ---

const (
	scratchDirPerm           = 0o750
	scratchFilePerm          = 0o600
	completionSuffix         = ".complete"
	maxScratchInfoBytes      = 1 << 20
	maxCompletionRecordBytes = 2 << 20
	maxMaintenanceBatch      = 256
	maxOwnedCleanupEntries   = 4096
)

var (
	errScratchFileNotRegular = errors.New("scratch file is not a regular file")
	errScratchStateTooLarge  = errors.New("scratch state exceeds size limit")
	errScratchFileChanged    = errors.New("scratch file changed during open")
)

// localScratch stores each upload as [id] (raw bytes) + [id].info (JSON) under
// a single root directory, mirroring tusd's filestore but self-contained.
type localScratch struct {
	root              string
	removeRootOnClose bool
	syncParent        func(path string) error
	expiryCursor      dirCursor
	completionCursor  dirCursor
}

type scratchFileHandle struct {
	root *os.Root
	file *os.File
	info os.FileInfo
}

// dirCursor keeps a bounded, advancing directory scan between maintenance
// ticks. It prevents a large scratch directory from being materialized or
// repeatedly rescanned from its first entry.
type dirCursor struct {
	mu  sync.Mutex
	dir *os.File
}

func newLocalScratch(root string) (*localScratch, error) {
	removeRootOnClose := false
	if root == "" {
		var err error
		root, err = os.MkdirTemp("", "golusoris-tus-")
		if err != nil {
			return nil, fmt.Errorf("tus: create private scratch dir: %w", err)
		}
		removeRootOnClose = true
	} else if err := os.MkdirAll(root, scratchDirPerm); err != nil {
		return nil, fmt.Errorf("tus: create scratch dir: %w", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		if removeRootOnClose {
			if removeErr := os.RemoveAll(root); removeErr != nil {
				return nil, errors.Join(
					fmt.Errorf("tus: resolve scratch dir: %w", err),
					fmt.Errorf("tus: remove unresolved private scratch dir: %w", removeErr),
				)
			}
		}
		return nil, fmt.Errorf("tus: resolve scratch dir: %w", err)
	}
	return &localScratch{
		root: abs, removeRootOnClose: removeRootOnClose, syncParent: syncParentDir,
	}, nil
}

// idPath maps an upload id to its on-disk path, rejecting traversal and NUL.
func (s *localScratch) idPath(id, suffix string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\\x00") || strings.Contains(id, "..") {
		return "", fmt.Errorf("tus: invalid upload id %q", id)
	}
	return filepath.Join(s.root, id+suffix), nil
}

func (s *localScratch) binPath(id string) (string, error)  { return s.idPath(id, "") }
func (s *localScratch) infoPath(id string) (string, error) { return s.idPath(id, ".info") }

func (s *localScratch) completionPath(id string) (string, error) {
	return s.idPath(id, completionSuffix)
}

func (s *localScratch) Create(ctx context.Context, info tusd.FileInfo) (scratchEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binPath, err := s.binPath(info.ID)
	if err != nil {
		return nil, err
	}
	infoPath, err := s.infoPath(info.ID)
	if err != nil {
		return nil, err
	}
	if err = writeNewFile(binPath, nil); err != nil {
		return nil, err
	}
	info.MetaData = maps.Clone(info.MetaData)
	e := &localEntry{store: s, info: info, id: info.ID, infoPath: infoPath}
	if err = ctx.Err(); err == nil {
		err = e.writeNewInfo()
	}
	if err != nil {
		if removeErr := os.Remove(binPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("tus: remove failed scratch bin: %w", removeErr))
		}
		return nil, err
	}
	return e, nil
}

func (s *localScratch) Get(ctx context.Context, id string) (scratchEntry, error) {
	if err := checkScratchContext(ctx, "get preflight"); err != nil {
		return nil, err
	}
	infoPath, err := s.infoPath(id)
	if err != nil {
		return nil, err
	}
	info, err := readScratchInfo(ctx, s.root, filepath.Base(infoPath))
	if err != nil {
		return nil, err
	}
	if info.ID != id {
		return nil, fmt.Errorf("tus: scratch info id mismatch for %s", id)
	}
	stat, err := s.statBin(ctx, id)
	if err != nil {
		return nil, err
	}
	info.Offset = stat.Size()
	return &localEntry{store: s, info: info, id: id, infoPath: infoPath}, nil
}

func readScratchInfo(ctx context.Context, root, name string) (tusd.FileInfo, error) {
	data, err := readScratchStateFile(ctx, root, name, "scratch info", maxScratchInfoBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return tusd.FileInfo{}, tusd.ErrNotFound
		}
		return tusd.FileInfo{}, fmt.Errorf("tus: read scratch info: %w", err)
	}
	var info tusd.FileInfo
	if err = json.Unmarshal(data, &info); err != nil {
		return tusd.FileInfo{}, fmt.Errorf("tus: decode scratch info: %w", err)
	}
	return info, nil
}

func readScratchStateFile(
	ctx context.Context,
	rootPath string,
	name string,
	kind string,
	limit int64,
) (data []byte, err error) {
	handle, err := openScratchStateFile(ctx, rootPath, name, kind, limit)
	if err != nil {
		return nil, err
	}
	defer gerr.CloseInto(handle, &err, "tus: close scratch state")
	data, err = io.ReadAll(scratchCheckedReader{
		check: ctx.Err,
		src:   io.LimitReader(handle.file, limit+1),
	})
	if err != nil {
		return nil, fmt.Errorf("tus: read %s: %w", kind, err)
	}
	if err = validateScratchStateSize(kind, int64(len(data)), limit); err != nil {
		return nil, err
	}
	if err = checkScratchContext(ctx, kind+" after read"); err != nil {
		return nil, err
	}
	return data, nil
}

func openScratchStateFile(
	ctx context.Context,
	rootPath string,
	name string,
	kind string,
	limit int64,
) (*scratchFileHandle, error) {
	return openScratchRegularFile(ctx, rootPath, name, kind, os.O_RDONLY, limit)
}

func openScratchRegularFile(
	ctx context.Context,
	rootPath string,
	name string,
	kind string,
	flag int,
	limit int64,
) (_ *scratchFileHandle, err error) {
	if err = checkScratchContext(ctx, kind+" open preflight"); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("tus: open scratch root: %w", err)
	}
	handle := &scratchFileHandle{root: root}
	defer func() {
		if err != nil {
			err = errors.Join(err, handle.Close())
		}
	}()
	before, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("tus: inspect %s: %w", kind, err)
	}
	if err = validateScratchFilePreOpen(kind, before, limit); err != nil {
		return nil, err
	}
	if err = checkScratchContext(ctx, kind+" before open"); err != nil {
		return nil, err
	}
	handle.file, err = root.OpenFile(name, flag, scratchFilePerm)
	if err != nil {
		return nil, fmt.Errorf("tus: open %s: %w", kind, err)
	}
	opened, err := handle.file.Stat()
	if err != nil {
		return nil, fmt.Errorf("tus: stat opened %s: %w", kind, err)
	}
	if err = validateScratchFileOpened(kind, before, opened); err != nil {
		return nil, err
	}
	handle.info = opened
	return handle, nil
}

func validateScratchFilePreOpen(kind string, info os.FileInfo, limit int64) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("tus: %s: %w", kind, errScratchFileNotRegular)
	}
	if limit < 0 {
		return nil
	}
	return validateScratchStateSize(kind, info.Size(), limit)
}

func validateScratchFileOpened(kind string, before, opened os.FileInfo) error {
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return fmt.Errorf("tus: %s: %w", kind, errScratchFileChanged)
	}
	return nil
}

func (h *scratchFileHandle) Read(p []byte) (int, error) {
	n, err := h.file.Read(p)
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("tus: read scratch data: %w", err)
	}
	return n, nil
}

func (h *scratchFileHandle) Seek(offset int64, whence int) (int64, error) {
	position, err := h.file.Seek(offset, whence)
	if err != nil {
		return position, fmt.Errorf("tus: seek scratch data: %w", err)
	}
	return position, nil
}

func (h *scratchFileHandle) Close() error {
	var err error
	if h.file != nil {
		err = h.file.Close()
	}
	if h.root != nil {
		err = errors.Join(err, h.root.Close())
	}
	return err
}

func validateScratchStateSize(kind string, size, limit int64) error {
	if size > limit {
		return fmt.Errorf("tus: %s size limit %d: %w", kind, limit, errScratchStateTooLarge)
	}
	return nil
}

func (s *localScratch) openBin(
	ctx context.Context, id string, flag int,
) (*scratchFileHandle, error) {
	binPath, err := s.binPath(id)
	if err != nil {
		return nil, err
	}
	return openScratchRegularFile(
		ctx, s.root, filepath.Base(binPath), "scratch data", flag, -1,
	)
}

func (s *localScratch) statBin(ctx context.Context, id string) (_ os.FileInfo, err error) {
	handle, err := s.openBin(ctx, id, os.O_RDONLY)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, tusd.ErrNotFound
		}
		return nil, fmt.Errorf("tus: stat scratch data: %w", err)
	}
	defer gerr.CloseInto(handle, &err, "tus: close scratch data stat")
	return handle.info, nil
}

func checkScratchContext(ctx context.Context, phase string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("tus: scratch %s: %w", phase, err)
	}
	return nil
}

func (s *localScratch) Expired(ctx context.Context, now time.Time, ttl time.Duration) ([]string, error) {
	if ttl <= 0 {
		return nil, errors.New("tus: upload expiry must be positive")
	}
	entries, err := s.expiryCursor.read(ctx, s.root, maxMaintenanceBatch)
	if err != nil {
		return nil, fmt.Errorf("tus: read scratch dir: %w", err)
	}
	var expired []string
	for _, de := range entries {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		id, ok := s.expiryCandidate(de)
		if !ok {
			continue
		}
		expiredID, expiryErr := s.IsExpired(ctx, id, now, ttl)
		if expiryErr != nil {
			if errors.Is(expiryErr, tusd.ErrNotFound) {
				continue
			}
			return nil, expiryErr
		}
		if expiredID {
			expired = append(expired, id)
		}
	}
	return expired, nil
}

func (s *localScratch) expiryCandidate(de os.DirEntry) (string, bool) {
	name := de.Name()
	if de.IsDir() || !strings.HasSuffix(name, ".info") {
		return "", false
	}
	return strings.TrimSuffix(name, ".info"), true
}

func (s *localScratch) IsExpired(
	ctx context.Context, id string, now time.Time, ttl time.Duration,
) (bool, error) {
	if ttl <= 0 {
		return false, errors.New("tus: upload expiry must be positive")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	pending, err := s.hasCompletion(id)
	if err != nil || pending {
		return false, err
	}
	lastActivity, err := s.lastActivity(ctx, id)
	if err != nil {
		return false, err
	}
	return !lastActivity.After(now.Add(-ttl)), nil
}

func (s *localScratch) hasCompletion(id string) (bool, error) {
	completionPath, err := s.completionPath(id)
	if err != nil {
		return false, err
	}
	if _, err = os.Stat(completionPath); err == nil {
		return true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("tus: stat completion: %w", err)
	}
	return false, nil
}

func (s *localScratch) lastActivity(ctx context.Context, id string) (time.Time, error) {
	infoPath, err := s.infoPath(id)
	if err != nil {
		return time.Time{}, err
	}
	infoActivity, err := scratchModTime(infoPath, "info")
	if err != nil {
		return time.Time{}, err
	}
	binStat, err := s.statBin(ctx, id)
	if err != nil {
		return time.Time{}, err
	}
	binActivity := binStat.ModTime()
	if binActivity.After(infoActivity) {
		return binActivity, nil
	}
	return infoActivity, nil
}

func scratchModTime(path, kind string) (time.Time, error) {
	stat, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, tusd.ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("tus: stat scratch %s: %w", kind, err)
	}
	return stat.ModTime(), nil
}

func (s *localScratch) SaveCompletion(ctx context.Context, record completionRecord) error {
	if err := checkScratchContext(ctx, "save completion preflight"); err != nil {
		return err
	}
	if err := record.validate(); err != nil {
		return err
	}
	completionPath, err := s.completionPath(record.Upload.ID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("tus: encode completion: %w", err)
	}
	if err = validateScratchStateSize(
		"completion record", int64(len(data)), maxCompletionRecordBytes,
	); err != nil {
		return err
	}
	if err = checkScratchContext(ctx, "save completion before publish"); err != nil {
		return err
	}
	if err = writeStateFile(ctx, completionPath, data); err != nil {
		return fmt.Errorf("tus: persist completion: %w", err)
	}
	return nil
}

func (s *localScratch) Completion(ctx context.Context, id string) (completionRecord, error) {
	if err := checkScratchContext(ctx, "read completion preflight"); err != nil {
		return completionRecord{}, err
	}
	completionPath, err := s.completionPath(id)
	if err != nil {
		return completionRecord{}, err
	}
	data, err := readScratchStateFile(
		ctx,
		s.root,
		filepath.Base(completionPath),
		"completion record",
		maxCompletionRecordBytes,
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return completionRecord{}, tusd.ErrNotFound
		}
		return completionRecord{}, fmt.Errorf("tus: read completion: %w", err)
	}
	var record completionRecord
	if err = json.Unmarshal(data, &record); err != nil {
		return completionRecord{}, fmt.Errorf("tus: decode completion: %w", err)
	}
	if record.Upload.ID != id {
		return completionRecord{}, fmt.Errorf("tus: completion id mismatch for %s", id)
	}
	if err = record.validate(); err != nil {
		return completionRecord{}, fmt.Errorf("tus: validate completion %s: %w", id, err)
	}
	return record, nil
}

func (s *localScratch) CompletionIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > maxMaintenanceBatch {
		return nil, fmt.Errorf("tus: invalid completion batch limit %d", limit)
	}
	entries, err := s.completionCursor.read(ctx, s.root, limit)
	if err != nil {
		return nil, fmt.Errorf("tus: read completion dir: %w", err)
	}
	ids := make([]string, 0, min(limit, len(entries)))
	for _, de := range entries {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if len(ids) >= limit {
			break
		}
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, completionSuffix) {
			continue
		}
		id := strings.TrimSuffix(name, completionSuffix)
		if _, pathErr := s.completionPath(id); pathErr == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// Close releases directory cursors and removes an automatically-owned root.
func (s *localScratch) Close() error {
	return s.CloseContext(context.Background())
}

// CloseContext releases cursors and removes a bounded, flat private root.
func (s *localScratch) CloseContext(ctx context.Context) error {
	err := errors.Join(s.expiryCursor.close(), s.completionCursor.close())
	if !s.removeRootOnClose {
		return err
	}
	if removeErr := removeOwnedScratchRoot(ctx, s.root); removeErr != nil {
		err = errors.Join(err, fmt.Errorf("tus: remove private scratch dir: %w", removeErr))
	}
	return err
}

func removeOwnedScratchRoot(ctx context.Context, rootPath string) (err error) {
	if err = checkScratchContext(ctx, "close private root preflight"); err != nil {
		return err
	}
	entries, exists, err := readOwnedScratchEntries(rootPath)
	if err != nil || !exists {
		return err
	}
	if err = validateOwnedScratchEntries(ctx, entries); err != nil {
		return err
	}
	if err = removeOwnedScratchEntries(ctx, rootPath, entries); err != nil {
		return err
	}
	if err = checkScratchContext(ctx, "remove private root"); err != nil {
		return err
	}
	if err = os.Remove(rootPath); err != nil {
		return fmt.Errorf("remove private scratch root: %w", err)
	}
	return nil
}

func readOwnedScratchEntries(rootPath string) ([]os.DirEntry, bool, error) {
	dir, err := os.Open(rootPath) // #nosec G304 -- root is a private directory created by newLocalScratch.
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open private scratch root: %w", err)
	}
	entries, readErr := dir.ReadDir(maxOwnedCleanupEntries + 1)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, false, errors.Join(
			fmt.Errorf("read private scratch root: %w", readErr),
			closeErr,
		)
	}
	if closeErr != nil {
		return nil, false, fmt.Errorf("close private scratch root: %w", closeErr)
	}
	if len(entries) > maxOwnedCleanupEntries {
		return nil, false, fmt.Errorf("private scratch root exceeds cleanup limit %d", maxOwnedCleanupEntries)
	}
	return entries, true, nil
}

func validateOwnedScratchEntries(ctx context.Context, entries []os.DirEntry) error {
	for _, entry := range entries {
		if err := checkScratchContext(ctx, "close private root entry"); err != nil {
			return err
		}
		if entry.IsDir() {
			return fmt.Errorf("private scratch root contains unexpected directory %q", entry.Name())
		}
	}
	return nil
}

func removeOwnedScratchEntries(ctx context.Context, rootPath string, entries []os.DirEntry) error {
	for _, entry := range entries {
		if err := checkScratchContext(ctx, "remove private root entry"); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(rootPath, entry.Name())); err != nil {
			return fmt.Errorf("remove private scratch entry %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func (c *dirCursor) read(ctx context.Context, root string, limit int) ([]os.DirEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.dir == nil {
		dir, err := os.Open(root) // #nosec G304 -- configured scratch root
		if err != nil {
			return nil, fmt.Errorf("open directory cursor: %w", err)
		}
		c.dir = dir
	}
	entries, err := c.dir.ReadDir(limit)
	if errors.Is(err, io.EOF) {
		err = c.closeLocked()
	} else if err != nil {
		err = errors.Join(err, c.closeLocked())
	}
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *dirCursor) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked()
}

func (c *dirCursor) closeLocked() error {
	if c.dir == nil {
		return nil
	}
	err := c.dir.Close()
	c.dir = nil
	if err != nil {
		return fmt.Errorf("close directory cursor: %w", err)
	}
	return nil
}

func (s *localScratch) DeleteCompletion(ctx context.Context, id string) error {
	if err := checkScratchContext(ctx, "delete completion preflight"); err != nil {
		return err
	}
	completionPath, err := s.completionPath(id)
	if err != nil {
		return err
	}
	if err = checkScratchContext(ctx, "delete completion before remove"); err != nil {
		return err
	}
	if err = os.Remove(completionPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("tus: remove completion: %w", err)
	}
	if err == nil {
		if err = s.syncParent(completionPath); err != nil {
			return fmt.Errorf("tus: sync deleted completion: %w", err)
		}
	}
	return nil
}

func (s *localScratch) RemoveUpload(ctx context.Context, id string) error {
	if err := checkScratchContext(ctx, "remove upload preflight"); err != nil {
		return err
	}
	binPath, err := s.binPath(id)
	if err != nil {
		return err
	}
	infoPath, err := s.infoPath(id)
	if err != nil {
		return err
	}
	if err = s.inspectBinForRemove(ctx, id); err != nil {
		return err
	}
	return removeScratchUploadFiles(ctx, binPath, infoPath)
}

func (s *localScratch) inspectBinForRemove(ctx context.Context, id string) error {
	handle, err := s.openBin(ctx, id, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("tus: inspect scratch upload before remove: %w", err)
	}
	if err = handle.Close(); err != nil {
		return fmt.Errorf("tus: close scratch upload before remove: %w", err)
	}
	return nil
}

func removeScratchUploadFiles(ctx context.Context, paths ...string) error {
	var removeErrs []error
	for _, candidate := range paths {
		if err := checkScratchContext(ctx, "remove upload before file remove"); err != nil {
			return errors.Join(append([]error{err}, removeErrs...)...)
		}
		if removeErr := os.Remove(candidate); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			removeErrs = append(removeErrs, removeErr)
		}
	}
	if err := errors.Join(removeErrs...); err != nil {
		return fmt.Errorf("tus: remove scratch upload: %w", err)
	}
	return nil
}

// localEntry is the scratchEntry for one upload backed by two local files.
type localEntry struct {
	mu       sync.Mutex
	store    *localScratch
	info     tusd.FileInfo
	id       string
	infoPath string
}

func (e *localEntry) GetInfo(ctx context.Context) (tusd.FileInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tusd.FileInfo{}, err
	}
	info := e.info
	info.MetaData = maps.Clone(e.info.MetaData)
	return info, nil
}

func (e *localEntry) WriteChunk(ctx context.Context, offset int64, src io.Reader) (n int64, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 || offset != e.info.Offset {
		return 0, fmt.Errorf("tus: scratch offset %d does not match current offset %d", offset, e.info.Offset)
	}
	handle, err := e.store.openBin(ctx, e.id, os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return 0, fmt.Errorf("tus: open scratch chunk: %w", err)
	}
	if handle.info.Size() != offset {
		return 0, closeScratchChunk(handle, fmt.Errorf(
			"tus: scratch file size %d does not match offset %d", handle.info.Size(), offset,
		))
	}
	n, err = io.Copy(handle.file, scratchCheckedReader{check: ctx.Err, src: src})
	e.info.Offset += n
	err = closeScratchChunk(handle, err)
	if err != nil {
		return n, fmt.Errorf("tus: write scratch chunk: %w", err)
	}
	return n, nil
}

func closeScratchChunk(closer io.Closer, cause error) error {
	if closeErr := closer.Close(); closeErr != nil {
		cause = errors.Join(cause, closeErr)
	}
	return cause
}

type scratchCheckedReader struct {
	check func() error
	src   io.Reader
}

func (r scratchCheckedReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, fmt.Errorf("tus: read chunk context: %w", err)
	}
	n, err := r.src.Read(p)
	if ctxErr := r.check(); ctxErr != nil {
		return n, fmt.Errorf("tus: read chunk context: %w", ctxErr)
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("tus: read chunk source: %w", err)
	}
	return n, nil
}

func (e *localEntry) GetReader(ctx context.Context) (io.ReadCloser, error) {
	handle, err := e.store.openBin(ctx, e.id, os.O_RDONLY)
	if err != nil {
		return nil, fmt.Errorf("tus: open scratch reader: %w", err)
	}
	return handle, nil
}

func (e *localEntry) DeclareLength(ctx context.Context, length int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	previous := e.info
	e.info.Size = length
	e.info.SizeIsDeferred = false
	if err := e.writeInfo(ctx); err != nil {
		e.info = previous
		return err
	}
	return nil
}

func (e *localEntry) Terminate(ctx context.Context) error {
	return e.store.RemoveUpload(ctx, e.id)
}

func (e *localEntry) writeInfo(ctx context.Context) (err error) {
	data, err := e.encodedInfo()
	if err != nil {
		return err
	}
	handle, err := openScratchStateFile(
		ctx, e.store.root, filepath.Base(e.infoPath), "scratch info", maxScratchInfoBytes,
	)
	if err != nil {
		return err
	}
	if err = handle.Close(); err != nil {
		return fmt.Errorf("tus: close scratch info before replace: %w", err)
	}
	return writeStateFile(ctx, e.infoPath, data)
}

func (e *localEntry) writeNewInfo() error {
	data, err := e.encodedInfo()
	if err != nil {
		return err
	}
	return writeNewFile(e.infoPath, data)
}

func (e *localEntry) encodedInfo() ([]byte, error) {
	data, err := json.Marshal(e.info)
	if err != nil {
		return nil, fmt.Errorf("tus: encode scratch info: %w", err)
	}
	if err = validateScratchStateSize("scratch info", int64(len(data)), maxScratchInfoBytes); err != nil {
		return nil, err
	}
	return data, nil
}

func writeNewFile(path string, content []byte) error {
	return writeFileWithFlags(path, content, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
}

func writeFileWithFlags(path string, content []byte, flags int) (err error) {
	f, err := os.OpenFile(path, flags, scratchFilePerm) // #nosec G304 -- path built from sanitized id at scratch boundary
	if err != nil {
		return fmt.Errorf("tus: create scratch file: %w", err)
	}
	defer gerr.CloseInto(f, &err, "tus: close scratch file")
	if len(content) > 0 {
		if _, wErr := f.Write(content); wErr != nil {
			return fmt.Errorf("tus: write scratch file: %w", wErr)
		}
	}
	return nil
}

// writeStateFile atomically replaces durable scratch state in one rename.
func writeStateFile(ctx context.Context, path string, content []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".tus-completion-*")
	if err != nil {
		return fmt.Errorf("tus: create completion temp: %w", err)
	}
	tempPath := f.Name()
	defer func() {
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("tus: remove completion temp: %w", removeErr))
		}
	}()
	writeErr := func() (closeErr error) {
		defer gerr.CloseInto(f, &closeErr, "tus: close completion temp")
		if chmodErr := f.Chmod(scratchFilePerm); chmodErr != nil {
			return fmt.Errorf("tus: chmod completion temp: %w", chmodErr)
		}
		if _, err = f.Write(content); err != nil {
			return fmt.Errorf("tus: write completion temp: %w", err)
		}
		if err = f.Sync(); err != nil {
			return fmt.Errorf("tus: sync completion temp: %w", err)
		}
		return nil
	}()
	if writeErr != nil {
		return writeErr
	}
	if err = checkScratchContext(ctx, "save completion before rename"); err != nil {
		return err
	}
	if err = os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("tus: replace completion state: %w", err)
	}
	return syncParentDir(path)
}

func syncParentDir(path string) (err error) {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("tus: open completion directory: %w", err)
	}
	defer gerr.CloseInto(dir, &err, "tus: close completion directory")
	if err = dirsync.Sync(dir); err != nil {
		return fmt.Errorf("tus: sync completion directory: %w", err)
	}
	return nil
}
