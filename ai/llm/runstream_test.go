// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package llm_test

import (
	"errors"
	"testing"

	"github.com/golusoris/golusoris/ai/llm"
)

// TestRunStream pins the contract every backend's Stream relies on.
func TestRunStream(t *testing.T) {
	t.Parallel()

	t.Run("positive: deltas are forwarded in order, then the channel closes", func(t *testing.T) {
		t.Parallel()
		ch := llm.RunStream(func(ch chan<- llm.Chunk) error {
			ch <- llm.Chunk{Content: "a"}
			ch <- llm.Chunk{Content: "b"}
			return nil
		})
		var got []string
		for c := range ch {
			if c.Err != nil {
				t.Fatalf("unexpected Err chunk: %v", c.Err)
			}
			got = append(got, c.Content)
		}
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("got %q, want [a b]", got)
		}
	})

	t.Run("negative: fn's error arrives as the terminal Err chunk after its deltas", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		ch := llm.RunStream(func(ch chan<- llm.Chunk) error {
			ch <- llm.Chunk{Content: "partial"}
			return boom
		})
		first, ok := <-ch
		if !ok || first.Content != "partial" || first.Err != nil {
			t.Fatalf("first = %+v ok=%v, want the partial delta", first, ok)
		}
		last, ok := <-ch
		if !ok || !errors.Is(last.Err, boom) {
			t.Fatalf("last = %+v ok=%v, want Err=boom", last, ok)
		}
		if _, open := <-ch; open {
			t.Fatal("channel must be closed after the terminal Err chunk")
		}
	})

	t.Run("boundary: nil error with no sends closes an empty channel", func(t *testing.T) {
		t.Parallel()
		ch := llm.RunStream(func(chan<- llm.Chunk) error { return nil })
		if c, open := <-ch; open {
			t.Fatalf("expected closed empty channel, got %+v", c)
		}
	})
}
