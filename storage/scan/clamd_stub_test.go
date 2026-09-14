// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !unix

package scan_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/storage/scan"
)

// TestNewClamdScanner_Unsupported confirms the non-unix stub refuses to build a
// clamd Scanner and surfaces both the package and stdlib ErrUnsupported.
func TestNewClamdScanner_Unsupported(t *testing.T) {
	t.Parallel()
	s, err := scan.NewClamdScanner(scan.ClamdOptions{Address: "127.0.0.1:3310"},
		slog.New(slog.DiscardHandler), clock.NewFake())
	if s != nil {
		t.Fatalf("NewClamdScanner = %v, want nil Scanner", s)
	}
	if !errors.Is(err, scan.ErrUnsupported) {
		t.Fatalf("err = %v, want scan.ErrUnsupported", err)
	}
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("err = %v, want errors.ErrUnsupported", err)
	}
}

// TestModule_ClamdUnsupported confirms the fx module fails closed on a platform
// without the clamd backend — even with fail_open, which only covers the ping.
func TestModule_ClamdUnsupported(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"storage:\n  scan:\n    backend: clamd\n",
		"storage:\n  scan:\n    backend: clamd\n    fail_open: true\n    ping_on_start: false\n",
	} {
		cfg := newConfig(t, body)
		_, err := bootScanner(t, cfg)
		if !errors.Is(err, scan.ErrUnsupported) {
			t.Fatalf("boot(%q) = %v, want ErrUnsupported", body, err)
		}
	}
}
