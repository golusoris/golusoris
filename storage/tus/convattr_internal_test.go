// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"log/slog"
	"testing"

	xslog "golang.org/x/exp/slog"
)

func nestedGroup(depth int) xslog.Attr {
	attr := xslog.String("leaf", "v")
	for i := range depth {
		attr = xslog.Group("g", attr)
		_ = i
	}
	return attr
}

func groupDepth(a slog.Attr) int {
	depth := 0
	for a.Value.Kind() == slog.KindGroup && len(a.Value.Group()) > 0 {
		depth++
		a = a.Value.Group()[0]
	}
	return depth
}

func TestConvAttr(t *testing.T) {
	t.Parallel()
	// positive: a two-level group keeps keys, order and nesting
	got := convAttr(xslog.Group("outer", xslog.String("a", "1"), xslog.Group("inner", xslog.Int("b", 2)), xslog.Bool("c", true)))
	if got.Key != "outer" || got.Value.Kind() != slog.KindGroup {
		t.Fatalf("outer group not converted: %v", got)
	}
	kids := got.Value.Group()
	if len(kids) != 3 || kids[0].Key != "a" || kids[1].Key != "inner" || kids[2].Key != "c" {
		t.Fatalf("children mismatch: %v", kids)
	}
	if inner := kids[1].Value.Group(); len(inner) != 1 || inner[0].Key != "b" || inner[0].Value.Int64() != 2 {
		t.Fatalf("inner group mismatch: %v", kids[1])
	}
	// negative: a plain attr is copied, not grouped
	if leaf := convAttr(xslog.String("k", "v")); leaf.Key != "k" || leaf.Value.Kind() == slog.KindGroup || leaf.Value.String() != "v" {
		t.Fatalf("leaf mismatch: %v", leaf)
	}
	// boundary: nesting at the cap is fully descended, beyond it is truncated, never a panic
	if d := groupDepth(convAttr(nestedGroup(convAttrMaxDepth - 1))); d != convAttrMaxDepth-1 {
		t.Fatalf("depth at cap: got %d", d)
	}
	if d := groupDepth(convAttr(nestedGroup(convAttrMaxDepth + 5))); d != convAttrMaxDepth {
		t.Fatalf("depth beyond cap should stop at %d, got %d", convAttrMaxDepth, d)
	}
}
