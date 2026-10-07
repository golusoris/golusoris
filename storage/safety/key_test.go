// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package safety_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/storage/safety"
)

func TestCleanKey(t *testing.T) {
	t.Parallel()
	const maxLen = 1024
	tests := []struct {
		name    string
		key     string
		want    string
		wantErr bool
	}{
		{"simple", "avatars/u-42.png", "avatars/u-42.png", false},
		{"nested", "a/b/c/d.txt", "a/b/c/d.txt", false},
		{"dots inside segment ok", "tenant.v1/file..name", "tenant.v1/file..name", false},
		{"leading dot file ok", ".hidden", ".hidden", false},

		{"dot segment", "a/./b.txt", "", true},
		{"redundant slash", "a//b.txt", "", true},
		{"trailing slash", "a/b/", "", true},
		{"parent traversal", "../etc/passwd", "", true},
		{"namespace traversal", "tenant-a/../tenant-b/object", "", true},
		{"embedded traversal", "a/../../b", "", true},
		{"traversal resolves up", "a/../../etc", "", true},
		{"absolute", "/etc/passwd", "", true},
		{"unc backslash", `\\unc\share`, "", true},
		{"windows drive backslash", `C:\x`, "", true},
		{"windows ads", "file.txt:secret", "", true},
		{"windows less than", "dir/a<b", "", true},
		{"windows greater than", "dir/a>b", "", true},
		{"windows quote", `dir/a"b`, "", true},
		{"windows pipe", "dir/a|b", "", true},
		{"windows question", "dir/a?b", "", true},
		{"windows star", "dir/a*b", "", true},
		{"trailing space rejected", "key ", "", true},
		{"trailing dot rejected", "file.", "", true},
		{"component trailing space rejected", "dir /file", "", true},
		{"component trailing dot rejected", "dir./file", "", true},
		{"repeated trailing dots rejected", "file..", "", true},
		{"null byte", "a\x00b", "", true},
		{"control char", "a\tb", "", true},
		{"newline", "a\nb", "", true},
		{"win reserved CON", "CON", "", true},
		{"win reserved nul with ext", "dir/NUL.txt", "", true},
		{"win reserved lower com1", "com1", "", true},
		{"win reserved conin", "CONIN$", "", true},
		{"win reserved conout with ext", "dir/conout$.txt", "", true},
		{"win reserved superscript com", "COM¹", "", true},
		{"win reserved superscript lpt with ext", "dir/LPT³.log", "", true},
		{"empty", "", "", true},
		{"dot only", ".", "", true},
		{"dotdot only", "..", "", true},
		{"del char", "a\x7fb", "", true},
		{"reserved staged object", "dir/.golusoris-put-token.tmp", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := safety.CleanKey(tt.key, maxLen)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("CleanKey(%q) = %q, nil; want error", tt.key, got)
				}
				if !errors.Is(err, safety.ErrUnsafeKey) {
					t.Fatalf("CleanKey(%q) error = %v; want ErrUnsafeKey", tt.key, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CleanKey(%q) unexpected error: %v", tt.key, err)
			}
			if got != tt.want {
				t.Fatalf("CleanKey(%q) = %q; want %q", tt.key, got, tt.want)
			}
			if !filepath.IsLocal(got) {
				t.Fatalf("CleanKey(%q) = %q is not local", tt.key, got)
			}
			for seg := range strings.SplitSeq(got, "/") {
				if seg == ".." {
					t.Fatalf("CleanKey(%q) = %q has .. segment", tt.key, got)
				}
			}
		})
	}
}

func TestCleanKey_OverLength(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 50)
	if _, err := safety.CleanKey(long, 10); !errors.Is(err, safety.ErrUnsafeKey) {
		t.Fatalf("over-length key: want ErrUnsafeKey, got %v", err)
	}
	if _, err := safety.CleanKey(long, 0); err != nil {
		t.Fatalf("maxLen=0 disables length check, got %v", err)
	}
}

// TestCleanKey_LengthBoundary is the exact edge of the length check: a key
// whose length equals maxLen must pass, one byte longer must fail.
func TestCleanKey_LengthBoundary(t *testing.T) {
	t.Parallel()
	exact := strings.Repeat("a", 10)
	if _, err := safety.CleanKey(exact, 10); err != nil {
		t.Fatalf("key of length == maxLen: want nil, got %v", err)
	}
	overByOne := strings.Repeat("a", 11)
	if _, err := safety.CleanKey(overByOne, 10); !errors.Is(err, safety.ErrUnsafeKey) {
		t.Fatalf("key of length == maxLen+1: want ErrUnsafeKey, got %v", err)
	}
}

func TestMustBeLocal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"local", "a/b.txt", false},
		{"nested ok", "x/y/z", false},
		{"traversal", "../x", true},
		{"embedded traversal", "a/../b", true},
		{"component trailing dot", "dir./file", true},
		{"component trailing space", "dir /file", true},
		{"windows reserved", "dir/NUL.txt", true},
		{"absolute", "/x", true},
		{"empty", "", true},
		{"null", "a\x00b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := safety.MustBeLocal(tt.key)
			if tt.wantErr && err == nil {
				t.Fatalf("MustBeLocal(%q) = nil; want error", tt.key)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("MustBeLocal(%q) = %v; want nil", tt.key, err)
			}
		})
	}
}

func FuzzCleanKey(f *testing.F) {
	seeds := []string{
		"a/b.txt", "../etc/passwd", "/abs", `\unc`, "a\x00b", "CON",
		"a/./b", "a//b", "..", ".", "", "a/../../b", "tenant-a/../tenant-b/object",
		"key ", "dir./file", "dir /file", "file..",
	}
	for _, s := range seeds {
		f.Add(s, 1024)
	}
	f.Fuzz(func(t *testing.T, key string, maxLen int) {
		got, err := safety.CleanKey(key, maxLen)
		if err != nil {
			return
		}
		if !filepath.IsLocal(got) {
			t.Fatalf("CleanKey(%q) = %q is not local", key, got)
		}
		// No ".." path SEGMENT (a file literally named "..0" is legitimate).
		for seg := range strings.SplitSeq(got, "/") {
			if seg == ".." {
				t.Fatalf("CleanKey(%q) = %q has .. segment", key, got)
			}
		}
		if strings.HasPrefix(got, "/") {
			t.Fatalf("CleanKey(%q) = %q is absolute", key, got)
		}
		if strings.ContainsRune(got, '\x00') {
			t.Fatalf("CleanKey(%q) = %q contains null byte", key, got)
		}
		if got != key {
			t.Fatalf("CleanKey(%q) silently aliases to %q", key, got)
		}
	})
}
