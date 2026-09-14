// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package capabilities_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/capabilities"
)

const valid = `version: 1
framework: github.com/golusoris/golusoris
modules:
  - github.com/golusoris/golusoris/core
packages:
  - import: github.com/golusoris/golusoris/core/config
    module: github.com/golusoris/golusoris/core
    domain: config
    capabilities: [config.loader, config.env]
    description: koanf-backed configuration
    replaces: [github.com/spf13/viper]
  - import: github.com/golusoris/golusoris/db/pgx
    domain: db
    capabilities: [db.postgres]
    status: stable
    replaces: [github.com/jackc/pgx, github.com/lib/pq]
`

func TestParseValid(t *testing.T) {
	t.Parallel()
	idx, err := capabilities.Parse([]byte(valid))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !idx.Covers("db.postgres") || idx.Covers("db.orm") {
		t.Fatal("Covers mismatch")
	}
	by := idx.ByCapability()
	if got := by["config.env"]; len(got) != 1 || got[0] != "github.com/golusoris/golusoris/core/config" {
		t.Fatalf("ByCapability: %v", by)
	}
	if got := strings.Join(idx.Keys(), ","); got != "config.env,config.loader,db.postgres" {
		t.Fatalf("Keys: %s", got)
	}
	rep := idx.Replacements()
	if rep["github.com/lib/pq"] != "github.com/golusoris/golusoris/db/pgx" || len(rep) != 3 {
		t.Fatalf("Replacements: %v", rep)
	}
	if p, ok := idx.Lookup("github.com/golusoris/golusoris/db/pgx"); !ok || p.Domain != "db" {
		t.Fatalf("Lookup: %+v %v", p, ok)
	}
	if _, ok := idx.Lookup("nope"); ok {
		t.Fatal("Lookup of unknown import must fail")
	}
}

func TestParseRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want error
	}{
		{"wrong version", strings.Replace(valid, "version: 1", "version: 2", 1), capabilities.ErrSchemaVersion},
		{"missing framework", strings.Replace(valid, "framework: github.com/golusoris/golusoris\n", "", 1), capabilities.ErrInvalid},
		{"duplicate import", valid + "  - import: github.com/golusoris/golusoris/db/pgx\n    domain: db\n    capabilities: [db.x]\n", capabilities.ErrInvalid},
		{"foreign import", valid + "  - import: github.com/other/x\n    domain: x\n    capabilities: [x.y]\n", capabilities.ErrInvalid},
		{"no capabilities", valid + "  - import: github.com/golusoris/golusoris/z\n    domain: z\n    capabilities: []\n", capabilities.ErrInvalid},
		{"bad key", valid + "  - import: github.com/golusoris/golusoris/z\n    domain: z\n    capabilities: [NoDot]\n", capabilities.ErrInvalid},
		{"undeclared module", valid + "  - import: github.com/golusoris/golusoris/z\n    module: github.com/golusoris/golusoris/zz\n    domain: z\n    capabilities: [z.a]\n", capabilities.ErrInvalid},
		{"unknown status", valid + "  - import: github.com/golusoris/golusoris/z\n    domain: z\n    capabilities: [z.a]\n    status: alpha\n", capabilities.ErrInvalid},
		{"unknown field (strict yaml)", valid + "extra: 1\n", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := capabilities.Parse([]byte(tc.src))
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, capabilities.FileName)
	if err := os.WriteFile(p, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := capabilities.Load(p)
	if err != nil || len(idx.Packages) != 2 {
		t.Fatalf("Load: %v %+v", err, idx)
	}
	if _, err := capabilities.Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
