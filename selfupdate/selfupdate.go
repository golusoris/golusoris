// Package selfupdate provides binary self-update from GitHub releases.
//
// The updater fetches the latest GitHub release, selects the asset matching
// the running OS/arch, verifies its SHA-256 checksum (against a *_checksums.txt
// asset if present), and replaces the current executable.
//
// Usage:
//
//	updated, err := selfupdate.Update(ctx, selfupdate.Options{
//	    Owner:   "golusoris",
//	    Repo:    "myapp",
//	    Version: version.Current, // e.g. "v1.2.3"
//	})
//	if updated {
//	    fmt.Println("Updated — restart to use the new version.")
//	}
package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"

	"github.com/minio/selfupdate"
)

// Options configures the updater.
type Options struct {
	Owner   string // GitHub org or user
	Repo    string // Repository name
	Version string // Current version (e.g. "v1.2.3"); used to detect whether an update is available

	// AssetName overrides the asset selection heuristic.
	// Default: "<repo>_<os>_<arch>" (case-insensitive prefix match).
	AssetName string

	// HTTPClient overrides the HTTP client used to call the GitHub API.
	// Defaults to http.DefaultClient.
	HTTPClient *http.Client
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
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	release, err := latestRelease(ctx, client, opts.Owner, opts.Repo)
	if err != nil {
		return Result{}, fmt.Errorf("selfupdate: fetch latest release: %w", err)
	}

	result := Result{
		LatestVersion:  release.TagName,
		CurrentVersion: opts.Version,
	}
	if release.TagName == opts.Version {
		return result, nil // already up to date
	}

	assetURL, err := selectAsset(release, opts)
	if err != nil {
		return result, fmt.Errorf("selfupdate: select asset: %w", err)
	}

	checksum, _ := fetchChecksum(ctx, client, release, assetURL) // best-effort

	rc, err := fetchAsset(ctx, client, assetURL)
	if err != nil {
		return result, fmt.Errorf("selfupdate: fetch asset: %w", err)
	}
	defer func() { _ = rc.Close() }()

	h := sha256.New()
	r := io.TeeReader(rc, h)
	data, err2 := io.ReadAll(r)
	if err2 != nil {
		return result, fmt.Errorf("selfupdate: read asset: %w", err2)
	}
	if checksum != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != checksum {
			return result, fmt.Errorf("selfupdate: checksum mismatch: got %s, want %s", got, checksum)
		}
	}

	if err := selfupdate.Apply(bytes.NewReader(data), selfupdate.Options{}); err != nil {
		return result, fmt.Errorf("selfupdate: apply: %w", err)
	}

	result.Updated = true
	return result, nil
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

func latestRelease(ctx context.Context, client *http.Client, owner, repo string) (ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ghRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return ghRelease{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ghRelease{}, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return ghRelease{}, err
	}
	return rel, nil
}

func selectAsset(rel ghRelease, opts Options) (string, error) {
	prefix := opts.AssetName
	if prefix == "" {
		prefix = fmt.Sprintf("%s_%s_%s", opts.Repo, runtime.GOOS, runtime.GOARCH)
	}
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, prefix) || strings.HasPrefix(strings.ToLower(a.Name), strings.ToLower(prefix)) {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no asset matching %q for %s/%s", prefix, runtime.GOOS, runtime.GOARCH)
}

// fetchChecksum looks for a *_checksums.txt asset and extracts the SHA-256 for assetURL.
func fetchChecksum(ctx context.Context, client *http.Client, rel ghRelease, assetURL string) (string, error) {
	var checksumURL string
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, "_checksums.txt") || strings.HasSuffix(a.Name, "checksums.txt") {
			checksumURL = a.BrowserDownloadURL
			break
		}
	}
	if checksumURL == "" {
		return "", nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// goreleaser format: "<sha256>  <filename>\n"
	assetName := assetURL[strings.LastIndex(assetURL, "/")+1:]
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && strings.EqualFold(parts[1], assetName) {
			return parts[0], nil
		}
	}
	return "", nil
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
		_ = resp.Body.Close()
		return nil, fmt.Errorf("asset download returned %d", resp.StatusCode)
	}
	return resp.Body, nil
}
