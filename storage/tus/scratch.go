package tus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tusd "github.com/tus/tusd/v2/pkg/handler"
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
	scratchDirPerm  = 0o750
	scratchFilePerm = 0o600
)

// localScratch stores each upload as [id] (raw bytes) + [id].info (JSON) under
// a single root directory, mirroring tusd's filestore but self-contained.
type localScratch struct {
	root string
}

func newLocalScratch(root string) (*localScratch, error) {
	if err := os.MkdirAll(root, scratchDirPerm); err != nil {
		return nil, fmt.Errorf("tus: create scratch dir: %w", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("tus: resolve scratch dir: %w", err)
	}
	return &localScratch{root: abs}, nil
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

func (s *localScratch) Create(_ context.Context, info tusd.FileInfo) (scratchEntry, error) {
	binPath, err := s.binPath(info.ID)
	if err != nil {
		return nil, err
	}
	infoPath, err := s.infoPath(info.ID)
	if err != nil {
		return nil, err
	}
	if err = writeFile(binPath, nil); err != nil {
		return nil, err
	}
	e := &localEntry{info: info, binPath: binPath, infoPath: infoPath}
	if err = e.writeInfo(); err != nil {
		return nil, err
	}
	return e, nil
}

func (s *localScratch) Get(_ context.Context, id string) (scratchEntry, error) {
	infoPath, err := s.infoPath(id)
	if err != nil {
		return nil, err
	}
	binPath, err := s.binPath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(infoPath) //nolint:gosec // G304: id sanitized by idPath at the scratch boundary // #nosec G304
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, tusd.ErrNotFound
		}
		return nil, fmt.Errorf("tus: read scratch info: %w", err)
	}
	var info tusd.FileInfo
	if err = json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("tus: decode scratch info: %w", err)
	}
	stat, err := os.Stat(binPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, tusd.ErrNotFound
		}
		return nil, fmt.Errorf("tus: stat scratch bin: %w", err)
	}
	info.Offset = stat.Size()
	return &localEntry{info: info, binPath: binPath, infoPath: infoPath}, nil
}

func (s *localScratch) Expired(_ context.Context, now time.Time, ttl time.Duration) ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("tus: read scratch dir: %w", err)
	}
	var expired []string
	cutoff := now.Add(-ttl)
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".info") {
			continue
		}
		fi, statErr := de.Info()
		if statErr != nil || fi.ModTime().After(cutoff) {
			continue
		}
		expired = append(expired, strings.TrimSuffix(name, ".info"))
	}
	return expired, nil
}

// localEntry is the scratchEntry for one upload backed by two local files.
type localEntry struct {
	info     tusd.FileInfo
	binPath  string
	infoPath string
}

func (e *localEntry) GetInfo(_ context.Context) (tusd.FileInfo, error) { return e.info, nil }

func (e *localEntry) WriteChunk(_ context.Context, _ int64, src io.Reader) (int64, error) {
	f, err := os.OpenFile(e.binPath, os.O_WRONLY|os.O_APPEND, scratchFilePerm) //nolint:gosec // G304: path built from sanitized id at scratch boundary // #nosec G304
	if err != nil {
		return 0, fmt.Errorf("tus: open scratch chunk: %w", err)
	}
	n, err := io.Copy(f, src)
	e.info.Offset += n
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return n, fmt.Errorf("tus: write scratch chunk: %w", err)
	}
	return n, nil
}

func (e *localEntry) GetReader(_ context.Context) (io.ReadCloser, error) {
	f, err := os.Open(e.binPath) //nolint:gosec // G304: path built from sanitized id at scratch boundary // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("tus: open scratch reader: %w", err)
	}
	return f, nil
}

func (e *localEntry) DeclareLength(_ context.Context, length int64) error {
	e.info.Size = length
	e.info.SizeIsDeferred = false
	return e.writeInfo()
}

func (e *localEntry) Terminate(_ context.Context) error {
	if err := os.Remove(e.binPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("tus: remove scratch bin: %w", err)
	}
	if err := os.Remove(e.infoPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("tus: remove scratch info: %w", err)
	}
	return nil
}

func (e *localEntry) writeInfo() error {
	data, err := json.Marshal(e.info)
	if err != nil {
		return fmt.Errorf("tus: encode scratch info: %w", err)
	}
	return writeFile(e.infoPath, data)
}

// writeFile (over)writes path with content, creating parent dirs as needed.
func writeFile(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, scratchFilePerm) //nolint:gosec // G304: path built from sanitized id at scratch boundary // #nosec G304
	if err != nil {
		return fmt.Errorf("tus: create scratch file: %w", err)
	}
	if len(content) > 0 {
		if _, wErr := f.Write(content); wErr != nil {
			_ = f.Close()
			return fmt.Errorf("tus: write scratch file: %w", wErr)
		}
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("tus: close scratch file: %w", err)
	}
	return nil
}
