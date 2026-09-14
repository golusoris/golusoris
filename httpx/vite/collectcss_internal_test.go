// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package vite

import (
	"slices"
	"testing"
)

func TestCollectCSS(t *testing.T) {
	t.Parallel()
	entries := map[string]Entry{
		"app":    {CSS: []string{"app.css"}, Imports: []string{"shared", "widget"}},
		"shared": {CSS: []string{"shared.css"}, Imports: []string{"widget"}},
		"widget": {CSS: []string{"widget.css"}, Imports: []string{"app"}}, // cycle back to app
		"leaf":   {},
	}
	tests := []struct {
		name string
		src  string
		want []string
	}{
		// positive: pre-order, import order, each file once despite the cycle
		{"transitive", "app", []string{"app.css", "shared.css", "widget.css"}},
		{"from the middle", "shared", []string{"shared.css", "widget.css", "app.css"}},
		// negative: unknown entry and entry without CSS
		{"unknown", "nope", nil},
		{"no css", "leaf", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := collectCSS(entries, tc.src, map[string]bool{})
			if !slices.Equal(got, tc.want) {
				t.Fatalf("collectCSS(%q) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
	// boundary: an already-seen source yields nothing
	if got := collectCSS(entries, "app", map[string]bool{"app": true}); got != nil {
		t.Fatalf("seen source should yield nil, got %v", got)
	}
}
