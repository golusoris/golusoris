// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package parse_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/golusoris/golusoris/pdf/parse"
)

func TestParseTime_valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		zero bool
	}{
		{"D:20230415120000", false},
		{"D:20230415", false},
		{"", true},
		{"garbage", true},
	}
	for _, tc := range cases {
		got := parse.ParseTime(tc.in)
		if tc.zero && !got.IsZero() {
			t.Errorf("ParseTime(%q): expected zero, got %v", tc.in, got)
		}
		if !tc.zero && got.IsZero() {
			t.Errorf("ParseTime(%q): expected non-zero", tc.in)
		}
	}
}

func TestMerge_emptySlice(t *testing.T) {
	t.Parallel()
	// Merging an empty list should return nil, not panic.
	err := parse.Merge(context.Background(), nil, "out.pdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOptimize_invalidInputPreservesDestination(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "source.pdf")
	dst := filepath.Join(dir, "destination.pdf")
	if err := os.WriteFile(src, []byte("not a pdf"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(dst, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("write destination: %v", err)
	}

	if err := parse.Optimize(context.Background(), src, dst); err == nil {
		t.Fatal("Optimize() = nil error for invalid PDF")
	}
	got, err := os.ReadFile(dst) // #nosec G304 -- test reads its own fixed temp path.
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(got) != "keep me" {
		t.Fatalf("destination = %q, want unchanged", got)
	}
}

func TestInfoCanceledContextPreventsInputRead(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parse.Info(ctx, panicReadSeeker{}, "canceled.pdf"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Info() error = %v, want context.Canceled", err)
	}
}

func TestMergeRejectsInputCountAboveBound(t *testing.T) {
	t.Parallel()
	inputs := make([]string, 257)
	if err := parse.Merge(context.Background(), inputs, "out.pdf"); err == nil {
		t.Fatal("Merge() = nil error for 257 inputs")
	}
}

func TestOptimizeCanceledContextPreservesDestination(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "destination.pdf")
	if err := os.WriteFile(dst, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("write destination: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := parse.Optimize(ctx, filepath.Join(dir, "missing.pdf"), dst); !errors.Is(err, context.Canceled) {
		t.Fatalf("Optimize() error = %v, want context.Canceled", err)
	}
	got, err := os.ReadFile(dst) // #nosec G304 -- test reads its own fixed temp path.
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(got) != "keep me" {
		t.Fatalf("destination = %q, want unchanged", got)
	}
}

type panicReadSeeker struct{}

func (panicReadSeeker) Read([]byte) (int, error) { panic("unexpected Read") }

func (panicReadSeeker) Seek(int64, int) (int64, error) { panic("unexpected Seek") }

var _ io.ReadSeeker = panicReadSeeker{}
