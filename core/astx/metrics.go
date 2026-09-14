// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package astx

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// FuncMetric describes one function or method declaration with a body.
type FuncMetric struct {
	// Name is "Func" or "Recv.Method".
	Name string `json:"name"`
	// Line is the 1-based line of the declaration.
	Line int `json:"line"`
	// Lines is the span from the func keyword to the closing brace, inclusive.
	Lines int `json:"lines"`
	// Cyclomatic is McCabe complexity: 1 + decision points
	// (if, for, range, case, comm clauses, &&, ||).
	Cyclomatic int `json:"cyclomatic"`
	// Statements counts statement nodes excluding bare blocks.
	Statements int `json:"statements"`
	// Params counts declared parameters.
	Params int `json:"params"`
}

// FuncMetrics parses the Go file at path and returns metrics for every
// function and method that has a body, in source order. HISS-04 / Power-of-10
// rule 4 gates (function length, complexity) are evaluated on this output.
func FuncMetrics(path string) ([]FuncMetric, error) {
	src, err := ReadFileBounded(path, MaxSourceSize)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("astx: parse %s: %w", path, err)
	}
	var out []FuncMetric
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		out = append(out, measure(fset, fn))
	}
	return out, nil
}

func measure(fset *token.FileSet, fn *ast.FuncDecl) FuncMetric {
	start, end := fset.Position(fn.Pos()), fset.Position(fn.End())
	m := FuncMetric{
		Name:       funcName(fn),
		Line:       start.Line,
		Lines:      end.Line - start.Line + 1,
		Cyclomatic: 1,
		Params:     fn.Type.Params.NumFields(),
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		m.Cyclomatic += branchWeight(n)
		if isStatement(n) {
			m.Statements++
		}
		return true
	})
	return m
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return recvTypeName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func recvTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvTypeName(t.X)
	case *ast.IndexListExpr:
		return recvTypeName(t.X)
	default:
		return "?"
	}
}

// branchWeight returns the cyclomatic contribution of n.
func branchWeight(n ast.Node) int {
	switch x := n.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.CommClause:
		return 1
	case *ast.CaseClause:
		if x.List == nil { // default: no new path
			return 0
		}
		return 1
	case *ast.BinaryExpr:
		if x.Op == token.LAND || x.Op == token.LOR {
			return 1
		}
	}
	return 0
}

func isStatement(n ast.Node) bool {
	if _, isBlock := n.(*ast.BlockStmt); isBlock {
		return false
	}
	_, ok := n.(ast.Stmt)
	return ok
}
