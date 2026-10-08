// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bio

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

const fastaReadBufferSize = 64 << 10

const (
	// DefaultFASTAMaxBytes caps raw input consumed by ParseFASTA.
	DefaultFASTAMaxBytes int64 = 64 << 20
	// DefaultFASTAMaxRecords caps records returned by ParseFASTA.
	DefaultFASTAMaxRecords = 100_000
	// DefaultFASTAMaxLineBytes caps one logical line, excluding its newline.
	DefaultFASTAMaxLineBytes = 1 << 20
)

var (
	// ErrFASTAInputTooLarge reports a total-byte limit breach.
	ErrFASTAInputTooLarge = errors.New("bio: FASTA input exceeds byte limit")
	// ErrFASTALineTooLong reports a logical-line limit breach.
	ErrFASTALineTooLong = errors.New("bio: FASTA line exceeds byte limit")
	// ErrFASTATooManyRecords reports a record-count limit breach.
	ErrFASTATooManyRecords = errors.New("bio: FASTA record count exceeds limit")
	// ErrInvalidFASTAOptions reports a negative or unrepresentable limit.
	ErrInvalidFASTAOptions = errors.New("bio: invalid FASTA options")
)

// Sequence holds one named biological sequence.
type Sequence struct {
	Name string
	Seq  string
}

// FASTAOptions sets finite parser limits. Zero values select the exported
// defaults; negative values are invalid.
type FASTAOptions struct {
	MaxBytes     int64
	MaxRecords   int
	MaxLineBytes int
}

// ParseFASTA reads FASTA records with finite default limits.
func ParseFASTA(r io.Reader) ([]Sequence, error) {
	return ParseFASTAContext(context.Background(), r, FASTAOptions{})
}

// ParseFASTAContext reads FASTA records while enforcing context cancellation
// and explicit total-byte, record-count, and logical-line limits. Sequence
// lines are upper-cased; text before the first header remains ignored.
func ParseFASTAContext(ctx context.Context, r io.Reader, opts FASTAOptions) ([]Sequence, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrInvalidFASTAOptions)
	}
	if r == nil {
		return nil, fmt.Errorf("%w: nil reader", ErrInvalidFASTAOptions)
	}
	normalized, err := opts.normalized()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	limited := &io.LimitedReader{R: r, N: normalized.MaxBytes + 1}
	source := &fastaCountingReader{r: limited}
	reader := bufio.NewReaderSize(source, fastaReadBufferSize)
	return parseFASTALines(ctx, reader, source, normalized)
}

func (o FASTAOptions) normalized() (FASTAOptions, error) {
	if o.MaxBytes < 0 || o.MaxRecords < 0 || o.MaxLineBytes < 0 {
		return FASTAOptions{}, fmt.Errorf("%w: limits must not be negative", ErrInvalidFASTAOptions)
	}
	if o.MaxBytes == math.MaxInt64 {
		return FASTAOptions{}, fmt.Errorf("%w: MaxBytes must be less than %d", ErrInvalidFASTAOptions, int64(math.MaxInt64))
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = DefaultFASTAMaxBytes
	}
	if o.MaxRecords == 0 {
		o.MaxRecords = DefaultFASTAMaxRecords
	}
	if o.MaxLineBytes == 0 {
		o.MaxLineBytes = DefaultFASTAMaxLineBytes
	}
	return o, nil
}

func parseFASTALines(ctx context.Context, reader *bufio.Reader, source *fastaCountingReader, opts FASTAOptions) ([]Sequence, error) {
	state := fastaState{}
	sequences := make([]Sequence, 0, min(opts.MaxRecords, 128))
	maxLines := opts.MaxBytes + 1
	for lineNumber := int64(1); lineNumber <= maxLines; lineNumber++ {
		line, err := readFASTALine(ctx, reader, opts.MaxLineBytes)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if source.read > opts.MaxBytes {
			return nil, fmt.Errorf("%w: more than %d bytes", ErrFASTAInputTooLarge, opts.MaxBytes)
		}
		if errors.Is(err, io.EOF) {
			return state.finish(sequences), nil
		}
		if err != nil {
			return nil, fmt.Errorf("bio: read FASTA line %d: %w", lineNumber, err)
		}
		sequences, err = state.consume(line, sequences, opts.MaxRecords)
		if err != nil {
			return nil, fmt.Errorf("bio: FASTA line %d: %w", lineNumber, err)
		}
		if lineNumber == maxLines {
			break
		}
	}
	return nil, fmt.Errorf("%w: more than %d bytes", ErrFASTAInputTooLarge, opts.MaxBytes)
}

func readFASTALine(ctx context.Context, reader *bufio.Reader, maxBytes int) ([]byte, error) {
	line := make([]byte, 0, min(maxBytes, fastaReadBufferSize))
	maxFragments := maxBytes/fastaReadBufferSize + 2
	for range maxFragments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fragment, continued, err := reader.ReadLine()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			return nil, fmt.Errorf("bio: read buffered FASTA line: %w", err)
		}
		if len(fragment) > maxBytes-len(line) {
			return nil, fmt.Errorf("%w: more than %d bytes", ErrFASTALineTooLong, maxBytes)
		}
		line = append(line, fragment...)
		if !continued {
			return bytes.TrimRight(line, "\r"), nil
		}
	}
	return nil, fmt.Errorf("%w: more than %d bytes", ErrFASTALineTooLong, maxBytes)
}

type fastaCountingReader struct {
	r    io.Reader
	read int64
}

func (r *fastaCountingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.read += int64(n)
	if err != nil {
		return n, fmt.Errorf("bio: read FASTA source: %w", err)
	}
	return n, nil
}

type fastaState struct {
	name     string
	sequence []byte
	active   bool
	records  int
}

func (s *fastaState) consume(line []byte, sequences []Sequence, maxRecords int) ([]Sequence, error) {
	if len(line) == 0 || line[0] != '>' {
		if s.active {
			s.sequence = append(s.sequence, strings.ToUpper(string(line))...)
		}
		return sequences, nil
	}
	if s.records >= maxRecords {
		return nil, fmt.Errorf("%w: more than %d", ErrFASTATooManyRecords, maxRecords)
	}
	sequences = s.finish(sequences)
	s.name = string(line[1:])
	s.sequence = s.sequence[:0]
	s.active = true
	s.records++
	return sequences, nil
}

func (s *fastaState) finish(sequences []Sequence) []Sequence {
	if !s.active {
		return sequences
	}
	sequences = append(sequences, Sequence{Name: s.name, Seq: string(s.sequence)})
	s.active = false
	return sequences
}
