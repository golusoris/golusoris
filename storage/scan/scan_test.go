// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package scan_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/storage/scan"
)

// eicar is the standard, safe, non-malicious AV test vector. No real malware.
const eicar = `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`

func TestNoopScanner_AlwaysClean(t *testing.T) {
	t.Parallel()
	s := newNoopForTest(t)
	ctx := context.Background()

	v, err := s.Scan(ctx, strings.NewReader(eicar))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !v.Clean {
		t.Fatalf("noop verdict = %+v, want Clean", v)
	}
	if err := s.ScanStrict(ctx, strings.NewReader("anything")); err != nil {
		t.Fatalf("ScanStrict: %v", err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestNoopScanner_DrainError(t *testing.T) {
	t.Parallel()
	s := newNoopForTest(t)
	_, err := s.Scan(context.Background(), errReader{})
	if err == nil {
		t.Fatal("Scan(errReader) = nil, want drain error")
	}
}

func newNoopForTest(t *testing.T) scan.Scanner {
	t.Helper()
	return scan.NewNoopScanner(discardLogger(t))
}

func discardLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.DiscardHandler)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

var _ io.Reader = errReader{}
