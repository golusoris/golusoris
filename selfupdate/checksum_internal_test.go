// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	minioselfupdate "github.com/minio/selfupdate"
)

func TestFindChecksumAsset_exactManifest(t *testing.T) {
	t.Parallel()
	assets := []ghAsset{
		{Name: "app_checksums.txt", BrowserDownloadURL: "http://x/wrong"},
		{Name: checksumAssetName, BrowserDownloadURL: "http://x/checksums"},
	}
	got, err := findChecksumAsset(assets)
	if err != nil {
		t.Fatalf("findChecksumAsset(): %v", err)
	}
	if got.BrowserDownloadURL != "http://x/checksums" {
		t.Errorf("checksum URL = %q, want %q", got.BrowserDownloadURL, "http://x/checksums")
	}
}

func TestFindChecksumAsset_missingOrDuplicate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		assets []ghAsset
	}{
		{name: "missing", assets: []ghAsset{{Name: "app_checksums.txt"}}},
		{name: "duplicate", assets: []ghAsset{{Name: checksumAssetName}, {Name: checksumAssetName}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := findChecksumAsset(tc.assets); err == nil {
				t.Fatal("findChecksumAsset() = nil error")
			}
		})
	}
}

func TestFindPublisherBundle_exactOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		assets []ghAsset
	}{
		{name: "missing", assets: []ghAsset{{Name: checksumAssetName}}},
		{name: "near match", assets: []ghAsset{{Name: checksumBundleName + ".extra"}}},
		{name: "duplicate", assets: []ghAsset{{Name: checksumBundleName}, {Name: checksumBundleName}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := findPublisherBundle(tc.assets); err == nil {
				t.Fatal("findPublisherBundle() = nil error")
			}
		})
	}
}

func TestFetchChecksum_publisherBundleBoundary(t *testing.T) {
	t.Parallel()
	asset := ghAsset{Name: "app.tar.gz"}
	manifest := strings.Repeat("0", sha256.Size*2) + "  " + asset.Name + "\n"
	tests := []struct {
		name          string
		bundleBytes   int
		wantVerify    int64
		wantSizeError bool
	}{
		{name: "exact limit", bundleBytes: maxPublisherBundleBytes, wantVerify: 1},
		{name: "over limit", bundleBytes: maxPublisherBundleBytes + 1, wantSizeError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var verifyCalls atomic.Int64
			mux := http.NewServeMux()
			mux.HandleFunc("/checksums", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(manifest)) })
			mux.HandleFunc("/bundle", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, strings.Repeat("x", tc.bundleBytes))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			rel := ghRelease{Assets: []ghAsset{
				{Name: checksumAssetName, BrowserDownloadURL: srv.URL + "/checksums"},
				{Name: checksumBundleName, BrowserDownloadURL: srv.URL + "/bundle"},
			}}
			_, err := fetchChecksum(context.Background(), srv.Client(), rel, asset, NewPublisherVerifier(func(
				context.Context, []byte, []byte,
			) error {
				verifyCalls.Add(1)
				return nil
			}))
			if tc.wantSizeError && (err == nil || !strings.Contains(err.Error(), "response exceeds")) {
				t.Fatalf("fetchChecksum() error = %v, want size error", err)
			}
			if !tc.wantSizeError && err != nil {
				t.Fatalf("fetchChecksum(): %v", err)
			}
			if got := verifyCalls.Load(); got != tc.wantVerify {
				t.Fatalf("verifier calls = %d, want %d", got, tc.wantVerify)
			}
		})
	}
}

func TestFetchChecksum_requiresOKStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	rel := ghRelease{Assets: []ghAsset{
		{Name: checksumAssetName, BrowserDownloadURL: srv.URL},
		{Name: checksumBundleName, BrowserDownloadURL: srv.URL},
	}}
	asset := ghAsset{Name: "app_1.2.3_linux_amd64.tar.gz"}
	if _, err := fetchChecksum(context.Background(), srv.Client(), rel, asset, NewPublisherVerifier(acceptPublisher)); err == nil {
		t.Fatal("fetchChecksum() = nil error for non-200 response")
	}
}

func TestFetchChecksum_requiresManifest(t *testing.T) {
	t.Parallel()
	if _, err := fetchChecksum(
		context.Background(),
		&http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()},
		ghRelease{},
		ghAsset{Name: "app.tar.gz"},
		NewPublisherVerifier(acceptPublisher),
	); err == nil {
		t.Fatal("fetchChecksum() = nil error without manifest")
	}
}

func TestFetchChecksum_verifiesBeforeParsingManifest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checksums" {
			_, _ = io.WriteString(w, "malformed manifest")
			return
		}
		_, _ = io.WriteString(w, `{"bundle":"untrusted"}`)
	}))
	defer srv.Close()
	rel := ghRelease{Assets: []ghAsset{
		{Name: checksumAssetName, BrowserDownloadURL: srv.URL + "/checksums"},
		{Name: checksumBundleName, BrowserDownloadURL: srv.URL + "/bundle"},
	}}
	_, err := fetchChecksum(
		context.Background(),
		srv.Client(),
		rel,
		ghAsset{Name: "app.tar.gz"},
		NewPublisherVerifier(func(context.Context, []byte, []byte) error { return errors.New("untrusted publisher") }),
	)
	if err == nil || !strings.Contains(err.Error(), "untrusted publisher") {
		t.Fatalf("fetchChecksum() error = %v, want publisher rejection before parse", err)
	}
}

func acceptPublisher(context.Context, []byte, []byte) error { return nil }

func TestDownloadAsset_requiresMatchingSHA256(t *testing.T) {
	t.Parallel()
	data := []byte("verified archive")
	digest := sha256.Sum256(data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	got, err := downloadAsset(context.Background(), srv.Client(), srv.URL, digest[:], int64(len(data)))
	if err != nil {
		t.Fatalf("downloadAsset(): %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("archive = %q, want %q", got, data)
	}

	wrong := sha256.Sum256([]byte("wrong"))
	if _, err := downloadAsset(context.Background(), srv.Client(), srv.URL, wrong[:], int64(len(data))); err == nil {
		t.Fatal("downloadAsset() = nil error for checksum mismatch")
	}
	if _, err := downloadAsset(context.Background(), srv.Client(), srv.URL, nil, int64(len(data))); err == nil {
		t.Fatal("downloadAsset() = nil error without checksum")
	}
}

func TestParseChecksum_exactSHA256(t *testing.T) {
	t.Parallel()
	want := strings.Repeat("ab", sha256.Size)
	got, err := parseChecksum([]byte(want+"  app.tar.gz\n"), "app.tar.gz")
	if err != nil {
		t.Fatalf("parseChecksum(): %v", err)
	}
	if hex.EncodeToString(got) != want {
		t.Errorf("checksum = %x, want %s", got, want)
	}
}

func TestParseChecksum_rejectsInvalidOrInexactEntry(t *testing.T) {
	t.Parallel()
	valid := strings.Repeat("ab", sha256.Size)
	tests := []struct {
		name string
		data string
	}{
		{name: "short hash", data: "deadbeef  app.tar.gz\n"},
		{name: "non-hex hash", data: strings.Repeat("z", sha256.Size*2) + "  app.tar.gz\n"},
		{name: "wrong case", data: valid + "  App.tar.gz\n"},
		{name: "suffix match", data: valid + "  app.tar.gz.sbom.json\n"},
		{name: "missing", data: valid + "  other.tar.gz\n"},
		{name: "duplicate", data: valid + "  app.tar.gz\n" + valid + "  app.tar.gz\n"},
		{name: "malformed manifest", data: "not-a-checksum-line\n" + valid + "  app.tar.gz\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseChecksum([]byte(tc.data), "app.tar.gz"); err == nil {
				t.Fatal("parseChecksum() = nil error")
			}
		})
	}
}

func TestExtractTarGzBinary_exactMemberAndBoundary(t *testing.T) {
	t.Parallel()
	want := []byte("binary")
	archive := makeTarGz(t, []archiveEntry{
		{name: "README.md", data: []byte("docs"), mode: 0o644},
		{name: "app", data: want, mode: 0o755},
	})
	got, err := extractBinary(context.Background(), archive, "app_1.2.3_linux_amd64.tar.gz", "app", int64(len(want)))
	if err != nil {
		t.Fatalf("extractBinary(): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("binary = %q, want %q", got, want)
	}
}

func TestExtractBinary_rejectsUnsafeTarMembers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		entries []archiveEntry
		limit   int64
	}{
		{name: "nested", entries: []archiveEntry{{name: "bin/app", data: []byte("binary"), mode: 0o755}}, limit: 32},
		{name: "not executable", entries: []archiveEntry{{name: "app", data: []byte("binary"), mode: 0o644}}, limit: 32},
		{name: "duplicate", entries: []archiveEntry{{name: "app", data: []byte("one"), mode: 0o755}, {name: "app", data: []byte("two"), mode: 0o755}}, limit: 32},
		{name: "oversized", entries: []archiveEntry{{name: "app", data: []byte("binary"), mode: 0o755}}, limit: 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			archive := makeTarGz(t, tc.entries)
			if _, err := extractBinary(context.Background(), archive, "app_1.2.3_linux_amd64.tar.gz", "app", tc.limit); err == nil {
				t.Fatal("extractBinary() = nil error")
			}
		})
	}
}

func TestExtractZipBinary_exactMember(t *testing.T) {
	t.Parallel()
	want := []byte("windows-binary")
	archive := makeZip(t, []archiveEntry{{name: "app.exe", data: want, mode: 0o755}})
	got, err := extractBinary(context.Background(), archive, "app_1.2.3_windows_amd64.zip", "app.exe", 32)
	if err != nil {
		t.Fatalf("extractBinary(): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("binary = %q, want %q", got, want)
	}
}

func TestExtractBinary_rejectsUnsupportedArchive(t *testing.T) {
	t.Parallel()
	if _, err := extractBinary(context.Background(), []byte("binary"), "app.bin", "app", 32); err == nil {
		t.Fatal("extractBinary() = nil error for unsupported archive")
	}
}

func TestExtractBinary_rejectsCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, archiveName := range []string{"app.tar.gz", "app.zip"} {
		_, err := extractBinary(ctx, []byte("archive"), archiveName, "app", 32)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("extractBinary(%q) error = %v, want context.Canceled", archiveName, err)
		}
	}
}

func TestApplyBinary_checksContextBeforeReplacement(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := applyBinary(ctx, []byte("binary"), func(io.Reader, minioselfupdate.Options) error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("applyBinary() error = %v, called = %t", err, called)
	}
}

func TestApplyBinary_contextCheckedReader(t *testing.T) {
	t.Parallel()
	t.Run("complete", func(t *testing.T) {
		t.Parallel()
		got := ""
		err := applyBinary(context.Background(), []byte("binary"), func(
			reader io.Reader,
			_ minioselfupdate.Options,
		) error {
			data, readErr := io.ReadAll(reader)
			got = string(data)
			return readErr
		})
		if err != nil || got != "binary" {
			t.Fatalf("applyBinary() error = %v, data = %q", err, got)
		}
	})
	t.Run("cancel between reads", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		err := applyBinary(ctx, []byte("binary"), func(reader io.Reader, _ minioselfupdate.Options) error {
			buffer := make([]byte, 1)
			if _, readErr := reader.Read(buffer); readErr != nil {
				return readErr
			}
			cancel()
			_, readErr := reader.Read(buffer)
			return readErr
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("applyBinary() error = %v, want context.Canceled", err)
		}
	})
}

func TestApplyBinary_surfacesRollbackFailure(t *testing.T) {
	t.Parallel()
	applyErr := errors.New("replacement failed")
	rollbackErr := errors.New("rollback failed")
	err := applyBinaryWithRollback(
		context.Background(),
		[]byte("binary"),
		func(io.Reader, minioselfupdate.Options) error { return applyErr },
		func(got error) error {
			if !errors.Is(got, applyErr) {
				t.Fatalf("rollback inspector input = %v; want apply error", got)
			}
			return rollbackErr
		},
	)
	if !errors.Is(err, applyErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("applyBinaryWithRollback() error = %v; want apply and rollback failures", err)
	}
}

func TestSelectedBinaryName_overrideValidation(t *testing.T) {
	t.Parallel()
	got, err := selectedBinaryName(Options{BinaryName: "app"})
	if err != nil {
		t.Fatalf("selectedBinaryName(): %v", err)
	}
	if runtime.GOOS == "windows" {
		if got != "app.exe" {
			t.Fatalf("binary name = %q, want app.exe", got)
		}
	} else if got != "app" {
		t.Fatalf("binary name = %q, want app", got)
	}
	if _, err := selectedBinaryName(Options{BinaryName: "../app"}); err == nil {
		t.Fatal("selectedBinaryName() = nil error for path")
	}
}

func TestReadRemoteBodyBoundary(t *testing.T) {
	t.Parallel()
	data, err := readRemoteBody(strings.NewReader("1234"), 4)
	if err != nil {
		t.Fatalf("readRemoteBody(exact limit): %v", err)
	}
	if got := string(data); got != "1234" {
		t.Fatalf("body = %q, want %q", got, "1234")
	}

	if _, err := readRemoteBody(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("readRemoteBody(over limit) = nil error")
	}
}

type archiveEntry struct {
	name string
	data []byte
	mode int64
}

func makeTarGz(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data))}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			t.Fatalf("write tar entry: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(0o755)
		writer, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := writer.Write(entry.data); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
