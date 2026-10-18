// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package configfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWrite(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "valid configuration", body: "search:\n  backend: memory\n"},
		{name: "invalid configuration", body: "search: [\n"},
		{name: "empty boundary", body: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := Write(t, tc.body)
			if filepath.Base(path) != "config.yaml" {
				t.Fatalf("path = %q, want config.yaml basename", path)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read config: %v", err)
			}
			if string(got) != tc.body {
				t.Fatalf("body = %q, want %q", got, tc.body)
			}
			if runtime.GOOS == "windows" {
				t.Skip("Windows does not expose Unix permission bits")
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat config: %v", err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("mode = %04o, want 0600", got)
			}
		})
	}
}

func TestWriteIsolatesFiles(t *testing.T) {
	t.Parallel()
	first := Write(t, "first")
	second := Write(t, "second")
	if first == second {
		t.Fatalf("Write paths are equal: %q", first)
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{path: first, want: "first"},
		{path: second, want: "second"},
	} {
		got, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if string(got) != tc.want {
			t.Fatalf("%s body = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestWriteFileRejectsDirectory(t *testing.T) {
	t.Parallel()
	if err := writeFile(t.TempDir(), "body"); err == nil {
		t.Fatal("writeFile accepted a directory path")
	}
}
