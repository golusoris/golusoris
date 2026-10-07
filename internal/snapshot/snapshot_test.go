// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package snapshot_test

import (
	"math"
	"testing"
	"time"

	"github.com/golusoris/golusoris/internal/snapshot"
)

func TestClonePreservesTypesAndSeparatesMutableValues(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 19, 22, 0, 0, 0, time.UTC)
	bytes := []byte{1, 2}
	source := map[string]any{
		"int64":  int64(-9),
		"uint64": uint64(math.MaxUint64),
		"time":   now,
		"bytes":  bytes,
	}

	cloned, err := snapshot.Clone(source)
	if err != nil {
		t.Fatal(err)
	}
	bytes[0] = 99
	if got := cloned["int64"]; got != int64(-9) {
		t.Fatalf("int64 = %v (%T)", got, got)
	}
	if got := cloned["uint64"]; got != uint64(math.MaxUint64) {
		t.Fatalf("uint64 = %v (%T)", got, got)
	}
	if got := cloned["time"]; got != now {
		t.Fatalf("time = %v (%T)", got, got)
	}
	if got := cloned["bytes"].([]byte)[0]; got != 1 {
		t.Fatalf("bytes[0] = %d, want 1", got)
	}
}

func TestClonePreservesCycles(t *testing.T) {
	t.Parallel()
	cycle := map[string]any{}
	cycle["self"] = cycle

	cloned, err := snapshot.Clone(cycle)
	if err != nil {
		t.Fatal(err)
	}
	cloned["marker"] = true
	self := cloned["self"].(map[string]any)
	if got := self["marker"]; got != true {
		t.Fatalf("cycle marker = %v, want true", got)
	}
	if _, exists := cycle["marker"]; exists {
		t.Fatal("clone aliases source")
	}
}

func TestCloneRejectsUnsupportedMutableValue(t *testing.T) {
	t.Parallel()
	if _, err := snapshot.Clone(map[string]any{"fn": func() {}}); err == nil {
		t.Fatal("Clone() accepted function")
	}
}

func TestCloneRejectsWideContainerBeforeCopy(t *testing.T) {
	t.Parallel()
	wide := make([]byte, 65_537)
	if _, err := snapshot.Clone(wide); err == nil {
		t.Fatal("Clone() accepted container wider than node bound")
	}
}
