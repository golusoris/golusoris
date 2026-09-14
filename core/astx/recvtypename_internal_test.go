// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package astx

import (
	"go/ast"
	"testing"
)

func wrapStars(inner ast.Expr, n int) ast.Expr {
	for range n {
		inner = &ast.StarExpr{X: inner}
	}
	return inner
}

func TestRecvTypeName(t *testing.T) {
	t.Parallel()
	ident := &ast.Ident{Name: "T"}
	tests := []struct {
		name string
		expr ast.Expr
		want string
	}{
		// positive
		{"ident", ident, "T"},
		{"pointer", &ast.StarExpr{X: ident}, "T"},
		{"generic", &ast.IndexExpr{X: ident, Index: &ast.Ident{Name: "K"}}, "T"},
		{"generic multi", &ast.IndexListExpr{X: ident, Indices: []ast.Expr{&ast.Ident{Name: "K"}, &ast.Ident{Name: "V"}}}, "T"},
		{"pointer to generic", &ast.StarExpr{X: &ast.IndexExpr{X: ident, Index: &ast.Ident{Name: "K"}}}, "T"},
		// negative: shapes that are not receivers
		{"selector", &ast.SelectorExpr{X: &ast.Ident{Name: "pkg"}, Sel: ident}, "?"},
		{"func type", &ast.FuncType{}, "?"},
		{"nil", nil, "?"},
		// boundary: the depth bound
		{"depth at bound", wrapStars(ident, recvTypeNameMaxDepth-1), "T"},
		{"depth beyond bound", wrapStars(ident, recvTypeNameMaxDepth), "?"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := recvTypeName(tc.expr); got != tc.want {
				t.Fatalf("recvTypeName(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
