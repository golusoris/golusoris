// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bio_test

import (
	"context"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	bio "github.com/golusoris/golusoris/science/bio"
)

func TestParseFASTACompatibility(t *testing.T) {
	t.Parallel()

	got, err := bio.ParseFASTA(strings.NewReader("ignored\n>one\r\nacg\r\r\n>two\nnN"))
	if err != nil {
		t.Fatalf("ParseFASTA() error = %v", err)
	}
	want := []bio.Sequence{{Name: "one", Seq: "ACG"}, {Name: "two", Seq: "NN"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseFASTA() = %#v, want %#v", got, want)
	}
}

func TestParseFASTAContextLongMultilineRecord(t *testing.T) {
	t.Parallel()

	line := strings.Repeat("acgt", 20<<10)
	input := ">long\n" + line + "\n" + line + "\n"
	opts := bio.FASTAOptions{
		MaxBytes:     int64(len(input)),
		MaxRecords:   1,
		MaxLineBytes: len(line),
	}
	got, err := bio.ParseFASTAContext(context.Background(), strings.NewReader(input), opts)
	if err != nil {
		t.Fatalf("ParseFASTAContext() error = %v", err)
	}
	wantSeq := strings.Repeat("ACGT", 40<<10)
	if len(got) != 1 || got[0].Name != "long" || got[0].Seq != wantSeq {
		t.Fatalf("ParseFASTAContext() record = {count:%d name:%q bytes:%d}, want {1 long %d}", len(got), firstName(got), firstLength(got), len(wantSeq))
	}
}

func TestParseFASTAContextLimits(t *testing.T) {
	t.Parallel()

	t.Run("total bytes exact", func(t *testing.T) {
		t.Parallel()

		input := ">x\nAC\n"
		opts := bio.FASTAOptions{MaxBytes: int64(len(input)), MaxRecords: 1, MaxLineBytes: 2}
		if _, err := bio.ParseFASTAContext(context.Background(), strings.NewReader(input), opts); err != nil {
			t.Fatalf("exact limit error = %v", err)
		}
	})

	t.Run("total bytes plus one", func(t *testing.T) {
		t.Parallel()

		input := ">x\nAC\n"
		opts := bio.FASTAOptions{MaxBytes: int64(len(input) - 1), MaxRecords: 1, MaxLineBytes: 2}
		_, err := bio.ParseFASTAContext(context.Background(), strings.NewReader(input), opts)
		if !errors.Is(err, bio.ErrFASTAInputTooLarge) {
			t.Fatalf("limit+1 error = %v, want ErrFASTAInputTooLarge", err)
		}
	})

	t.Run("total bytes plus one with short reads", func(t *testing.T) {
		t.Parallel()

		input := "\n\n\n\n"
		opts := bio.FASTAOptions{MaxBytes: int64(len(input) - 1), MaxRecords: 1, MaxLineBytes: 1}
		_, err := bio.ParseFASTAContext(context.Background(), &oneByteReader{r: strings.NewReader(input)}, opts)
		if !errors.Is(err, bio.ErrFASTAInputTooLarge) {
			t.Fatalf("limit+1 error = %v, want ErrFASTAInputTooLarge", err)
		}
	})

	t.Run("total bytes exact with short reads", func(t *testing.T) {
		t.Parallel()

		input := "\n\n\n"
		opts := bio.FASTAOptions{MaxBytes: int64(len(input)), MaxRecords: 1, MaxLineBytes: 1}
		if _, err := bio.ParseFASTAContext(context.Background(), &oneByteReader{r: strings.NewReader(input)}, opts); err != nil {
			t.Fatalf("exact limit error = %v", err)
		}
	})

	t.Run("line exact", func(t *testing.T) {
		t.Parallel()

		opts := bio.FASTAOptions{MaxBytes: 16, MaxRecords: 1, MaxLineBytes: 3}
		if _, err := bio.ParseFASTAContext(context.Background(), strings.NewReader(">x\nACG\n"), opts); err != nil {
			t.Fatalf("exact limit error = %v", err)
		}
	})

	t.Run("line plus one", func(t *testing.T) {
		t.Parallel()

		opts := bio.FASTAOptions{MaxBytes: 16, MaxRecords: 1, MaxLineBytes: 3}
		_, err := bio.ParseFASTAContext(context.Background(), strings.NewReader(">x\nACGT\n"), opts)
		if !errors.Is(err, bio.ErrFASTALineTooLong) {
			t.Fatalf("limit+1 error = %v, want ErrFASTALineTooLong", err)
		}
	})

	t.Run("records exact", func(t *testing.T) {
		t.Parallel()

		input := ">a\nA\n>b\nC\n"
		opts := bio.FASTAOptions{MaxBytes: int64(len(input)), MaxRecords: 2, MaxLineBytes: 2}
		if _, err := bio.ParseFASTAContext(context.Background(), strings.NewReader(input), opts); err != nil {
			t.Fatalf("exact limit error = %v", err)
		}
	})

	t.Run("records plus one", func(t *testing.T) {
		t.Parallel()

		input := ">a\nA\n>b\nC\n>c\nG\n"
		opts := bio.FASTAOptions{MaxBytes: int64(len(input)), MaxRecords: 2, MaxLineBytes: 2}
		_, err := bio.ParseFASTAContext(context.Background(), strings.NewReader(input), opts)
		if !errors.Is(err, bio.ErrFASTATooManyRecords) {
			t.Fatalf("limit+1 error = %v, want ErrFASTATooManyRecords", err)
		}
	})
}

func TestParseFASTAContextCancellation(t *testing.T) {
	t.Parallel()

	t.Run("before read", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := &readCounter{r: strings.NewReader(">x\nAC\n")}
		_, err := bio.ParseFASTAContext(ctx, r, bio.FASTAOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if r.reads != 0 {
			t.Fatalf("reads = %d, want 0", r.reads)
		}
	})

	t.Run("during read", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		r := &cancelingReader{r: strings.NewReader(">x\nAC\n"), cancel: cancel}
		_, err := bio.ParseFASTAContext(ctx, r, bio.FASTAOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
}

func TestParseFASTAContextRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  context.Context
		r    io.Reader
		opts bio.FASTAOptions
	}{
		{name: "negative bytes", ctx: context.Background(), r: strings.NewReader(""), opts: bio.FASTAOptions{MaxBytes: -1}},
		{name: "negative records", ctx: context.Background(), r: strings.NewReader(""), opts: bio.FASTAOptions{MaxRecords: -1}},
		{name: "negative line", ctx: context.Background(), r: strings.NewReader(""), opts: bio.FASTAOptions{MaxLineBytes: -1}},
		{name: "unrepresentable bytes", ctx: context.Background(), r: strings.NewReader(""), opts: bio.FASTAOptions{MaxBytes: math.MaxInt64}},
		{name: "nil context", r: strings.NewReader("")},
		{name: "nil reader", ctx: context.Background()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := bio.ParseFASTAContext(test.ctx, test.r, test.opts)
			if !errors.Is(err, bio.ErrInvalidFASTAOptions) {
				t.Fatalf("error = %v, want ErrInvalidFASTAOptions", err)
			}
		})
	}
}

func TestParseFASTALegacyWrapperIsBounded(t *testing.T) {
	t.Parallel()

	input := ">x\n" + strings.Repeat("A", bio.DefaultFASTAMaxLineBytes+1) + "\n"
	_, err := bio.ParseFASTA(strings.NewReader(input))
	if !errors.Is(err, bio.ErrFASTALineTooLong) {
		t.Fatalf("error = %v, want ErrFASTALineTooLong", err)
	}
}

func TestParseFASTAContextPropagatesReaderError(t *testing.T) {
	t.Parallel()

	want := errors.New("injected read failure")
	_, err := bio.ParseFASTAContext(context.Background(), errorReader{err: want}, bio.FASTAOptions{})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped injected error", err)
	}
}

func firstName(seqs []bio.Sequence) string {
	if len(seqs) == 0 {
		return ""
	}
	return seqs[0].Name
}

func firstLength(seqs []bio.Sequence) int {
	if len(seqs) == 0 {
		return 0
	}
	return len(seqs[0].Seq)
}

type readCounter struct {
	r     io.Reader
	reads int
}

func (r *readCounter) Read(p []byte) (int, error) {
	r.reads++
	return r.r.Read(p)
}

type cancelingReader struct {
	r      io.Reader
	cancel context.CancelFunc
}

type oneByteReader struct {
	r io.Reader
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	return r.r.Read(p[:min(len(p), 1)])
}

func (r *cancelingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.cancel()
	return n, err
}
