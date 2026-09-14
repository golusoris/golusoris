// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package anthropic

import (
	"fmt"
	"testing"
)

// TestTextDelta pins every skip condition the stream loop used to apply
// inline, so the extraction cannot silently start forwarding (or dropping)
// a line class it did not before.
func TestTextDelta(t *testing.T) {
	t.Parallel()

	const delta = `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":%q}}`

	tests := []struct {
		name string
		line string
		want string
		ok   bool
	}{
		// positive: the one shape that carries text
		{
			name: "text delta",
			line: `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`,
			want: "hi",
			ok:   true,
		},
		// negative: lines that must never produce a chunk
		{name: "no data prefix", line: `event: content_block_delta`},
		{name: "empty line", line: ``},
		{name: "comment line", line: `: keep-alive`},
		{name: "done sentinel", line: `data: [DONE]`},
		{name: "empty payload", line: `data: `},
		{name: "malformed json", line: `data: {"type":`},
		{name: "other event type", line: `data: {"type":"message_stop"}`},
		{
			name: "other delta type",
			line: `data: {"type":"content_block_delta","delta":{"type":"input_json_delta","text":"x"}}`,
		},
		// boundary: right shape, nothing to send
		{
			name: "empty text is not a chunk",
			line: `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":""}}`,
		},
		// boundary: prefix match must be exact, not a trimmed variant
		{name: "prefix without space", line: `data:{"type":"content_block_delta"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := textDelta(tc.line)
			if ok != tc.ok {
				t.Fatalf("textDelta(%q) ok = %v, want %v", tc.line, ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("textDelta(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}

	// boundary: whitespace and multi-byte text survive verbatim.
	for _, text := range []string{" ", "\n", "ünïcödé ✅", "a b\tc"} {
		line := fmt.Sprintf(delta, text)
		got, ok := textDelta(line)
		if !ok || got != text {
			t.Fatalf("textDelta(%q) = %q, %v; want %q, true", line, got, ok, text)
		}
	}
}
