// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package selfupdate

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestDecodeReleaseBody_boundsAndRejectsTrailingData(t *testing.T) {
	t.Parallel()
	valid := `{"tag_name":"v1.2.3","assets":[]}`
	for _, tc := range []struct {
		name string
		body io.Reader
	}{
		{name: "oversized", body: strings.NewReader(strings.Repeat(" ", maxReleaseMetadataBytes+1))},
		{name: "trailing json", body: strings.NewReader(valid + ` {}`)},
		{name: "trailing garbage", body: strings.NewReader(valid + ` x`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeReleaseBody(tc.body); err == nil {
				t.Fatal("decodeReleaseBody() = nil error")
			}
		})
	}
}

func TestExtractTarGzBinary_acceptsExactEntryLimit(t *testing.T) {
	t.Parallel()
	entries := make([]archiveEntry, 0, maxArchiveEntries+1)
	for index := range maxArchiveEntries - 1 {
		entries = append(entries, archiveEntry{
			name: fmt.Sprintf("notice-%03d.txt", index),
			data: []byte("x"),
			mode: 0o644,
		})
	}
	entries = append(entries, archiveEntry{name: "app", data: []byte("binary"), mode: 0o755})
	archive := makeTarGz(t, entries)
	got, err := extractTarGzBinary(context.Background(), archive, "app", 32)
	if err != nil {
		t.Fatalf("extractTarGzBinary(exact limit): %v", err)
	}
	if string(got) != "binary" {
		t.Fatalf("binary = %q; want %q", got, "binary")
	}

	entries = append(entries, archiveEntry{name: "extra.txt", data: []byte("x"), mode: 0o644})
	if _, err = extractTarGzBinary(context.Background(), makeTarGz(t, entries), "app", 32); err == nil {
		t.Fatal("extractTarGzBinary(over limit) = nil error")
	}
}

func TestCompareReleaseVersions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		current string
		latest  string
		want    int
	}{
		{name: "upgrade", current: "v1.2.3", latest: "v1.2.4", want: 1},
		{name: "same without prefix", current: "1.2.3", latest: "v1.2.3", want: 0},
		{name: "build metadata same precedence", current: "v1.2.3+installed", latest: "v1.2.3+release", want: 0},
		{name: "downgrade", current: "v2.0.0", latest: "v1.9.9", want: -1},
		{name: "prerelease upgrade", current: "v2.0.0-rc.1", latest: "v2.0.0", want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := compareReleaseVersions(tc.current, tc.latest)
			if err != nil {
				t.Fatalf("compareReleaseVersions(): %v", err)
			}
			if got != tc.want {
				t.Fatalf("comparison = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCompareReleaseVersions_rejectsUnpinnedVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		current string
		latest  string
	}{
		{name: "development build", current: "(devel)", latest: "v1.2.3"},
		{name: "empty installed", current: "", latest: "v1.2.3"},
		{name: "short installed", current: "v1.2", latest: "v1.2.3"},
		{name: "invalid release", current: "v1.2.3", latest: "latest"},
		{name: "uppercase prefix", current: "V1.2.3", latest: "v1.2.4"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := compareReleaseVersions(tc.current, tc.latest); err == nil {
				t.Fatal("compareReleaseVersions() = nil error")
			}
		})
	}
}

func TestSelectAssetForPlatform_exactReleaseContract(t *testing.T) {
	t.Parallel()
	exact := "app_1.2.3_linux_amd64.tar.gz"
	rel := ghRelease{
		TagName: "v1.2.3",
		Assets: []ghAsset{
			{Name: exact + ".sbom.json", BrowserDownloadURL: "https://example.invalid/sbom"},
			{Name: "app_1.2.3_linux_arm64.tar.gz", BrowserDownloadURL: "https://example.invalid/arm"},
			{Name: exact, BrowserDownloadURL: "https://example.invalid/archive"},
		},
	}
	got, err := selectAssetForPlatform(rel, Options{Repo: "app"}, "linux", "amd64")
	if err != nil {
		t.Fatalf("selectAssetForPlatform(): %v", err)
	}
	if got.Name != exact {
		t.Fatalf("asset = %q, want %q", got.Name, exact)
	}
}

func TestSelectAssetForPlatform_windowsZip(t *testing.T) {
	t.Parallel()
	want := "app_1.2.3_windows_arm64.zip"
	rel := ghRelease{TagName: "v1.2.3", Assets: []ghAsset{{Name: want, BrowserDownloadURL: "https://example.invalid/archive"}}}
	got, err := selectAssetForPlatform(rel, Options{Repo: "app"}, "windows", "arm64")
	if err != nil {
		t.Fatalf("selectAssetForPlatform(): %v", err)
	}
	if got.Name != want {
		t.Fatalf("asset = %q, want %q", got.Name, want)
	}
}

func TestSelectAssetForPlatform_rejectsNearOrDuplicateMatch(t *testing.T) {
	t.Parallel()
	exact := "app_1.2.3_linux_amd64.tar.gz"
	tests := []struct {
		name   string
		assets []ghAsset
	}{
		{name: "legacy prefix only", assets: []ghAsset{{Name: "app_linux_amd64.tar.gz"}}},
		{name: "sbom suffix only", assets: []ghAsset{{Name: exact + ".sbom.json"}}},
		{name: "case mismatch", assets: []ghAsset{{Name: strings.ToUpper(exact)}}},
		{name: "duplicate", assets: []ghAsset{{Name: exact}, {Name: exact}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rel := ghRelease{TagName: "v1.2.3", Assets: tc.assets}
			if _, err := selectAssetForPlatform(rel, Options{Repo: "app"}, "linux", "amd64"); err == nil {
				t.Fatal("selectAssetForPlatform() = nil error")
			}
		})
	}
}

func TestSelectAssetForPlatform_exactOverride(t *testing.T) {
	t.Parallel()
	rel := ghRelease{TagName: "v1.2.3", Assets: []ghAsset{
		{Name: "custom.tar.gz.sigstore.json"},
		{Name: "custom.tar.gz", BrowserDownloadURL: "https://example.invalid/custom"},
	}}
	got, err := selectAssetForPlatform(rel, Options{Repo: "app", AssetName: "custom.tar.gz"}, "linux", "amd64")
	if err != nil {
		t.Fatalf("selectAssetForPlatform(): %v", err)
	}
	if got.Name != "custom.tar.gz" {
		t.Fatalf("asset = %q", got.Name)
	}
}
