// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package selfupdate

import "testing"

func TestFindChecksumAssetURL_underscoreSuffix(t *testing.T) {
	t.Parallel()
	assets := []ghAsset{
		{Name: "app_linux_amd64", BrowserDownloadURL: "http://x/app"},
		{Name: "app_checksums.txt", BrowserDownloadURL: "http://x/checksums"},
	}
	if got := findChecksumAssetURL(assets); got != "http://x/checksums" {
		t.Errorf("findChecksumAssetURL() = %q, want %q", got, "http://x/checksums")
	}
}

func TestFindChecksumAssetURL_plainSuffix(t *testing.T) {
	t.Parallel()
	assets := []ghAsset{
		{Name: "checksums.txt", BrowserDownloadURL: "http://x/plain-checksums"},
	}
	if got := findChecksumAssetURL(assets); got != "http://x/plain-checksums" {
		t.Errorf("findChecksumAssetURL() = %q, want %q", got, "http://x/plain-checksums")
	}
}

func TestFindChecksumAssetURL_noMatch(t *testing.T) {
	t.Parallel()
	assets := []ghAsset{
		{Name: "app_linux_amd64", BrowserDownloadURL: "http://x/app"},
	}
	if got := findChecksumAssetURL(assets); got != "" {
		t.Errorf("findChecksumAssetURL() = %q, want empty", got)
	}
}

func TestFindChecksumAssetURL_emptyAssets(t *testing.T) {
	t.Parallel()
	if got := findChecksumAssetURL(nil); got != "" {
		t.Errorf("findChecksumAssetURL(nil) = %q, want empty", got)
	}
}

func TestParseChecksum_matches(t *testing.T) {
	t.Parallel()
	data := []byte("deadbeef  app_linux_amd64\ncafef00d  app_darwin_arm64\n")
	if got := parseChecksum(data, "app_darwin_arm64"); got != "cafef00d" {
		t.Errorf("parseChecksum() = %q, want %q", got, "cafef00d")
	}
}

func TestParseChecksum_caseInsensitiveFilename(t *testing.T) {
	t.Parallel()
	data := []byte("deadbeef  App_Linux_AMD64\n")
	if got := parseChecksum(data, "app_linux_amd64"); got != "deadbeef" {
		t.Errorf("parseChecksum() = %q, want %q", got, "deadbeef")
	}
}

func TestParseChecksum_noMatch(t *testing.T) {
	t.Parallel()
	data := []byte("deadbeef  app_linux_amd64\n")
	if got := parseChecksum(data, "app_windows_amd64.exe"); got != "" {
		t.Errorf("parseChecksum() = %q, want empty", got)
	}
}

func TestParseChecksum_malformedLineSkipped(t *testing.T) {
	t.Parallel()
	// A line with the wrong field count (e.g. a stray comment) must be
	// skipped rather than matched or causing a panic.
	data := []byte("this line has three fields\ndeadbeef  app_linux_amd64\n")
	if got := parseChecksum(data, "app_linux_amd64"); got != "deadbeef" {
		t.Errorf("parseChecksum() = %q, want %q", got, "deadbeef")
	}
}

func TestParseChecksum_emptyData(t *testing.T) {
	t.Parallel()
	if got := parseChecksum(nil, "app_linux_amd64"); got != "" {
		t.Errorf("parseChecksum(nil) = %q, want empty", got)
	}
}
