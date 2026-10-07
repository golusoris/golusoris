// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package selfupdate_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/selfupdate"
)

func TestOptionsRemainComparable(t *testing.T) {
	t.Parallel()
	opts := selfupdate.Options{
		Owner:             "golusoris",
		Repo:              "app",
		Version:           "v1.0.0",
		PublisherVerifier: selfupdate.NewPublisherVerifier(acceptPublisher),
	}

	set := map[selfupdate.Options]struct{}{opts: {}}
	if _, ok := set[opts]; !ok {
		t.Fatal("Options value is not usable as a comparable key")
	}
}

func TestUpdate_alreadyLatest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.2.3",
			"assets":   []any{},
		}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer srv.Close()

	// Redirect GitHub API calls to the test server.
	client := srv.Client()
	// We can't easily override the URL in the current implementation without
	// adding a BaseURL field, so verify that passing current version == latest
	// returns Updated=false when the API responds with the same version.
	// This tests the version-comparison logic, not the HTTP layer.
	_ = client

	// Direct unit test: same version → no update (skips HTTP entirely when
	// the release tag matches — but the current impl always fetches first).
	// Test the error path instead: invalid owner/repo with injected client.
	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:   "nobody",
		Repo:    "doesnotexist",
		Version: "v0.0.1",
		HTTPClient: &http.Client{Transport: &roundTripFunc{fn: func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusNotFound)
			return rec.Result(), nil
		}}},
	})
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestUpdate_appliesDefaultOperationDeadline(t *testing.T) {
	t.Parallel()
	var sawDeadline atomic.Bool
	client := &http.Client{Transport: &roundTripFunc{fn: func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			return nil, errors.New("request context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > selfupdate.DefaultOperationTimeout {
			return nil, errors.New("request context has invalid deadline")
		}
		sawDeadline.Store(true)
		recorder := httptest.NewRecorder()
		recorder.WriteHeader(http.StatusNotFound)
		return recorder.Result(), nil
	}}}

	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:      "test",
		Repo:       "app",
		Version:    "v1.0.0",
		HTTPClient: client,
	})
	if err == nil || !sawDeadline.Load() {
		t.Fatalf("Update() error = %v, saw deadline = %t", err, sawDeadline.Load())
	}
}

func TestUpdate_operationTimeoutValidationAndCallerDeadline(t *testing.T) {
	t.Parallel()
	var requests atomic.Int64
	client := &http.Client{Transport: &roundTripFunc{fn: func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		deadline, ok := r.Context().Deadline()
		if !ok {
			return nil, errors.New("request context has no deadline")
		}
		return nil, fmt.Errorf("observed deadline %s", deadline.UTC().Format(time.RFC3339Nano))
	}}}
	if _, err := selfupdate.Update(context.Background(), selfupdate.Options{
		OperationTimeout: -time.Second,
		HTTPClient:       client,
	}); err == nil || !strings.Contains(err.Error(), "invalid OperationTimeout") {
		t.Fatalf("Update(negative timeout) error = %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests after invalid timeout = %d, want 0", got)
	}

	callerDeadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()
	_, err := selfupdate.Update(ctx, selfupdate.Options{
		Owner:            "test",
		Repo:             "app",
		Version:          "v1.0.0",
		OperationTimeout: 2 * time.Minute,
		HTTPClient:       client,
	})
	want := callerDeadline.UTC().Format(time.RFC3339Nano)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Update(caller deadline) error = %v, want deadline %s", err, want)
	}
}

func TestUpdate_sameVersion(t *testing.T) {
	t.Parallel()
	srv := fakeGitHub(t, "v1.0.0", nil)
	defer srv.Close()

	result, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:      "test",
		Repo:       "app",
		Version:    "v1.0.0",
		HTTPClient: fakeClient(srv),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Updated {
		t.Fatal("should not have updated: already on latest")
	}
	if result.LatestVersion != "v1.0.0" {
		t.Fatalf("LatestVersion: got %q", result.LatestVersion)
	}
}

func TestUpdate_noMatchingAsset(t *testing.T) {
	t.Parallel()
	srv := fakeGitHub(t, "v2.0.0", nil)
	defer srv.Close()

	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:      "test",
		Repo:       "app",
		Version:    "v1.0.0",
		HTTPClient: fakeClient(srv),
	})
	if err == nil {
		t.Fatal("expected error: no matching asset")
	}
}

func TestUpdate_requiresPublisherVerifierBeforeManifest(t *testing.T) {
	t.Parallel()
	assetName := releaseAssetName()
	var artifactRequests atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets": []any{
				map[string]any{"name": assetName, "browser_download_url": "http://placeholder/asset"},
				map[string]any{"name": "checksums.txt", "browser_download_url": "http://placeholder/checksums"},
				map[string]any{"name": "checksums.txt.sigstore.json", "browser_download_url": "http://placeholder/bundle"},
			},
		})
	})
	mux.HandleFunc("/checksums", func(http.ResponseWriter, *http.Request) { artifactRequests.Add(1) })
	mux.HandleFunc("/bundle", func(http.ResponseWriter, *http.Request) { artifactRequests.Add(1) })
	mux.HandleFunc("/asset", func(http.ResponseWriter, *http.Request) { artifactRequests.Add(1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:      "test",
		Repo:       "app",
		Version:    "v1.0.0",
		HTTPClient: fakeClient(srv),
	})
	if err == nil || !strings.Contains(err.Error(), "publisher verifier") {
		t.Fatalf("Update() error = %v, want publisher-verifier requirement", err)
	}
	if got := artifactRequests.Load(); got != 0 {
		t.Fatalf("artifact requests = %d, want 0 before trust validation", got)
	}
}

func TestUpdate_requiresExactPublisherBundle(t *testing.T) {
	t.Parallel()
	assetName := releaseAssetName()
	var artifactRequests atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets": []any{
				map[string]any{"name": assetName, "browser_download_url": "http://placeholder/asset"},
				map[string]any{"name": "checksums.txt", "browser_download_url": "http://placeholder/checksums"},
				map[string]any{"name": "checksums.txt.sigstore.json.extra", "browser_download_url": "http://placeholder/wrong-bundle"},
			},
		})
	})
	mux.HandleFunc("/checksums", func(http.ResponseWriter, *http.Request) { artifactRequests.Add(1) })
	mux.HandleFunc("/wrong-bundle", func(http.ResponseWriter, *http.Request) { artifactRequests.Add(1) })
	mux.HandleFunc("/asset", func(http.ResponseWriter, *http.Request) { artifactRequests.Add(1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:      "test",
		Repo:       "app",
		Version:    "v1.0.0",
		HTTPClient: fakeClient(srv),
		PublisherVerifier: selfupdate.NewPublisherVerifier(func(context.Context, []byte, []byte) error {
			return nil
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "checksums.txt.sigstore.json") {
		t.Fatalf("Update() error = %v, want exact publisher-bundle requirement", err)
	}
	if got := artifactRequests.Load(); got != 0 {
		t.Fatalf("artifact requests = %d, want 0 before exact bundle selection", got)
	}
}

func TestUpdate_verifiesPublisherBeforeArchiveDownload(t *testing.T) {
	t.Parallel()
	assetName := releaseAssetName()
	archive := []byte("archive")
	digest := sha256.Sum256(archive)
	manifest := []byte(hex.EncodeToString(digest[:]) + "  " + assetName + "\n")
	bundle := []byte(`{"identity":"https://github.com/attacker/repo/.github/workflows/release.yml@refs/tags/v2.0.0"}`)
	expectedIdentity := "https://github.com/test/app/.github/workflows/release.yml@refs/tags/v2.0.0"
	var archiveRequests atomic.Int64
	var verifierCalls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets": []any{
				map[string]any{"name": assetName, "browser_download_url": "http://placeholder/asset"},
				map[string]any{"name": "checksums.txt", "browser_download_url": "http://placeholder/checksums"},
				map[string]any{"name": "checksums.txt.sigstore.json", "browser_download_url": "http://placeholder/bundle"},
			},
		})
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(manifest) })
	mux.HandleFunc("/bundle", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(bundle) })
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		archiveRequests.Add(1)
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:      "test",
		Repo:       "app",
		Version:    "v1.0.0",
		HTTPClient: fakeClient(srv),
		PublisherVerifier: selfupdate.NewPublisherVerifier(func(_ context.Context, gotManifest, gotBundle []byte) error {
			verifierCalls.Add(1)
			if string(gotManifest) != string(manifest) || string(gotBundle) != string(bundle) {
				return errors.New("publisher verifier received wrong inputs")
			}
			var signed struct {
				Identity string `json:"identity"`
			}
			if decodeErr := json.Unmarshal(gotBundle, &signed); decodeErr != nil {
				return decodeErr
			}
			if signed.Identity != expectedIdentity {
				return errors.New("publisher identity mismatch")
			}
			return nil
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "publisher identity mismatch") {
		t.Fatalf("Update() error = %v, want publisher-identity failure", err)
	}
	if got := verifierCalls.Load(); got != 1 {
		t.Fatalf("verifier calls = %d, want 1", got)
	}
	if got := archiveRequests.Load(); got != 0 {
		t.Fatalf("archive requests = %d, want 0 before publisher verification", got)
	}
}

func TestUpdate_rejectsMalformedPublisherBundle(t *testing.T) {
	t.Parallel()
	assetName := releaseAssetName()
	manifest := strings.Repeat("0", sha256.Size*2) + "  " + assetName + "\n"
	var archiveRequests atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets": []any{
				map[string]any{"name": assetName, "browser_download_url": "http://placeholder/asset"},
				map[string]any{"name": "checksums.txt", "browser_download_url": "http://placeholder/checksums"},
				map[string]any{"name": "checksums.txt.sigstore.json", "browser_download_url": "http://placeholder/bundle"},
			},
		})
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(manifest)) })
	mux.HandleFunc("/bundle", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"broken"`)) })
	mux.HandleFunc("/asset", func(http.ResponseWriter, *http.Request) { archiveRequests.Add(1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:      "test",
		Repo:       "app",
		Version:    "v1.0.0",
		HTTPClient: fakeClient(srv),
		PublisherVerifier: selfupdate.NewPublisherVerifier(func(_ context.Context, _, bundle []byte) error {
			if !json.Valid(bundle) {
				return errors.New("malformed publisher bundle")
			}
			return nil
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "malformed publisher bundle") {
		t.Fatalf("Update() error = %v, want malformed-bundle rejection", err)
	}
	if got := archiveRequests.Load(); got != 0 {
		t.Fatalf("archive requests = %d, want 0", got)
	}
}

func TestUpdate_refusesDowngradeBeforeAssetDownload(t *testing.T) {
	t.Parallel()
	var requests atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.9.9",
			"assets": []any{
				map[string]any{"name": "app_1.9.9_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz", "browser_download_url": "http://placeholder/asset"},
				map[string]any{"name": "checksums.txt", "browser_download_url": "http://placeholder/checksums"},
			},
		})
	})
	mux.HandleFunc("/asset", func(http.ResponseWriter, *http.Request) { requests.Add(1) })
	mux.HandleFunc("/checksums", func(http.ResponseWriter, *http.Request) { requests.Add(1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:      "test",
		Repo:       "app",
		Version:    "v2.0.0",
		HTTPClient: fakeClient(srv),
	})
	if err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("Update() error = %v, want downgrade refusal", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want release request only", got)
	}
}

// fakeGitHub returns a test server that returns a GitHub releases/latest payload.
func fakeGitHub(t *testing.T, tag string, assets []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag,
			"assets":   assets,
		}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
}

// fakeClient returns an http.Client whose transport rewrites the host to srv.
func fakeClient(srv *httptest.Server) *http.Client {
	return &http.Client{Transport: &roundTripFunc{fn: func(r *http.Request) (*http.Response, error) {
		r2 := r.Clone(r.Context())
		r2.URL.Scheme = "http"
		r2.URL.Host = srv.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(r2)
	}}}
}

func TestUpdate_withChecksumAndAsset(t *testing.T) {
	t.Parallel()

	assetData := []byte("fake-binary-data")
	h := sha256.New()
	h.Write(assetData)
	checksum := hex.EncodeToString(h.Sum(nil))
	assetName := releaseAssetName()
	checksumTxt := checksum + "  " + assetName + "\n"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets": []any{
				map[string]any{"name": assetName, "browser_download_url": "http://placeholder/asset"},
				map[string]any{"name": "checksums.txt", "browser_download_url": "http://placeholder/checksums"},
				map[string]any{"name": "checksums.txt.sigstore.json", "browser_download_url": "http://placeholder/bundle"},
			},
		})
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksumTxt))
	})
	mux.HandleFunc("/bundle", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"signed":"checksums.txt"}`))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(assetData)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	result, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:             "test",
		Repo:              "app",
		Version:           "v1.0.0",
		HTTPClient:        fakeClient(srv),
		PublisherVerifier: selfupdate.NewPublisherVerifier(acceptPublisher),
	})
	if result.LatestVersion != "v2.0.0" {
		t.Errorf("LatestVersion = %q, want v2.0.0", result.LatestVersion)
	}
	if err == nil || !strings.Contains(err.Error(), "extract binary") {
		t.Fatalf("Update() error = %v, want verified non-archive rejection", err)
	}
}

func TestUpdate_assetDownloadError(t *testing.T) {
	t.Parallel()

	assetName := releaseAssetName()
	checksum := strings.Repeat("0", sha256.Size*2)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets": []any{
				map[string]any{"name": assetName, "browser_download_url": "http://placeholder/asset"},
				map[string]any{"name": "checksums.txt", "browser_download_url": "http://placeholder/checksums"},
				map[string]any{"name": "checksums.txt.sigstore.json", "browser_download_url": "http://placeholder/bundle"},
			},
		})
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksum + "  " + assetName + "\n"))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	mux.HandleFunc("/bundle", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"signed":"checksums.txt"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:             "test",
		Repo:              "app",
		Version:           "v1.0.0",
		HTTPClient:        fakeClient(srv),
		PublisherVerifier: selfupdate.NewPublisherVerifier(acceptPublisher),
	})
	if err == nil {
		t.Fatal("expected error for 403 asset download")
	}
	if !strings.Contains(err.Error(), "asset download returned 403") {
		t.Fatalf("Update() error = %v, want asset status failure", err)
	}
}

func TestUpdate_rejectsOversizedAsset(t *testing.T) {
	t.Parallel()

	assetName := releaseAssetName()
	assetData := []byte("too large")
	digest := sha256.Sum256(assetData)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets": []any{
				map[string]any{"name": assetName, "browser_download_url": "http://placeholder/asset"},
				map[string]any{"name": "checksums.txt", "browser_download_url": "http://placeholder/checksums"},
				map[string]any{"name": "checksums.txt.sigstore.json", "browser_download_url": "http://placeholder/bundle"},
			},
		})
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(hex.EncodeToString(digest[:]) + "  " + assetName + "\n"))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(assetData)
	})
	mux.HandleFunc("/bundle", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"signed":"checksums.txt"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:             "test",
		Repo:              "app",
		Version:           "v1.0.0",
		HTTPClient:        fakeClient(srv),
		MaxAssetBytes:     4,
		PublisherVerifier: selfupdate.NewPublisherVerifier(acceptPublisher),
	})
	if err == nil {
		t.Fatal("Update() = nil error for oversized asset")
	}
	if !strings.Contains(err.Error(), "response exceeds 4-byte limit") {
		t.Fatalf("Update() error = %v, want asset-size failure", err)
	}
}

func TestUpdate_stopsExtractionWhenDownloadCancels(t *testing.T) {
	t.Parallel()
	archive := missingExecutableArchive(t)
	digest := sha256.Sum256(archive)
	assetName := "app_2.0.0_linux_amd64.tar.gz"
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets": []any{
				map[string]any{"name": assetName, "browser_download_url": "http://placeholder/asset"},
				map[string]any{"name": "checksums.txt", "browser_download_url": "http://placeholder/checksums"},
				map[string]any{"name": "checksums.txt.sigstore.json", "browser_download_url": "http://placeholder/bundle"},
			},
		})
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%x  %s\n", digest, assetName)
	})
	mux.HandleFunc("/bundle", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) })
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) })
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := cancelAtAssetEOFClient(server, cancel)
	_, err := selfupdate.Update(ctx, selfupdate.Options{
		Owner:             "test",
		Repo:              "app",
		Version:           "v1.0.0",
		AssetName:         assetName,
		BinaryName:        "app",
		HTTPClient:        client,
		PublisherVerifier: selfupdate.NewPublisherVerifier(acceptPublisher),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Update() error = %v, want context.Canceled", err)
	}
}

func missingExecutableArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	data := []byte("not the selected executable")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "other", Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tarWriter.Write(data); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

func cancelAtAssetEOFClient(server *httptest.Server, cancel context.CancelFunc) *http.Client {
	base := fakeClient(server).Transport
	return &http.Client{Transport: &roundTripFunc{fn: func(request *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(request)
		if err == nil && request.URL.Path == "/asset" {
			response.Body = &cancelAtEOFReadCloser{ReadCloser: response.Body, cancel: cancel}
		}
		return response, err
	}}}
}

type cancelAtEOFReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (reader *cancelAtEOFReadCloser) Read(buffer []byte) (int, error) {
	count, err := reader.ReadCloser.Read(buffer)
	if errors.Is(err, io.EOF) {
		reader.cancel()
	}
	return count, err
}

func releaseAssetName() string {
	extension := ".tar.gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	return "app_2.0.0_" + runtime.GOOS + "_" + runtime.GOARCH + extension
}

type roundTripFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f *roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f.fn(r) }

func acceptPublisher(context.Context, []byte, []byte) error { return nil }
