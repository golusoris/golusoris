// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package llm

import (
	"context"
	"strings"
	"testing"
)

// TestParseSSELine pins the line-classification contract emitSSEChunks
// relies on: which lines to skip, which end the stream, and which carry
// content.
func TestParseSSELine(t *testing.T) {
	t.Parallel()

	t.Run("positive: a data line with content decodes", func(t *testing.T) {
		t.Parallel()
		content, done, ok, err := parseSSELine(`data: {"choices":[{"delta":{"content":"hi"}}]}`)
		if err != nil || !ok || done || content != "hi" {
			t.Fatalf("got content=%q done=%v ok=%v, want content=hi done=false ok=true", content, done, ok)
		}
	})

	t.Run("negative: a non-data line is skipped", func(t *testing.T) {
		t.Parallel()
		content, done, ok, err := parseSSELine(": comment")
		if err != nil || ok || done || content != "" {
			t.Fatalf("got content=%q done=%v ok=%v, want ok=false", content, done, ok)
		}
	})

	t.Run("negative: an unparseable JSON payload fails", func(t *testing.T) {
		t.Parallel()
		content, done, ok, err := parseSSELine("data: {not json")
		if err == nil || ok || done || content != "" {
			t.Fatalf("got content=%q done=%v ok=%v err=%v, want parse error", content, done, ok, err)
		}
	})

	t.Run("boundary: the [DONE] sentinel reports done", func(t *testing.T) {
		t.Parallel()
		content, done, ok, err := parseSSELine("data: [DONE]")
		if err != nil || ok || !done || content != "" {
			t.Fatalf("got content=%q done=%v ok=%v, want done=true ok=false", content, done, ok)
		}
	})

	t.Run("boundary: an empty choices array is ok with empty content", func(t *testing.T) {
		t.Parallel()
		content, done, ok, err := parseSSELine(`data: {"choices":[]}`)
		if err != nil || !ok || done || content != "" {
			t.Fatalf("got content=%q done=%v ok=%v, want content=\"\" done=false ok=true", content, done, ok)
		}
	})
}

// TestEmitSSEChunks_stopsAtDoneAndSkipsBadLines exercises the loop that
// drives parseSSELine: it must skip non-data lines, forward
// only non-empty content, and stop (without forwarding anything further)
// at the [DONE] sentinel.
func TestEmitSSEChunks_stopsAtDoneAndSkipsBadLines(t *testing.T) {
	t.Parallel()
	body := "" +
		": keep-alive comment\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n" +
		"data: {\"choices\":[]}\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"\"}}]}\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n" +
		"data: [DONE]\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"unreachable\"}}]}\n"
	ch := make(chan Chunk, 8)
	if err := emitSSEChunks(context.Background(), strings.NewReader(body), DefaultMaxStreamFrameBytes, ch); err != nil {
		t.Fatalf("emitSSEChunks: %v", err)
	}
	close(ch)

	var got []string
	for c := range ch {
		got = append(got, c.Content)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %q, want [a b]", got)
	}
}
