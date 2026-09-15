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
//
// Iterative by construction: it drives stdlib's filepath.WalkDir (itself an
// explicit-stack walk, not recursive call-per-directory) through a named
// callback rather than recursing per directory, matching astx's no-recursion
// rule for AST tooling.
func Walk(ctx context.Context, root string, opts WalkOptions, fn func(path string) error) error {
	w := &walker{
		ctxErr: ctx.Err, // bound method value, not a stored context.Context (containedctx)
		root:   root,
		opts:   opts,
		fn:     fn,
		budget: walkBudget(opts.MaxFiles),
		skip:   walkSkipSet(opts.SkipDirs),
	}
	if err := filepath.WalkDir(root, w.visit); err != nil {
		return fmt.Errorf("astx: walk %s: %w", root, err)
	}
	return nil
}

// walker carries one Walk call's state across filepath.WalkDir callbacks —
// giving the callback a name and fields instead of a nested closure is what
// pulls Walk itself under the HISS-04 cognitive-complexity cap.
type walker struct {
	ctxErr func() error // bound from a context.Context; see Walk
	root   string
	opts   WalkOptions
	fn     func(path string) error
	budget int
	skip   map[string]bool
	seen   int
}

// walkBudget resolves the effective file-count budget: maxFiles when
// positive, else DefaultMaxFiles.
func walkBudget(maxFiles int) int {
	if maxFiles <= 0 {
		return DefaultMaxFiles
	}
	return maxFiles
}

// walkSkipSet merges the default skip-dir set with the caller's additions
// into a fresh map (defaultSkipDirs itself is never mutated).
func walkSkipSet(extra []string) map[string]bool {
	skip := make(map[string]bool, len(defaultSkipDirs)+len(extra))
	for k := range defaultSkipDirs {
		skip[k] = true
	}
	for _, d := range extra {
		skip[d] = true
	}
	return skip
}

// visit is the filepath.WalkDir callback: it applies ctx cancellation, the
// directory skip-list, the *.go / _test.go filter, and the file budget,
// in that order, matching Walk's original behaviour.
func (w *walker) visit(path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if err := w.ctxErr(); err != nil {
		return err
	}
	if d.IsDir() {
		return w.visitDir(path, d)
	}
	if !isGoSource(d.Name(), w.opts.IncludeTests) {
		return nil
	}
	w.seen++
	if w.seen > w.budget {
		return fmt.Errorf("%w: more than %d", ErrTooManyFiles, w.budget)
	}
	return w.fn(path)
}

// visitDir decides whether to descend into or skip a directory entry: the
// walk root itself is never skipped, even if its name would otherwise match.
func (w *walker) visitDir(path string, d fs.DirEntry) error {
	if path != w.root && shouldSkipDir(d.Name(), w.skip) {
		return filepath.SkipDir
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
	data, err := os.ReadFile(path) // #nosec G304 -- source path comes from a bounded Walk or the operator; size-bounded by the Stat check above
	if err != nil {
		return nil, fmt.Errorf("astx: read %s: %w", path, err)
	}
	return data, nil
}
