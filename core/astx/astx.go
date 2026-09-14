// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package astx holds the Go-source analysis primitives the framework's
// tooling shares — a bounded source walker, an AST-based import rewriter
// (the `golusoris bump` codemod engine), per-function size and complexity
// metrics, and a go.mod reader — so governance scanners and migration tools
// stop re-implementing them with regular expressions. Capability key:
// ast.analyzer.
package astx

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultMaxFiles bounds a Walk (Power-of-10 rule 2).
const DefaultMaxFiles = 50_000

// ErrTooManyFiles is returned when a walk exceeds its file budget.
var ErrTooManyFiles = errors.New("astx: too many files")

// defaultSkipDirs are never descended into: vendored code, fixtures, JS deps.
var defaultSkipDirs = map[string]bool{"vendor": true, "testdata": true, "node_modules": true}

// WalkOptions bounds and filters a source walk.
type WalkOptions struct {
	// IncludeTests visits *_test.go files too.
	IncludeTests bool
	// MaxFiles overrides DefaultMaxFiles (0 = default).
	MaxFiles int
	// SkipDirs adds directory names to the default skip set
	// (vendor, testdata, node_modules, and any name starting with "." or "_").
	SkipDirs []string
}

// Walk calls fn for every .go file under root, in lexical order, honouring
// ctx cancellation and the file budget. Directories starting with "." or "_"
// are skipped, matching the go tool's own package discovery.
func Walk(ctx context.Context, root string, opts WalkOptions, fn func(path string) error) error {
	budget := opts.MaxFiles
	if budget <= 0 {
		budget = DefaultMaxFiles
	}
	skip := make(map[string]bool, len(defaultSkipDirs)+len(opts.SkipDirs))
	for k := range defaultSkipDirs {
		skip[k] = true
	}
	for _, d := range opts.SkipDirs {
		skip[d] = true
	}
	seen := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && shouldSkipDir(d.Name(), skip) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isGoSource(d.Name(), opts.IncludeTests) {
			return nil
		}
		seen++
		if seen > budget {
			return fmt.Errorf("%w: more than %d", ErrTooManyFiles, budget)
		}
		return fn(path)
	})
	if err != nil {
		return fmt.Errorf("astx: walk %s: %w", root, err)
	}
	return nil
}

func shouldSkipDir(name string, skip map[string]bool) bool {
	return skip[name] || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func isGoSource(name string, includeTests bool) bool {
	if !strings.HasSuffix(name, ".go") {
		return false
	}
	return includeTests || !strings.HasSuffix(name, "_test.go")
}

// Imports returns the import paths declared by the Go file at path.
func Imports(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("astx: parse %s: %w", path, err)
	}
	out := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("astx: %s: unquote import %s: %w", path, imp.Path.Value, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// IsThirdParty reports whether importPath is neither stdlib nor part of module.
// Stdlib paths have no dot in their first element.
func IsThirdParty(importPath, module string) bool {
	if module != "" && (importPath == module || strings.HasPrefix(importPath, module+"/")) {
		return false
	}
	first, _, _ := strings.Cut(importPath, "/")
	return strings.Contains(first, ".")
}

// ReadFileBounded reads path, refusing files larger than limit bytes.
func ReadFileBounded(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("astx: stat %s: %w", path, err)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("astx: %s is %d bytes, limit %d", path, info.Size(), limit)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: source path comes from a bounded Walk or the operator // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("astx: read %s: %w", path, err)
	}
	return data, nil
}
