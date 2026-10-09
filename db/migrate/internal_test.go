// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package migrate

import (
	"net/url"
	"testing"

	"github.com/golusoris/golusoris/core/config"
)

func TestLoadOptions_empty(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := LoadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// LoadOptions does not set defaults — zero value is valid.
	if opts.Path != "" {
		t.Errorf("Path = %q, want empty", opts.Path)
	}
	if opts.Auto {
		t.Error("Auto should be false by default")
	}
	if opts.DSN != "" {
		t.Errorf("DSN = %q, want empty", opts.DSN)
	}
}

func TestOptions_withFS(t *testing.T) {
	t.Parallel()
	opts := Options{Path: "migrations", Auto: true}
	// WithFS is exported — verify it sets FS while preserving other fields.
	// We pass nil FS here just to test the chaining; nil FS is handled by New.
	o2 := opts.WithFS(nil)
	if o2.Path != "migrations" {
		t.Errorf("Path = %q, want migrations", o2.Path)
	}
	if !o2.Auto {
		t.Error("Auto not preserved after WithFS")
	}
}

func TestOptions_preservesNonZero(t *testing.T) {
	t.Parallel()
	opts := Options{
		Path: "/app/migrations",
		Auto: true,
		DSN:  "postgres://localhost/test",
	}
	if opts.Path != "/app/migrations" {
		t.Error("Path not preserved")
	}
	if !opts.Auto {
		t.Error("Auto not preserved")
	}
	if opts.DSN != "postgres://localhost/test" {
		t.Error("DSN not preserved")
	}
}

func TestFileSourceURLEscapesReservedPathCharacters(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		"migrations":      "file://migrations",
		"dir/a b":         "file://dir/a%20b",
		"/tmp/a#b?tenant": "file:///tmp/a%23b%3Ftenant",
	} {
		if got := fileSourceURLFor(path, false); got != want {
			t.Errorf("fileSourceURLFor(%q, false) = %q, want %q", path, got, want)
		}
	}
}

// TestFileSourceURLWindowsPaths pins the form golang-migrate's file source
// reads back: url.Parse, then Host+Path must be the slashed path (#643).
func TestFileSourceURLWindowsPaths(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ path, want, parsed string }{
		{`C:\app\migrations`, "file://C:/app/migrations", "C:/app/migrations"},
		{`c:\a#b\c?d`, "file://c:/a%23b/c%3Fd", "c:/a#b/c?d"},
		{`\\server\share\migrations`, "file:////server/share/migrations", "//server/share/migrations"},
		{`migrations\v1`, "file://migrations/v1", "migrations/v1"},
		{`1:\not-a-drive`, "file://1:/not-a-drive", ""},
		{`C:`, "file://C:", ""},
	} {
		got := fileSourceURLFor(tc.path, true)
		if got != tc.want {
			t.Errorf("fileSourceURLFor(%q, true) = %q, want %q", tc.path, got, tc.want)
			continue
		}
		if tc.parsed == "" {
			continue
		}
		u, err := url.Parse(got)
		if err != nil || u.Host+u.Path != tc.parsed {
			t.Errorf("url.Parse(%q) host+path = %q, %v; want %q", got, u.Host+u.Path, err, tc.parsed)
		}
	}
}
