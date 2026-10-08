// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"errors"
	"fmt"
	"io"
	"math"
)

type s3BodySizer interface {
	size() (int64, error)
}

type s3CountingReader struct {
	check    func() error
	src      io.Reader
	readSize int64
	overflow bool
}

func (r *s3CountingReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, fmt.Errorf("storage/s3: read body context: %w", err)
	}
	n, err := r.src.Read(p)
	r.readSize, r.overflow = addS3ReadSize(r.readSize, n, r.overflow)
	if ctxErr := r.check(); ctxErr != nil {
		return n, fmt.Errorf("storage/s3: read body context: %w", ctxErr)
	}
	return n, wrapS3BodyReadError(err)
}

func (r *s3CountingReader) size() (int64, error) {
	if r.overflow {
		return 0, errors.New("storage/s3: uploaded body size overflow")
	}
	return r.readSize, nil
}

type s3SeekCountingReader struct {
	check      func() error
	src        io.ReadSeeker
	start      int64
	position   int64
	maxReadEnd int64
	overflow   bool
}

func (r *s3SeekCountingReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, fmt.Errorf("storage/s3: read body context: %w", err)
	}
	n, err := r.src.Read(p)
	r.position, r.overflow = addS3ReadSize(r.position, n, r.overflow)
	if r.position > r.maxReadEnd {
		r.maxReadEnd = r.position
	}
	if ctxErr := r.check(); ctxErr != nil {
		return n, fmt.Errorf("storage/s3: read body context: %w", ctxErr)
	}
	return n, wrapS3BodyReadError(err)
}

func (r *s3SeekCountingReader) Seek(offset int64, whence int) (int64, error) {
	position, err := r.src.Seek(offset, whence)
	if err != nil {
		return 0, fmt.Errorf("storage/s3: seek body: %w", err)
	}
	r.position = position
	return position, nil
}

func (r *s3SeekCountingReader) size() (int64, error) {
	if r.overflow || r.maxReadEnd < r.start {
		return 0, errors.New("storage/s3: uploaded body size overflow")
	}
	return r.maxReadEnd - r.start, nil
}

func newS3Body(check func() error, src io.Reader) (io.Reader, s3BodySizer, error) {
	if src == nil {
		return nil, nil, errors.New("storage/s3: body reader is nil")
	}
	if seeker, ok := src.(io.ReadSeeker); ok {
		start, err := seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, nil, fmt.Errorf("storage/s3: determine body position: %w", err)
		}
		body := &s3SeekCountingReader{
			check: check, src: seeker, start: start, position: start, maxReadEnd: start,
		}
		return body, body, nil
	}
	body := &s3CountingReader{check: check, src: src}
	return body, body, nil
}

func addS3ReadSize(current int64, n int, overflow bool) (int64, bool) {
	if overflow || n < 0 || int64(n) > math.MaxInt64-current {
		return current, true
	}
	return current + int64(n), false
}

func wrapS3BodyReadError(err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	return fmt.Errorf("storage/s3: read body source: %w", err)
}
