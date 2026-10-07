// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package trainerio centralizes bounded, confinement-safe trainer staging and
// output handling shared by the Gemma and LiteRT trainers.
package trainerio

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/golusoris/golusoris/storage"
)

const (
	// DefaultMaxLogBytes caps captured stdout plus stderr per training run.
	DefaultMaxLogBytes int64 = 1 << 20
	// DefaultMaxArtifactBytes caps one model artifact at 1 GiB.
	DefaultMaxArtifactBytes int64 = 1 << 30
	// DefaultMaxMetricsBytes caps one optional metrics sidecar.
	DefaultMaxMetricsBytes int64 = 1 << 20
	// DefaultMaxConfigBytes caps one staged trainer configuration.
	DefaultMaxConfigBytes int64 = 1 << 20
	// DefaultMaxDatasetBytes caps one locally staged dataset.
	DefaultMaxDatasetBytes int64 = 10 << 30
	maxLoggedLines               = 1024
)

// ErrOutputTooLarge identifies container output rejected at its byte cap.
var ErrOutputTooLarge = errors.New("ai/tiny: trainer output exceeds byte cap")

// ValidateStorageSegment rejects values that could alter an artifact-key
// hierarchy. Values are deliberately narrower than general display text.
func ValidateStorageSegment(name, value string) error {
	if value == "" {
		return fmt.Errorf("ai/tiny: %s required", name)
	}
	if len(value) > 128 {
		return fmt.Errorf("ai/tiny: %s exceeds 128 bytes", name)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("ai/tiny: %s must be one safe storage segment", name)
	}
	for index := range len(value) {
		if isStorageSegmentByte(value[index]) {
			continue
		}
		return fmt.Errorf("ai/tiny: %s must contain only ASCII letters, digits, dot, dash, or underscore", name)
	}
	return nil
}

func isStorageSegmentByte(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.'
}

// ValidateKeyPrefix accepts a relative sequence of safe storage segments.
func ValidateKeyPrefix(prefix string) error {
	if prefix == "" || strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") ||
		strings.Contains(prefix, "\\") {
		return errors.New("ai/tiny: KeyPrefix must be a non-empty relative storage path")
	}
	segments := strings.Split(prefix, "/")
	if len(segments) > 32 {
		return errors.New("ai/tiny: KeyPrefix exceeds 32 segments")
	}
	for index, segment := range segments {
		if err := ValidateStorageSegment(fmt.Sprintf("KeyPrefix segment %d", index), segment); err != nil {
			return err
		}
	}
	return nil
}

// ArtifactKey constructs a tenant-isolated, content-addressed key without
// applying path normalization to caller-controlled values.
func ArtifactKey(prefix, tenantID, name, jobID, digest, artifact string) (string, error) {
	if err := ValidateKeyPrefix(prefix); err != nil {
		return "", err
	}
	if tenantID == "" {
		tenantID = "_default"
	}
	fields := [...]struct{ name, value string }{
		{"TenantID", tenantID},
		{"Name", name},
		{"ID", jobID},
		{"digest", digest},
		{"artifact", artifact},
	}
	for _, field := range fields {
		if err := ValidateStorageSegment(field.name, field.value); err != nil {
			return "", err
		}
	}
	if err := validateDigest(digest); err != nil {
		return "", err
	}
	return strings.Join(
		[]string{prefix, "tenants", tenantID, name, jobID, digest, artifact}, "/",
	), nil
}

func validateDigest(digest string) error {
	if len(digest) != sha256.Size*2 {
		return errors.New("ai/tiny: artifact digest must be a lowercase SHA-256 hex value")
	}
	for index := range len(digest) {
		char := digest[index]
		if char < '0' || char > '9' && char < 'a' || char > 'f' {
			return errors.New("ai/tiny: artifact digest must be a lowercase SHA-256 hex value")
		}
	}
	return nil
}

// Limits holds the three independently configurable trainer output caps.
type Limits struct {
	Log      int64
	Artifact int64
	Metrics  int64
}

// NormalizeLimits selects safe defaults and rejects negative caps.
func NormalizeLimits(limits Limits, scope string) (Limits, error) {
	var err error
	limits.Log, err = NormalizeLimit(limits.Log, DefaultMaxLogBytes, scope+" MaxLogBytes")
	if err != nil {
		return Limits{}, err
	}
	limits.Artifact, err = NormalizeLimit(
		limits.Artifact, DefaultMaxArtifactBytes, scope+" MaxArtifactBytes",
	)
	if err != nil {
		return Limits{}, err
	}
	limits.Metrics, err = NormalizeLimit(
		limits.Metrics, DefaultMaxMetricsBytes, scope+" MaxMetricsBytes",
	)
	if err != nil {
		return Limits{}, err
	}
	return limits, nil
}

// NormalizeLimit selects defaultLimit for zero and rejects negative values.
func NormalizeLimit(value, defaultLimit int64, name string) (int64, error) {
	if value < 0 {
		return 0, fmt.Errorf("ai/tiny: %s must not be negative", name)
	}
	if value == 0 {
		return defaultLimit, nil
	}
	return value, nil
}

// CanonicalDatasetRoot validates one absolute, non-symlinked dataset root.
func CanonicalDatasetRoot(root string) (string, error) {
	if root == "" {
		return "", errors.New("ai/tiny: DatasetRoot required")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", errors.New("ai/tiny: DatasetRoot must be an absolute canonical path")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("ai/tiny: resolve DatasetRoot: %w", err)
	}
	if resolved != root {
		return "", errors.New("ai/tiny: DatasetRoot must not contain symbolic links")
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("ai/tiny: stat DatasetRoot: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("ai/tiny: DatasetRoot must be a directory")
	}
	return root, nil
}

// CappedLog retains at most max bytes while reporting successful writes to
// prevent a noisy child process from blocking on discarded output.
type CappedLog struct {
	mu        sync.Mutex
	data      []byte
	max       int64
	truncated bool
}

// NewCappedLog returns a concurrency-safe bounded log writer.
func NewCappedLog(maxBytes int64) *CappedLog {
	return &CappedLog{max: maxBytes}
}

// Write implements io.Writer.
func (b *CappedLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(p)
	remaining := max(b.max-int64(len(b.data)), 0)
	keep := min(int64(len(p)), remaining)
	b.data = append(b.data, p[:int(keep)]...)
	if keep < int64(len(p)) {
		b.truncated = true
	}
	return written, nil
}

// Bytes returns an isolated snapshot of retained log bytes.
func (b *CappedLog) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.data)
}

// Truncated reports whether any log bytes were discarded.
func (b *CappedLog) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// DrainLog writes bounded captured output into structured logs.
func DrainLog(ctx context.Context, logger *slog.Logger, message string, captured *CappedLog) {
	data := captured.Bytes()
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), max(len(data)+1, 4096))
	lines := 0
	omitted := false
	for scanner.Scan() {
		if lines >= maxLoggedLines {
			omitted = true
			break
		}
		logger.InfoContext(ctx, message, slog.String("line", scanner.Text()))
		lines++
	}
	if err := scanner.Err(); err != nil {
		logger.WarnContext(ctx, message+" scan failed", slog.String("error", err.Error()))
	}
	if captured.Truncated() || omitted {
		logger.WarnContext(ctx, message+" truncated", slog.Int("retained_bytes", len(data)))
	}
}

// Stage creates an isolated input/output tree and atomically cleans it on any
// setup failure.
func Stage(prefix string, config any) (workDir, inputDir, outputDir string, err error) {
	workDir, err = os.MkdirTemp("", prefix)
	if err != nil {
		return "", "", "", fmt.Errorf("ai/tiny: mktemp: %w", err)
	}
	cleanupDir := workDir
	defer func() {
		if err == nil {
			return
		}
		if removeErr := os.RemoveAll(cleanupDir); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("ai/tiny: clean failed stage: %w", removeErr))
		}
	}()
	inputDir = filepath.Join(workDir, "input")
	outputDir = filepath.Join(workDir, "output")
	if err = os.Mkdir(inputDir, 0o750); err != nil {
		return "", "", "", fmt.Errorf("ai/tiny: mkdir input: %w", err)
	}
	if err = os.Mkdir(outputDir, 0o750); err != nil {
		return "", "", "", fmt.Errorf("ai/tiny: mkdir output: %w", err)
	}
	configBytes, marshalErr := marshalConfig(config)
	if marshalErr != nil {
		return "", "", "", fmt.Errorf("ai/tiny: marshal config: %w", marshalErr)
	}
	if err = os.WriteFile(filepath.Join(inputDir, "config.json"), configBytes, 0o600); err != nil {
		return "", "", "", fmt.Errorf("ai/tiny: write config: %w", err)
	}
	return workDir, inputDir, outputDir, nil
}

// StageDataset copies one local dataset into the read-only input mount so host
// paths and network credentials never enter the container contract.
func StageDataset(
	ctx context.Context,
	prefix string,
	config map[string]any,
	datasetRoot string,
	tenantID string,
	datasetURI string,
	maxDatasetBytes int64,
) (workDir, inputDir, outputDir string, err error) {
	maxDatasetBytes, err = NormalizeLimit(
		maxDatasetBytes, DefaultMaxDatasetBytes, "MaxDatasetBytes",
	)
	if err != nil {
		return "", "", "", err
	}
	stagedConfig := maps.Clone(config)
	stagedConfig["max_dataset_bytes"] = maxDatasetBytes
	workDir, inputDir, outputDir, err = Stage(prefix, stagedConfig)
	if err != nil {
		return "", "", "", err
	}
	defer cleanupFailedStage(workDir, &err)

	resolvedURI, resolveErr := stageDatasetURI(
		ctx, inputDir, datasetRoot, tenantID, datasetURI, maxDatasetBytes,
	)
	if resolveErr != nil {
		return "", "", "", resolveErr
	}
	stagedConfig["dataset_uri"] = resolvedURI
	if err = finalizeDatasetStage(stagedConfig, inputDir, outputDir); err != nil {
		return "", "", "", err
	}
	return workDir, inputDir, outputDir, nil
}

func cleanupFailedStage(workDir string, stageErr *error) {
	if *stageErr == nil {
		return
	}
	if removeErr := Cleanup(workDir); removeErr != nil {
		*stageErr = errors.Join(*stageErr, fmt.Errorf("ai/tiny: clean failed dataset stage: %w", removeErr))
	}
}

func finalizeDatasetStage(config map[string]any, inputDir, outputDir string) error {
	configBytes, err := marshalConfig(config)
	if err != nil {
		return fmt.Errorf("ai/tiny: marshal staged config: %w", err)
	}
	configPath := filepath.Join(inputDir, "config.json")
	if err = os.WriteFile(configPath, configBytes, 0o600); err != nil {
		return fmt.Errorf("ai/tiny: rewrite staged config: %w", err)
	}
	if err = os.Chmod(configPath, 0o444); err != nil { // #nosec G302 -- fixed nonroot container UID needs read access inside a private 0700 work root.
		return fmt.Errorf("ai/tiny: protect staged config: %w", err)
	}
	if err = os.Chmod(inputDir, 0o555); err != nil { // #nosec G302 -- fixed nonroot container UID needs traversal inside a private 0700 work root.
		return fmt.Errorf("ai/tiny: protect input directory: %w", err)
	}
	if err = os.Chmod(outputDir, 0o777); err != nil { // #nosec G302 -- bind-mounted output must admit the fixed nonroot container UID; its parent remains private.
		return fmt.Errorf("ai/tiny: permit non-root output: %w", err)
	}
	return nil
}

func marshalConfig(config any) ([]byte, error) {
	configBytes, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode config JSON: %w", err)
	}
	if int64(len(configBytes)) > DefaultMaxConfigBytes {
		return nil, fmt.Errorf(
			"config exceeds %d-byte limit: %d bytes",
			DefaultMaxConfigBytes, len(configBytes),
		)
	}
	return configBytes, nil
}

// Cleanup restores owner access to container mount directories before
// removing the private work tree.
func Cleanup(workDir string) error {
	var errs []error
	for _, name := range []string{"input", "output"} {
		path := filepath.Join(workDir, name)
		if err := os.Chmod(path, 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) { // #nosec G302 -- owner traversal is required to remove private directories after the container exits.
			errs = append(errs, fmt.Errorf("chmod %s: %w", name, err))
		}
	}
	if err := os.RemoveAll(workDir); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func stageDatasetURI(
	ctx context.Context,
	inputDir, datasetRoot, tenantID, rawURI string,
	maxDatasetBytes int64,
) (string, error) {
	canonicalRoot, err := CanonicalDatasetRoot(datasetRoot)
	if err != nil {
		return "", err
	}
	datasetPath, err := localDatasetPath(rawURI)
	if err != nil {
		return "", err
	}
	tenantSegment, relative, err := tenantDatasetPath(canonicalRoot, tenantID, datasetPath)
	if err != nil {
		return "", err
	}
	tenantRoot, err := openTenantDatasetRoot(canonicalRoot, tenantSegment)
	if err != nil {
		return "", err
	}
	stagedURI, copyErr := copyDataset(ctx, tenantRoot, relative, inputDir, maxDatasetBytes)
	if closeErr := tenantRoot.Close(); closeErr != nil {
		copyErr = errors.Join(copyErr, fmt.Errorf("ai/tiny: close tenant DatasetRoot: %w", closeErr))
	}
	return stagedURI, copyErr
}

func localDatasetPath(rawURI string) (string, error) {
	parsed, err := url.ParseRequestURI(rawURI)
	if err != nil {
		return "", fmt.Errorf("ai/tiny: parse dataset URI: %w", err)
	}
	if parsed.Fragment != "" || parsed.RawQuery != "" || parsed.User != nil {
		return "", errors.New("ai/tiny: dataset URI must not contain userinfo, query, or fragment")
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf(
			"ai/tiny: dataset URI scheme %q not supported; stage remote data as a local file",
			parsed.Scheme,
		)
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return "", errors.New("ai/tiny: file dataset URI host must be empty or localhost")
	}
	if !filepath.IsAbs(parsed.Path) {
		return "", errors.New("ai/tiny: file dataset URI path must be absolute")
	}
	return filepath.Clean(parsed.Path), nil
}

func tenantDatasetPath(canonicalRoot, tenantID, datasetPath string) (string, string, error) {
	tenantSegment := tenantID
	if tenantSegment == "" {
		tenantSegment = "_default"
	}
	if err := ValidateStorageSegment("TenantID", tenantSegment); err != nil {
		return "", "", err
	}
	tenantRootPath := filepath.Join(canonicalRoot, tenantSegment)
	relative, err := filepath.Rel(tenantRootPath, datasetPath)
	if err != nil {
		return "", "", fmt.Errorf("ai/tiny: locate dataset under tenant root: %w", err)
	}
	if relative == "." || filepath.IsAbs(relative) || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("ai/tiny: dataset must remain under its tenant DatasetRoot")
	}
	return tenantSegment, relative, nil
}

func openTenantDatasetRoot(canonicalRoot, tenantSegment string) (tenantRoot *os.Root, err error) {
	rootHandle, err := openVerifiedRoot(canonicalRoot, "DatasetRoot")
	if err != nil {
		return nil, err
	}
	defer func() {
		if rootHandle != nil {
			closeInto(rootHandle, &err, "DatasetRoot")
		}
	}()
	tenantInfo, err := rootHandle.Lstat(tenantSegment)
	if err != nil {
		return nil, fmt.Errorf("ai/tiny: lstat tenant DatasetRoot: %w", err)
	}
	if !isNonSymlinkDirectory(tenantInfo) {
		return nil, errors.New("ai/tiny: tenant DatasetRoot must be a non-symlink directory")
	}
	tenantRoot, err = rootHandle.OpenRoot(tenantSegment)
	if err != nil {
		return nil, fmt.Errorf("ai/tiny: open tenant DatasetRoot: %w", err)
	}
	openedTenantInfo, statErr := tenantRoot.Stat(".")
	if fileChanged(tenantInfo, openedTenantInfo, statErr) {
		return nil, closeWithCause(tenantRoot, errors.New("ai/tiny: tenant DatasetRoot changed while opening"), "tenant DatasetRoot")
	}
	closeErr := rootHandle.Close()
	rootHandle = nil
	if closeErr != nil {
		return nil, closeWithCause(
			tenantRoot, fmt.Errorf("ai/tiny: close DatasetRoot: %w", closeErr), "tenant DatasetRoot",
		)
	}
	return tenantRoot, nil
}

func isNonSymlinkDirectory(info fs.FileInfo) bool {
	return info.Mode()&os.ModeSymlink == 0 && info.IsDir()
}

func fileChanged(before, after fs.FileInfo, statErr error) bool {
	return statErr != nil || !os.SameFile(before, after)
}

func openVerifiedRoot(path, name string) (*os.Root, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("ai/tiny: lstat %s: %w", name, err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("ai/tiny: open %s: %w", name, err)
	}
	openedInfo, err := root.Stat(".")
	if err != nil {
		return nil, closeWithCause(root, fmt.Errorf("ai/tiny: stat opened %s: %w", name, err), name)
	}
	if !os.SameFile(pathInfo, openedInfo) {
		return nil, closeWithCause(root, fmt.Errorf("ai/tiny: %s changed while opening", name), name)
	}
	return root, nil
}

func copyDataset(
	ctx context.Context,
	datasetRoot *os.Root,
	relativePath, inputDir string,
	maxDatasetBytes int64,
) (uri string, err error) {
	pathInfo, err := validateDatasetPath(datasetRoot, relativePath)
	if err != nil {
		return "", err
	}
	source, err := openDatasetFile(datasetRoot, relativePath, pathInfo)
	if err != nil {
		return "", err
	}
	defer closeInto(source, &err, "local dataset source")
	if err = copyDatasetFile(ctx, source, inputDir, maxDatasetBytes); err != nil {
		return "", err
	}
	return "file:///work/input/dataset", nil
}

func validateDatasetPath(datasetRoot *os.Root, relativePath string) (fs.FileInfo, error) {
	var pathInfo fs.FileInfo
	current := ""
	components := strings.Split(relativePath, string(filepath.Separator))
	for index, component := range components {
		if component == "" || component == "." || component == ".." {
			return nil, errors.New("ai/tiny: dataset path contains an invalid component")
		}
		current = filepath.Join(current, component)
		var err error
		pathInfo, err = datasetRoot.Lstat(current)
		if err != nil {
			return nil, fmt.Errorf("ai/tiny: lstat local dataset component: %w", err)
		}
		if pathInfo.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("ai/tiny: local dataset path must not contain symbolic links")
		}
		if index < len(components)-1 && !pathInfo.IsDir() {
			return nil, errors.New("ai/tiny: local dataset parent must be a directory")
		}
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, errors.New("ai/tiny: local dataset must be a regular file")
	}
	return pathInfo, nil
}

func openDatasetFile(datasetRoot *os.Root, relativePath string, pathInfo fs.FileInfo) (*os.File, error) {
	source, err := datasetRoot.Open(relativePath)
	if err != nil {
		return nil, fmt.Errorf("ai/tiny: open local dataset: %w", err)
	}
	info, err := source.Stat()
	if err != nil {
		return nil, closeWithCause(source, fmt.Errorf("ai/tiny: stat local dataset: %w", err), "local dataset source")
	}
	if !info.Mode().IsRegular() {
		return nil, closeWithCause(source, errors.New("ai/tiny: local dataset must be a regular file"), "local dataset source")
	}
	if !os.SameFile(pathInfo, info) {
		return nil, closeWithCause(source, errors.New("ai/tiny: local dataset changed while opening"), "local dataset source")
	}
	return source, nil
}

func copyDatasetFile(
	ctx context.Context, source *os.File, inputDir string, maxDatasetBytes int64,
) (err error) {
	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("ai/tiny: stat local dataset: %w", err)
	}
	if info.Size() > maxDatasetBytes {
		return fmt.Errorf("%w: dataset is %d bytes, cap %d", ErrOutputTooLarge, info.Size(), maxDatasetBytes)
	}
	destinationPath := filepath.Join(inputDir, "dataset")
	inputRoot, err := os.OpenRoot(inputDir)
	if err != nil {
		return fmt.Errorf("ai/tiny: open staged input root: %w", err)
	}
	defer closeInto(inputRoot, &err, "staged input root")
	destination, err := inputRoot.OpenFile("dataset", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("ai/tiny: create staged dataset %q: %w", destinationPath, err)
	}
	defer closeInto(destination, &err, "staged dataset")
	copyLimit := maxDatasetBytes
	if maxDatasetBytes < math.MaxInt64 {
		copyLimit++
	}
	written, err := io.Copy(
		destination,
		io.LimitReader(contextReader{check: ctx.Err, src: source}, copyLimit),
	)
	if err != nil {
		return fmt.Errorf("ai/tiny: copy local dataset: %w", err)
	}
	if written > maxDatasetBytes {
		return fmt.Errorf("%w: dataset exceeds %d bytes", ErrOutputTooLarge, maxDatasetBytes)
	}
	if err = destination.Sync(); err != nil {
		return fmt.Errorf("ai/tiny: sync staged dataset: %w", err)
	}
	if err = destination.Chmod(0o444); err != nil {
		return fmt.Errorf("ai/tiny: protect staged dataset: %w", err)
	}
	return nil
}

// ReadOutput reads one fixed-name regular output file through an explicit cap.
func ReadOutput(ctx context.Context, outputDir, name string, maxBytes int64) (data []byte, err error) {
	file, _, err := openOutput(ctx, outputDir, name, maxBytes)
	if err != nil {
		return nil, err
	}
	defer closeInto(file, &err, "read output")
	readLimit := maxBytes
	if maxBytes < math.MaxInt64 {
		readLimit++
	}
	data, err = io.ReadAll(io.LimitReader(contextReader{check: ctx.Err, src: file}, readLimit))
	if err != nil {
		return nil, fmt.Errorf("ai/tiny: read output %q: %w", name, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: %q exceeds %d bytes", ErrOutputTooLarge, name, maxBytes)
	}
	return data, nil
}

// OutputUpload describes one content-addressed trainer artifact upload.
type OutputUpload struct {
	OutputDir    string
	SnapshotDir  string
	ArtifactName string
	KeyPrefix    string
	TenantID     string
	ModelName    string
	JobID        string
	MaxBytes     int64
	ContentType  string
}

// UploadOutput snapshots one verified output handle outside the writable
// output directory, then hashes and uploads the exact snapshot bytes.
func UploadOutput(
	ctx context.Context,
	bucket storage.Bucket,
	upload OutputUpload,
) (object storage.Object, err error) {
	upload.SnapshotDir, err = privateSnapshotDir(upload.SnapshotDir, upload.OutputDir)
	if err != nil {
		return storage.Object{}, err
	}
	file, size, err := openOutput(ctx, upload.OutputDir, upload.ArtifactName, upload.MaxBytes)
	if err != nil {
		return storage.Object{}, err
	}
	defer closeInto(file, &err, "source output")
	snapshot, digest, err := snapshotOutput(ctx, file, size, upload)
	if err != nil {
		return storage.Object{}, err
	}
	defer func() {
		closeInto(snapshot, &err, "output snapshot")
		if removeErr := os.Remove(snapshot.Name()); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("ai/tiny: remove output snapshot: %w", removeErr))
		}
		if err != nil {
			object = storage.Object{}
		}
	}()
	key, err := ArtifactKey(
		upload.KeyPrefix, upload.TenantID, upload.ModelName, upload.JobID,
		digest, upload.ArtifactName,
	)
	if err != nil {
		return storage.Object{}, err
	}
	object, err = putSnapshot(ctx, bucket, snapshot, size, digest, key, upload)
	if err != nil {
		return storage.Object{}, err
	}
	return object, nil
}

func privateSnapshotDir(snapshotDir, outputDir string) (string, error) {
	if snapshotDir == "" {
		return "", errors.New("ai/tiny: private output SnapshotDir required")
	}
	resolvedSnapshot, err := filepath.EvalSymlinks(snapshotDir)
	if err != nil {
		return "", fmt.Errorf("ai/tiny: resolve output SnapshotDir: %w", err)
	}
	info, err := os.Stat(resolvedSnapshot)
	if err != nil {
		return "", fmt.Errorf("ai/tiny: stat output SnapshotDir: %w", err)
	}
	if !isPrivateDirectory(info) {
		return "", errors.New("ai/tiny: output SnapshotDir must be a private directory")
	}
	resolvedOutput, err := filepath.EvalSymlinks(outputDir)
	if err != nil {
		return "", fmt.Errorf("ai/tiny: resolve output directory: %w", err)
	}
	relative, err := filepath.Rel(resolvedOutput, resolvedSnapshot)
	if err != nil {
		return "", fmt.Errorf("ai/tiny: compare output SnapshotDir: %w", err)
	}
	if !isOutsideDirectory(relative) {
		return "", errors.New("ai/tiny: output SnapshotDir must be outside the writable output directory")
	}
	return resolvedSnapshot, nil
}

func isPrivateDirectory(info fs.FileInfo) bool {
	return info.IsDir() && (runtime.GOOS == "windows" || info.Mode().Perm()&0o077 == 0)
}

func isOutsideDirectory(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func snapshotOutput(
	ctx context.Context, source *os.File, size int64, upload OutputUpload,
) (snapshot *os.File, digest string, err error) {
	snapshot, err = os.CreateTemp(upload.SnapshotDir, ".tiny-trainer-output-*")
	if err != nil {
		return nil, "", fmt.Errorf("ai/tiny: create output snapshot: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		err = closeWithCause(snapshot, err, "failed output snapshot")
		if removeErr := os.Remove(snapshot.Name()); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("ai/tiny: remove failed output snapshot: %w", removeErr))
		}
	}()
	hasher := sha256.New()
	written, err := io.Copy(
		io.MultiWriter(snapshot, hasher),
		io.LimitReader(contextReader{check: ctx.Err, src: source}, size),
	)
	if err != nil {
		return nil, "", fmt.Errorf("ai/tiny: snapshot output %q: %w", upload.ArtifactName, err)
	}
	if written != size {
		return nil, "", fmt.Errorf(
			"ai/tiny: snapshot output %q: read %d bytes, expected %d",
			upload.ArtifactName, written, size,
		)
	}
	if err = snapshot.Sync(); err != nil {
		return nil, "", fmt.Errorf("ai/tiny: sync output snapshot: %w", err)
	}
	if err = snapshot.Chmod(0o400); err != nil {
		return nil, "", fmt.Errorf("ai/tiny: protect output snapshot: %w", err)
	}
	if _, err = snapshot.Seek(0, io.SeekStart); err != nil {
		return nil, "", fmt.Errorf("ai/tiny: rewind output snapshot: %w", err)
	}
	return snapshot, hex.EncodeToString(hasher.Sum(nil)), nil
}

func putSnapshot(
	ctx context.Context,
	bucket storage.Bucket,
	snapshot *os.File,
	size int64,
	expectedDigest string,
	key string,
	upload OutputUpload,
) (storage.Object, error) {
	hasher := sha256.New()
	reader := io.LimitReader(
		contextReader{check: ctx.Err, src: io.TeeReader(snapshot, hasher)}, size,
	)
	object, err := bucket.Put(
		ctx, key, reader, storage.PutOptions{ContentType: upload.ContentType},
	)
	if err != nil {
		return storage.Object{}, fmt.Errorf("ai/tiny: upload output %q: %w", upload.ArtifactName, err)
	}
	actualDigest := hex.EncodeToString(hasher.Sum(nil))
	if object.Size == size && actualDigest == expectedDigest {
		return object, nil
	}
	cause := fmt.Errorf(
		"ai/tiny: upload output %q changed: stored %d/%d bytes with digest %s/%s",
		upload.ArtifactName, object.Size, size, actualDigest, expectedDigest,
	)
	if deleteErr := bucket.Delete(ctx, key); deleteErr != nil {
		cause = errors.Join(cause, fmt.Errorf("ai/tiny: delete rejected upload: %w", deleteErr))
	}
	return storage.Object{}, cause
}

func openOutput(
	ctx context.Context, outputDir, name string, maxBytes int64,
) (*os.File, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, fmt.Errorf("ai/tiny: inspect output %q: %w", name, err)
	}
	if maxBytes <= 0 {
		return nil, 0, fmt.Errorf("ai/tiny: output cap for %q must be positive", name)
	}
	if !filepath.IsLocal(name) || filepath.Base(name) != name {
		return nil, 0, fmt.Errorf("ai/tiny: invalid output name %q", name)
	}
	root, err := os.OpenRoot(outputDir)
	if err != nil {
		return nil, 0, fmt.Errorf("ai/tiny: open output root: %w", err)
	}
	file, size, openErr := openRootOutput(ctx, root, name, maxBytes)
	closeErr := root.Close()
	if openErr != nil || closeErr != nil {
		if file != nil {
			openErr = closeWithCause(file, openErr, "output after root failure")
		}
		if closeErr != nil {
			closeErr = fmt.Errorf("ai/tiny: close output root: %w", closeErr)
		}
		return nil, 0, errors.Join(openErr, closeErr)
	}
	return file, size, nil
}

func openRootOutput(
	ctx context.Context, root *os.Root, name string, maxBytes int64,
) (*os.File, int64, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, 0, fmt.Errorf("ai/tiny: inspect output %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("ai/tiny: output %q is not a regular file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, 0, fmt.Errorf("ai/tiny: open output %q: %w", name, err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		cause := fmt.Errorf("ai/tiny: stat output %q: %w", name, err)
		return nil, 0, closeWithCause(file, cause, "output after stat failure")
	}
	if err = validateOpenedOutput(name, info, openedInfo, maxBytes); err != nil {
		return nil, 0, closeWithCause(file, err, "rejected output")
	}
	if err = ctx.Err(); err != nil {
		cause := fmt.Errorf("ai/tiny: inspect output %q: %w", name, err)
		return nil, 0, closeWithCause(file, cause, "canceled output")
	}
	return file, openedInfo.Size(), nil
}

func validateOpenedOutput(name string, pathInfo, openedInfo fs.FileInfo, maxBytes int64) error {
	if !openedInfo.Mode().IsRegular() {
		return fmt.Errorf("ai/tiny: opened output %q is not a regular file", name)
	}
	if !os.SameFile(pathInfo, openedInfo) {
		return fmt.Errorf("ai/tiny: output %q changed while opening", name)
	}
	if openedInfo.Size() > maxBytes {
		return fmt.Errorf(
			"%w: %q is %d bytes, cap %d", ErrOutputTooLarge, name, openedInfo.Size(), maxBytes,
		)
	}
	return nil
}

type contextReader struct {
	check func() error
	src   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, fmt.Errorf("ai/tiny: read trainer output: %w", err)
	}
	n, err := r.src.Read(p)
	if contextErr := r.check(); contextErr != nil {
		return n, fmt.Errorf("ai/tiny: read trainer output: %w", contextErr)
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("ai/tiny: read trainer output: %w", err)
	}
	return n, nil
}

func closeInto(closer io.Closer, target *error, operation string) {
	if err := closer.Close(); err != nil {
		*target = errors.Join(*target, fmt.Errorf("ai/tiny: close %s: %w", operation, err))
	}
}

func closeWithCause(closer io.Closer, cause error, operation string) error {
	if err := closer.Close(); err != nil {
		return errors.Join(cause, fmt.Errorf("ai/tiny: close %s: %w", operation, err))
	}
	return cause
}

var _ io.Writer = (*CappedLog)(nil)
