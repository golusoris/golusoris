// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package yaml

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveTemp_DeletesFileAndKeepsPrimary(t *testing.T) {
	t.Parallel()
	tmp := filepath.Join(t.TempDir(), "x.tmp")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	primary := errors.New("primary")

	got := removeTemp(tmp, primary)

	if got != primary { //nolint:errorlint // identity is the contract: a clean removal returns err unchanged
		t.Errorf("removeTemp = %v, want the primary error unchanged", got)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp file still present after removeTemp: stat err = %v", err)
	}
}

func TestRemoveTemp_JoinsRemovalFailure(t *testing.T) {
	t.Parallel()
	primary := errors.New("primary")

	got := removeTemp(filepath.Join(t.TempDir(), "missing.tmp"), primary)

	if !errors.Is(got, primary) {
		t.Errorf("removeTemp = %v, want it to keep the primary error", got)
	}
	if !errors.Is(got, os.ErrNotExist) {
		t.Errorf("removeTemp = %v, want it to join the removal failure", got)
	}
	if !strings.Contains(got.Error(), "yaml: remove temp") {
		t.Errorf("removeTemp = %q, want the removal failure to be labelled", got)
	}
}

func TestWriteAndClose_JoinsWriteAndCloseFailures(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp(t.TempDir(), "closed.*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got := writeAndClose(f, []byte("x"), 0o600)

	if !errors.Is(got, os.ErrClosed) {
		t.Fatalf("writeAndClose on a closed file = %v, want os.ErrClosed", got)
	}
	msg := got.Error()
	if !strings.Contains(msg, "yaml: write ") || !strings.Contains(msg, "yaml: close ") {
		t.Errorf("writeAndClose = %q, want both the write and the close failure joined", msg)
	}
}
