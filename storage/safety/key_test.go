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
		{"dot-segment normalized", "a/./b.txt", "a/b.txt", false},
		{"redundant slash", "a//b.txt", "a/b.txt", false},
		{"trailing slash normalized", "a/b/", "a/b", false},
		{"leading dot file ok", ".hidden", ".hidden", false},

		{"parent traversal", "../etc/passwd", "", true},
		{"embedded traversal", "a/../../b", "", true},
		{"traversal resolves up", "a/../../etc", "", true},
		{"absolute", "/etc/passwd", "", true},
		{"unc backslash", `\\unc\share`, "", true},
		{"windows drive backslash", `C:\x`, "", true},
		{"trailing space rejected", "key ", "", true},
		{"trailing dot rejected", "file.", "", true},
		{"null byte", "a\x00b", "", true},
		{"control char", "a\tb", "", true},
		{"newline", "a\nb", "", true},
		{"win reserved CON", "CON", "", true},
		{"win reserved nul with ext", "dir/NUL.txt", "", true},
		{"win reserved lower com1", "com1", "", true},
		{"empty", "", "", true},
		{"dot only", ".", "", true},
		{"dotdot only", "..", "", true},
		{"del char", "a\x7fb", "", true},
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
			for _, seg := range strings.Split(got, "/") {
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
		"a/./b", "a//b", "..", ".", "", "a/../../b", "key ",
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
		for _, seg := range strings.Split(got, "/") {
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
	})
}
