// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package safety

import (
	"errors"
	"io"
	"testing"
)

func TestNewFetcher_NilLoggerAllowsPrivateMode(t *testing.T) {
	t.Parallel()
	f, err := newFetcher(Options{Fetch: FetchOptions{AllowPrivate: true}}, nil)
	if err != nil {
		t.Fatalf("newFetcher: %v", err)
	}
	if f == nil {
		t.Fatal("newFetcher returned nil")
	}
}

func TestCappedBodyRejectsSentinelReturnedWithEOF(t *testing.T) {
	t.Parallel()
	body := &cappedBody{
		rc:        &dataEOFReadCloser{data: []byte("12345")},
		remaining: 5,
		cancel:    func() {},
	}
	buf := make([]byte, 5)
	n, err := body.Read(buf)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Read error = %v; want ErrTooLarge", err)
	}
	if n != 4 || string(buf[:n]) != "1234" {
		t.Fatalf("Read = (%d, %q); want (4, %q)", n, buf[:n], "1234")
	}
}

type dataEOFReadCloser struct {
	data []byte
}

func (r *dataEOFReadCloser) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, io.EOF
}

func (*dataEOFReadCloser) Close() error { return nil }
