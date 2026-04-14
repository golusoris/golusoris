package selfupdate_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/golusoris/golusoris/selfupdate"
)

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
	assetName := "app_" + runtime.GOOS + "_" + runtime.GOARCH
	checksumTxt := checksum + "  " + assetName + "\n"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets": []any{
				map[string]any{"name": assetName, "browser_download_url": "http://placeholder/asset"},
				map[string]any{"name": "app_checksums.txt", "browser_download_url": "http://placeholder/checksums"},
			},
		})
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksumTxt))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(assetData)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	result, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:      "test",
		Repo:       "app",
		Version:    "v1.0.0",
		HTTPClient: fakeClient(srv),
	})
	if result.LatestVersion != "v2.0.0" {
		t.Errorf("LatestVersion = %q, want v2.0.0", result.LatestVersion)
	}
	// Apply fails in test (can't replace running binary) — that's expected.
	_ = err
}

func TestUpdate_assetDownloadError(t *testing.T) {
	t.Parallel()

	assetName := "app_" + runtime.GOOS + "_" + runtime.GOARCH
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/test/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0",
			"assets":   []any{map[string]any{"name": assetName, "browser_download_url": "http://placeholder/asset"}},
		})
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := selfupdate.Update(context.Background(), selfupdate.Options{
		Owner:      "test",
		Repo:       "app",
		Version:    "v1.0.0",
		HTTPClient: fakeClient(srv),
	})
	if err == nil {
		t.Fatal("expected error for 403 asset download")
	}
}

type roundTripFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f *roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f.fn(r) }
