// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package fileuri

import (
	"net/url"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFromAbsoluteRendersRFC8089URIs(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, path, want string
		separator        rune
	}{
		{name: "unix", path: "/tmp/a b/x#1?.jsonl", separator: '/', want: "file:///tmp/a%20b/x%231%3F.jsonl"},
		{name: "unix root", path: "/", separator: '/', want: "file:///"},
		{name: "windows drive", path: `C:\Users\a b\x#1%.jsonl`, separator: '\\', want: "file:///C:/Users/a%20b/x%231%25.jsonl"},
		{name: "windows drive root", path: `D:\`, separator: '\\', want: "file:///D:/"},
		{name: "windows forward slashes", path: "C:/data/x", separator: '\\', want: "file:///C:/data/x"},
		{name: "windows UNC", path: `\\server\share\x`, separator: '\\', want: "file:////server/share/x"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := fromAbsolute(tt.path, tt.separator); got != tt.want {
				t.Fatalf("fromAbsolute(%q) = %q; want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestToLocalUndoesFromAbsolute(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, path string
		separator  rune
	}{
		{name: "unix", path: "/tmp/a b/x#1?.jsonl", separator: '/'},
		{name: "unix drive-like segment", path: "/C:/x", separator: '/'},
		{name: "windows drive", path: `C:\Users\a b\x#1%.jsonl`, separator: '\\'},
		{name: "windows lowercase drive", path: `z:\x`, separator: '\\'},
		{name: "windows UNC", path: `\\server\share\x`, separator: '\\'},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := url.Parse(fromAbsolute(tt.path, tt.separator))
			if err != nil {
				t.Fatalf("parse rendered URI: %v", err)
			}
			if parsed.Host != "" {
				t.Fatalf("rendered URI host = %q; want empty", parsed.Host)
			}
			if got := toLocal(parsed.Path, tt.separator); got != tt.path {
				t.Fatalf("toLocal(%q) = %q; want %q", parsed.Path, got, tt.path)
			}
		})
	}
}

func TestToLocalStripsOnlyTheDriveSlashOnWindows(t *testing.T) {
	t.Parallel()
	for uriPath, want := range map[string]string{
		"/c:":     "c:",
		"/1:/x":   `\1:\x`,
		"/tmp/x":  `\tmp\x`,
		"/CD:/x":  `\CD:\x`,
		"/":       `\`,
		"":        "",
		"/C:/a/b": `C:\a\b`,
	} {
		if got := toLocal(uriPath, '\\'); got != want {
			t.Errorf("toLocal(%q, windows) = %q; want %q", uriPath, got, want)
		}
	}
}

func TestFromPathRoundTripsOnHost(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a b", "x#1%.db")
	rendered, err := FromPath(path)
	if err != nil {
		t.Fatalf("FromPath: %v", err)
	}
	parsed, err := url.ParseRequestURI(rendered)
	if err != nil {
		t.Fatalf("ParseRequestURI(%q): %v", rendered, err)
	}
	if parsed.Scheme != "file" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("FromPath(%q) = %q; want host-less file URI without query or fragment", path, rendered)
	}
	if got := ToPath(parsed); got != path {
		t.Fatalf("ToPath(%q) = %q; want %q", rendered, got, path)
	}
}

func TestFromPathRejectsRelativePaths(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"", "data.jsonl", filepath.Join("..", "data.jsonl")} {
		if rendered, err := FromPath(path); err == nil {
			t.Errorf("FromPath(%q) = %q; want error", path, rendered)
		}
	}
}

func TestToPathLeavesDrivelessRootRelativeOnWindows(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("drive-relative roots exist only on Windows")
	}
	got := ToPath(&url.URL{Scheme: "file", Path: "/tmp/x"})
	if filepath.IsAbs(got) {
		t.Fatalf("ToPath(file:///tmp/x) = %q; want a non-absolute Windows path", got)
	}
}
