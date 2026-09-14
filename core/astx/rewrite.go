// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package astx

import (
	"bytes"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
)

// MaxSourceSize bounds a single source file fed to the rewriter (4 MiB).
const MaxSourceSize int64 = 4 << 20

// Resolve maps importPath through mapping. A key matches the exact path or a
// prefix at a "/" boundary; the longest key wins and the remainder is kept,
// so {"a/b": "x/y"} maps "a/b/c" to "x/y/c".
func Resolve(importPath string, mapping map[string]string) (string, bool) {
	best, bestLen := "", -1
	for old := range mapping {
		if (importPath == old || strings.HasPrefix(importPath, old+"/")) && len(old) > bestLen {
			best, bestLen = old, len(old)
		}
	}
	if bestLen < 0 {
		return importPath, false
	}
	return mapping[best] + importPath[len(best):], true
}

// RewriteImports rewrites the import paths in src per mapping (see Resolve)
// and returns gofmt-formatted source plus whether anything changed. Comments
// and import aliases are preserved; import grouping is left to gci/gofumpt.
func RewriteImports(src []byte, mapping map[string]string) ([]byte, bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, false, fmt.Errorf("astx: parse: %w", err)
	}
	changed := false
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, false, fmt.Errorf("astx: unquote import %s: %w", imp.Path.Value, err)
		}
		if next, ok := Resolve(path, mapping); ok && next != path {
			imp.Path.Value = strconv.Quote(next)
			changed = true
		}
	}
	if !changed {
		return src, false, nil
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, false, fmt.Errorf("astx: format: %w", err)
	}
	return buf.Bytes(), true, nil
}

// RewriteImportsFile applies RewriteImports to the file at path in place,
// preserving its permissions. It reports whether the file changed.
func RewriteImportsFile(path string, mapping map[string]string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("astx: stat %s: %w", path, err)
	}
	src, err := ReadFileBounded(path, MaxSourceSize)
	if err != nil {
		return false, err
	}
	out, changed, err := RewriteImports(src, mapping)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return false, nil
	}
	if err := os.WriteFile(path, out, info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("astx: write %s: %w", path, err)
	}
	return true, nil
}
