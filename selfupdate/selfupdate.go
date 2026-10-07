// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package selfupdate provides binary self-update from GitHub releases.
//
// The updater fetches the latest GitHub release, requires a newer semantic
// version, selects the exact versioned archive for the running OS/arch,
// authenticates checksums.txt with a caller-supplied publisher verifier,
// verifies the archive digest, and replaces the current executable.
//
// Usage:
//
//	result, err := selfupdate.Update(ctx, selfupdate.Options{
//	    Owner:             "golusoris",
//	    Repo:              "myapp",
//	    Version:           version.Read().Version,
//	    PublisherVerifier: selfupdate.NewPublisherVerifier(verifyPublisher),
//	})
//	if result.Updated {
//	    fmt.Println("Updated — restart to use the new version.")
//	}
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/minio/selfupdate"

	"github.com/golusoris/golusoris/core/errors"
	httpclient "github.com/golusoris/golusoris/httpx/client"
)

const (
	// DefaultMaxAssetBytes caps the archive and extracted binary buffered before
	// atomic replacement.
	DefaultMaxAssetBytes int64 = 256 << 20
	// DefaultOperationTimeout bounds the complete release lookup, verification,
	// download, and apply operation when Options.OperationTimeout is zero.
	DefaultOperationTimeout = 10 * time.Minute
	maxChecksumBytes        = 4 << 20
	maxPublisherBundleBytes = 4 << 20
	maxReleaseMetadataBytes = 1 << 20
	maxArchiveOverheadBytes = 64 << 20
	maxExpandedArchiveBytes = 1 << 30
	maxReleaseAssets        = 256
	maxChecksumLines        = 512
	maxArchiveEntries       = 256
	checksumAssetName       = "checksums.txt"
	checksumBundleName      = checksumAssetName + ".sigstore.json"
)

var errTrailingReleaseJSON = stderrors.New("trailing JSON value")

// PublisherVerifier authenticates manifest against its Sigstore bundle using
// a caller-pinned trust policy. It must fail closed on every mismatch.
type PublisherVerifier func(
	ctx context.Context,
	manifest []byte,
	bundle []byte,
) error

// NewPublisherVerifier stores verify behind a comparable pointer for Options.
// A nil callback returns nil and is rejected by Update when an update exists.
func NewPublisherVerifier(verify PublisherVerifier) *PublisherVerifier {
	if verify == nil {
		return nil
	}
	return &verify
}

// Options configures the updater.
type Options struct {
	Owner   string // GitHub org or user
	Repo    string // Repository name
	Version string // Current version (e.g. "v1.2.3"); used to detect whether an update is available

	// AssetName overrides the exact release archive name.
	// Default: "<repo>_<version>_<os>_<arch>.<format>".
	AssetName string
	// BinaryName overrides the exact executable member selected from the
	// release archive. Empty uses the running executable's base name.
	BinaryName string

	// HTTPClient overrides the HTTP client used to call the GitHub API.
	// nil uses the bounded framework default.
	HTTPClient *http.Client

	// MaxAssetBytes caps the downloaded archive and extracted binary. 0 uses
	// DefaultMaxAssetBytes.
	MaxAssetBytes int64
	// OperationTimeout bounds the complete update operation. Zero uses
	// DefaultOperationTimeout; negative values are invalid. A sooner caller
	// deadline still wins.
	OperationTimeout time.Duration

	// PublisherVerifier authenticates checksums.txt and is required when an
	// update is available. The injected verifier owns the pinned trust policy.
	PublisherVerifier *PublisherVerifier
}

// Result describes the outcome of an update check.
type Result struct {
	Updated        bool
	LatestVersion  string
	CurrentVersion string
}

// Update checks for a newer GitHub release and, if one exists, replaces the
// running binary. Returns Updated=false when already on the latest version.
func Update(ctx context.Context, opts Options) (Result, error) {
	ctx, cancel, err := operationContext(ctx, opts.OperationTimeout)
	if err != nil {
		return Result{}, err
	}
	defer cancel()

	client := updateClient(opts.HTTPClient)
	release, err := latestRelease(ctx, client, opts.Owner, opts.Repo)
	if err != nil {
		return Result{}, fmt.Errorf("selfupdate: fetch latest release: %w", err)
	}

	result := Result{
		LatestVersion:  release.TagName,
		CurrentVersion: opts.Version,
	}
	available, err := updateAvailable(opts.Version, release.TagName)
	if err != nil {
		return result, err
	}
	if !available {
		return result, nil // already up to date
	}
	asset, maxAssetBytes, verifier, err := prepareUpdate(release, opts)
	if err != nil {
		return result, err
	}
	archive, err := fetchVerifiedArchive(
		ctx,
		client,
		release,
		asset,
		maxAssetBytes,
		verifier,
	)
	if err != nil {
		return result, err
	}
	binaryName, err := selectedBinaryName(opts)
	if err != nil {
		return result, fmt.Errorf("selfupdate: select binary: %w", err)
	}
	data, err := extractBinary(ctx, archive, asset.Name, binaryName, maxAssetBytes)
	if err != nil {
		return result, fmt.Errorf("selfupdate: extract binary: %w", err)
	}
	if err := applyBinary(ctx, data, selfupdate.Apply); err != nil {
		return result, fmt.Errorf("selfupdate: apply: %w", err)
	}

	result.Updated = true
	return result, nil
}

func operationContext(
	ctx context.Context,
	configured time.Duration,
) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		return nil, nil, stderrors.New("selfupdate: context is required")
	}
	if configured < 0 {
		return nil, nil, fmt.Errorf(
			"selfupdate: invalid OperationTimeout %s",
			configured,
		)
	}
	operationTimeout := configured
	if operationTimeout == 0 {
		operationTimeout = DefaultOperationTimeout
	}
	bounded, cancel := context.WithTimeout(ctx, operationTimeout)
	return bounded, cancel, nil
}

func prepareUpdate(release ghRelease, opts Options) (ghAsset, int64, *PublisherVerifier, error) {
	asset, err := selectAsset(release, opts)
	if err != nil {
		return ghAsset{}, 0, nil, fmt.Errorf("selfupdate: select asset: %w", err)
	}
	maxAssetBytes, err := assetByteLimit(opts.MaxAssetBytes)
	if err != nil {
		return ghAsset{}, 0, nil, err
	}
	if err := validatePublisher(opts); err != nil {
		return ghAsset{}, 0, nil, err
	}
	return asset, maxAssetBytes, opts.PublisherVerifier, nil
}

func validatePublisher(opts Options) error {
	if opts.PublisherVerifier == nil || *opts.PublisherVerifier == nil {
		return stderrors.New("selfupdate: publisher verifier is required")
	}
	return nil
}

func updateClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return httpclient.New(httpclient.Options{Name: "golusoris.selfupdate"})
}

func updateAvailable(current, latest string) (bool, error) {
	comparison, err := compareReleaseVersions(current, latest)
	if err != nil {
		return false, fmt.Errorf("selfupdate: compare versions: %w", err)
	}
	if comparison < 0 {
		return false, fmt.Errorf("selfupdate: refusing downgrade from %q to %q", current, latest)
	}
	return comparison > 0, nil
}

func assetByteLimit(configured int64) (int64, error) {
	if configured == 0 {
		return DefaultMaxAssetBytes, nil
	}
	if configured < 0 || configured == math.MaxInt64 {
		return 0, fmt.Errorf("selfupdate: invalid MaxAssetBytes %d", configured)
	}
	return configured, nil
}

func fetchVerifiedArchive(
	ctx context.Context,
	client *http.Client,
	release ghRelease,
	asset ghAsset,
	maxBytes int64,
	verifier *PublisherVerifier,
) ([]byte, error) {
	checksum, err := fetchChecksum(ctx, client, release, asset, verifier)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: verify release manifest: %w", err)
	}
	return downloadAsset(ctx, client, asset.BrowserDownloadURL, checksum, maxBytes)
}

// downloadAsset fetches assetURL into memory and verifies its required SHA-256.
// The response body is closed — and a close failure
// surfaced — before the caller applies the binary, so Updated=true is never
// paired with a non-nil error.
func downloadAsset(
	ctx context.Context,
	client *http.Client,
	assetURL string,
	checksum []byte,
	maxBytes int64,
) (data []byte, err error) {
	if len(checksum) != sha256.Size {
		return nil, fmt.Errorf("selfupdate: invalid SHA-256 length %d", len(checksum))
	}
	rc, err := fetchAsset(ctx, client, assetURL)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: fetch asset: %w", err)
	}
	defer errors.CloseInto(rc, &err, "selfupdate: close asset body")

	h := sha256.New()
	data, err = readRemoteBody(io.TeeReader(rc, h), maxBytes)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: read asset: %w", err)
	}
	if got := h.Sum(nil); !bytes.Equal(got, checksum) {
		return nil, fmt.Errorf(
			"selfupdate: checksum mismatch: got %s, want %s",
			hex.EncodeToString(got),
			hex.EncodeToString(checksum),
		)
	}
	return data, nil
}

// ghRelease is a minimal GitHub API /releases/latest response.
type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func latestRelease(ctx context.Context, client *http.Client, owner, repo string) (rel ghRelease, err error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ghRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req) //nolint:bodyclose // closed by the deferred errors.CloseInto below
	if err != nil {
		return ghRelease{}, err
	}
	defer errors.CloseInto(resp.Body, &err, "selfupdate: close release body")

	if resp.StatusCode != http.StatusOK {
		return ghRelease{}, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	return decodeReleaseBody(resp.Body)
}

func decodeReleaseBody(body io.Reader) (ghRelease, error) {
	data, err := readRemoteBody(body, maxReleaseMetadataBytes)
	if err != nil {
		return ghRelease{}, fmt.Errorf("read release metadata: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var rel ghRelease
	if err = decoder.Decode(&rel); err != nil {
		return ghRelease{}, fmt.Errorf("decode release metadata: %w", err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ghRelease{}, fmt.Errorf("decode release metadata: %w", errTrailingReleaseJSON)
		}
		return ghRelease{}, fmt.Errorf("decode release metadata trailing data: %w", err)
	}
	if len(rel.Assets) > maxReleaseAssets {
		return ghRelease{}, fmt.Errorf("release has %d assets; limit is %d", len(rel.Assets), maxReleaseAssets)
	}
	return rel, nil
}

func compareReleaseVersions(current, latest string) (int, error) {
	currentVersion, err := parseReleaseVersion("installed", current)
	if err != nil {
		return 0, err
	}
	latestVersion, err := parseReleaseVersion("release", latest)
	if err != nil {
		return 0, err
	}
	return latestVersion.Compare(currentVersion), nil
}

func parseReleaseVersion(label, raw string) (*semver.Version, error) {
	version := strings.TrimPrefix(raw, "v")
	parsed, err := semver.StrictNewVersion(version)
	if err != nil {
		return nil, fmt.Errorf("%s version %q is not strict semantic version: %w", label, raw, err)
	}
	return parsed, nil
}

func selectAsset(rel ghRelease, opts Options) (ghAsset, error) {
	return selectAssetForPlatform(rel, opts, runtime.GOOS, runtime.GOARCH)
}

func selectAssetForPlatform(rel ghRelease, opts Options, goos, goarch string) (ghAsset, error) {
	if len(rel.Assets) > maxReleaseAssets {
		return ghAsset{}, fmt.Errorf("release has %d assets; limit is %d", len(rel.Assets), maxReleaseAssets)
	}
	want, err := selectedArchiveName(rel.TagName, opts, goos, goarch)
	if err != nil {
		return ghAsset{}, err
	}
	match, err := exactAsset(rel.Assets, want)
	if err != nil {
		return ghAsset{}, fmt.Errorf("%w for %s/%s", err, goos, goarch)
	}
	return match, nil
}

func selectedArchiveName(tag string, opts Options, goos, goarch string) (string, error) {
	if opts.AssetName != "" {
		return opts.AssetName, nil
	}
	version, err := parseReleaseVersion("release", tag)
	if err != nil {
		return "", err
	}
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", opts.Repo, version.String(), goos, goarch, extension), nil
}

func exactAsset(assets []ghAsset, want string) (ghAsset, error) {
	var match ghAsset
	matches := 0
	for index := 0; index < len(assets) && index < maxReleaseAssets; index++ {
		asset := assets[index]
		if asset.Name != want {
			continue
		}
		match = asset
		matches++
	}
	if matches == 0 {
		return ghAsset{}, fmt.Errorf("no exact asset %q", want)
	}
	if matches != 1 {
		return ghAsset{}, fmt.Errorf("release contains %d assets named %q", matches, want)
	}
	if match.BrowserDownloadURL == "" {
		return ghAsset{}, fmt.Errorf("asset %q has no download URL", want)
	}
	return match, nil
}

// fetchChecksum requires checksums.txt and extracts the selected archive's SHA-256.
func fetchChecksum(
	ctx context.Context,
	client *http.Client,
	rel ghRelease,
	asset ghAsset,
	verifier *PublisherVerifier,
) (sum []byte, err error) {
	checksumAsset, err := findChecksumAsset(rel.Assets)
	if err != nil {
		return nil, err
	}
	bundleAsset, err := findPublisherBundle(rel.Assets)
	if err != nil {
		return nil, err
	}
	manifest, err := fetchReleaseAsset(ctx, client, checksumAsset, maxChecksumBytes, "checksum")
	if err != nil {
		return nil, err
	}
	bundle, err := fetchReleaseAsset(ctx, client, bundleAsset, maxPublisherBundleBytes, "publisher bundle")
	if err != nil {
		return nil, err
	}
	if verifyErr := (*verifier)(ctx, manifest, bundle); verifyErr != nil {
		return nil, fmt.Errorf("publisher verification: %w", verifyErr)
	}
	return parseChecksum(manifest, asset.Name)
}

func fetchReleaseAsset(
	ctx context.Context,
	client *http.Client,
	asset ghAsset,
	maxBytes int64,
	label string,
) (data []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req) //nolint:bodyclose // closed by the deferred errors.CloseInto below
	if err != nil {
		return nil, err
	}
	defer errors.CloseInto(resp.Body, &err, "selfupdate: close "+label+" body")
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s download returned %d", label, resp.StatusCode)
	}
	return readRemoteBody(resp.Body, maxBytes)
}

func readRemoteBody(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return nil, fmt.Errorf("invalid byte limit %d", maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d-byte limit", maxBytes)
	}
	return data, nil
}

func findChecksumAsset(assets []ghAsset) (ghAsset, error) {
	if len(assets) > maxReleaseAssets {
		return ghAsset{}, fmt.Errorf("release has %d assets; limit is %d", len(assets), maxReleaseAssets)
	}
	match, err := exactAsset(assets, checksumAssetName)
	if err != nil {
		return ghAsset{}, fmt.Errorf("checksum manifest: %w", err)
	}
	return match, nil
}

func findPublisherBundle(assets []ghAsset) (ghAsset, error) {
	if len(assets) > maxReleaseAssets {
		return ghAsset{}, fmt.Errorf("release has %d assets; limit is %d", len(assets), maxReleaseAssets)
	}
	match, err := exactAsset(assets, checksumBundleName)
	if err != nil {
		return ghAsset{}, fmt.Errorf("publisher bundle: %w", err)
	}
	return match, nil
}

// parseChecksum extracts the SHA-256 for assetName from a goreleaser-format
// checksums file ("<sha256>  <filename>\n" per line).
func parseChecksum(data []byte, assetName string) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxChecksumLines {
		return nil, fmt.Errorf("checksum manifest has %d lines; limit is %d", len(lines), maxChecksumLines)
	}
	var match []byte
	for index := 0; index < len(lines) && index < maxChecksumLines; index++ {
		filename, digest, err := parseChecksumLine(lines[index], index+1)
		if err != nil {
			return nil, err
		}
		if filename == "" || filename != assetName {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("duplicate checksum for %q", assetName)
		}
		match = digest
	}
	if match == nil {
		return nil, fmt.Errorf("manifest has no checksum for %q", assetName)
	}
	return match, nil
}

func parseChecksumLine(line string, lineNumber int) (string, []byte, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", nil, nil
	}
	parts := strings.Fields(line)
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("invalid checksum manifest line %d", lineNumber)
	}
	digest, err := hex.DecodeString(parts[0])
	if err != nil || len(digest) != sha256.Size {
		return "", nil, fmt.Errorf("invalid SHA-256 on manifest line %d", lineNumber)
	}
	return parts[1], digest, nil
}

func selectedBinaryName(opts Options) (string, error) {
	name := opts.BinaryName
	if name == "" {
		executable, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("resolve running executable: %w", err)
		}
		name = filepath.Base(executable)
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		name += ".exe"
	}
	if name == "" || name == "." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("invalid binary name %q", name)
	}
	return name, nil
}

func extractBinary(
	ctx context.Context,
	archive []byte,
	archiveName string,
	binaryName string,
	maxBytes int64,
) ([]byte, error) {
	if err := contextError(ctx, "before archive extraction"); err != nil {
		return nil, err
	}
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return nil, fmt.Errorf("invalid byte limit %d", maxBytes)
	}
	if binaryName == "" || binaryName == "." || strings.ContainsAny(binaryName, `/\`) {
		return nil, fmt.Errorf("invalid binary name %q", binaryName)
	}
	switch {
	case strings.HasSuffix(archiveName, ".tar.gz"):
		return extractTarGzBinary(ctx, archive, binaryName, maxBytes)
	case strings.HasSuffix(archiveName, ".zip"):
		return extractZipBinary(ctx, archive, binaryName, maxBytes)
	default:
		return nil, fmt.Errorf("unsupported release archive %q", archiveName)
	}
}

func extractTarGzBinary(
	ctx context.Context,
	archive []byte,
	binaryName string,
	maxBytes int64,
) (data []byte, err error) {
	archiveReader := contextCheckedReader(ctx, bytes.NewReader(archive))
	gzipReader, err := gzip.NewReader(archiveReader)
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer errors.CloseInto(gzipReader, &err, "selfupdate: close gzip reader")

	expandedLimit := expandedArchiveLimit(maxBytes)
	limited := &io.LimitedReader{
		R: contextCheckedReader(ctx, gzipReader),
		N: expandedLimit + 1,
	}
	tarReader := tar.NewReader(limited)
	collector := binaryCollector{name: binaryName, maxBytes: maxBytes}
	for range maxArchiveEntries {
		if err := contextError(ctx, "during tar extraction"); err != nil {
			return nil, err
		}
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			return collector.result()
		}
		if nextErr != nil {
			return nil, fmt.Errorf("read tar entry: %w", nextErr)
		}
		if err := collector.accept(
			ctx,
			header.Name,
			header.FileInfo().Mode(),
			header.Size,
			tarReader,
			true,
		); err != nil {
			return nil, err
		}
	}
	if _, nextErr := tarReader.Next(); nextErr == io.EOF {
		return collector.result()
	} else if nextErr != nil {
		return nil, fmt.Errorf("read tar entry after limit boundary: %w", nextErr)
	}
	return nil, fmt.Errorf("archive exceeds %d-entry limit", maxArchiveEntries)
}

func extractZipBinary(
	ctx context.Context,
	archive []byte,
	binaryName string,
	maxBytes int64,
) ([]byte, error) {
	if err := contextError(ctx, "before zip extraction"); err != nil {
		return nil, err
	}
	zipReader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	if err := contextError(ctx, "during zip extraction"); err != nil {
		return nil, err
	}
	if len(zipReader.File) > maxArchiveEntries {
		return nil, fmt.Errorf("archive has %d entries; limit is %d", len(zipReader.File), maxArchiveEntries)
	}
	collector := binaryCollector{name: binaryName, maxBytes: maxBytes}
	for entryIndex := 0; entryIndex < len(zipReader.File) && entryIndex < maxArchiveEntries; entryIndex++ {
		if err := contextError(ctx, "during zip extraction"); err != nil {
			return nil, err
		}
		entry := zipReader.File[entryIndex]
		if err := collectZipEntry(ctx, &collector, entry); err != nil {
			return nil, err
		}
	}
	return collector.result()
}

type binaryCollector struct {
	name     string
	maxBytes int64
	data     []byte
}

func (collector *binaryCollector) accept(
	ctx context.Context,
	name string,
	mode os.FileMode,
	size int64,
	reader io.Reader,
	requireExecutable bool,
) error {
	if name != collector.name {
		return nil
	}
	if collector.data != nil {
		return fmt.Errorf("archive contains duplicate executable %q", collector.name)
	}
	if !mode.IsRegular() || requireExecutable && mode&0o111 == 0 {
		return fmt.Errorf("archive member %q is not a regular executable", collector.name)
	}
	if size < 1 || size > collector.maxBytes {
		return fmt.Errorf("archive member %q has invalid size %d", collector.name, size)
	}
	checkedReader := contextCheckedReader(ctx, reader)
	data, err := readRemoteBody(checkedReader, collector.maxBytes)
	if err != nil {
		return fmt.Errorf("read archive member %q: %w", collector.name, err)
	}
	collector.data = data
	return nil
}

func (collector *binaryCollector) result() ([]byte, error) {
	if collector.data == nil {
		return nil, fmt.Errorf("archive has no executable %q", collector.name)
	}
	return collector.data, nil
}

func collectZipEntry(
	ctx context.Context,
	collector *binaryCollector,
	entry *zip.File,
) (err error) {
	if entry.Name != collector.name {
		return nil
	}
	if entry.UncompressedSize64 > math.MaxInt64 {
		return fmt.Errorf("archive member %q is too large", collector.name)
	}
	reader, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open archive member %q: %w", collector.name, err)
	}
	defer errors.CloseInto(reader, &err, "selfupdate: close zip member")
	return collector.accept(
		ctx,
		entry.Name,
		entry.FileInfo().Mode(),
		int64(entry.UncompressedSize64),
		reader,
		false,
	)
}

type binaryApplyFunc func(io.Reader, selfupdate.Options) error

type rollbackErrorFunc func(error) error

func applyBinary(ctx context.Context, data []byte, apply binaryApplyFunc) error {
	return applyBinaryWithRollback(ctx, data, apply, selfupdate.RollbackError)
}

func applyBinaryWithRollback(
	ctx context.Context,
	data []byte,
	apply binaryApplyFunc,
	rollbackError rollbackErrorFunc,
) error {
	if err := contextError(ctx, "before executable replacement"); err != nil {
		return err
	}
	reader := contextCheckedReader(ctx, bytes.NewReader(data))
	applyErr := apply(reader, selfupdate.Options{})
	if applyErr == nil {
		return nil
	}
	if rollbackErr := rollbackError(applyErr); rollbackErr != nil {
		return stderrors.Join(
			applyErr,
			fmt.Errorf("selfupdate: executable rollback failed: %w", rollbackErr),
		)
	}
	return applyErr
}

type contextReaderFunc func([]byte) (int, error)

func (read contextReaderFunc) Read(buffer []byte) (int, error) {
	return read(buffer)
}

func contextCheckedReader(ctx context.Context, source io.Reader) contextReaderFunc {
	return func(buffer []byte) (int, error) {
		if err := contextError(ctx, "before read"); err != nil {
			return 0, err
		}
		count, err := source.Read(buffer)
		if contextErr := contextError(ctx, "after read"); contextErr != nil {
			return count, contextErr
		}
		if err == nil {
			return count, nil
		}
		if err == io.EOF {
			return count, io.EOF
		}
		return count, fmt.Errorf("read source: %w", err)
	}
}

func contextError(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", stage, err)
	}
	return nil
}

func expandedArchiveLimit(maxBytes int64) int64 {
	if maxBytes > maxExpandedArchiveBytes-maxArchiveOverheadBytes {
		return maxExpandedArchiveBytes
	}
	return maxBytes + maxArchiveOverheadBytes
}

func fetchAsset(ctx context.Context, client *http.Client, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("asset download returned %d", resp.StatusCode)
		errors.CloseJoin(resp.Body, &statusErr, "selfupdate: close asset body")
		return nil, statusErr
	}
	return resp.Body, nil
}
