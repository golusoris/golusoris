// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeLocalTestFiles(t *testing.T, root, dir string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if err := os.WriteFile(filepath.Join(root, dir, fmt.Sprintf("o%03d", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWalkLocalObjects_StartAfterSkipsEarlierSubtreesUnopened(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for i := range 40 {
		writeLocalTestFiles(t, root, fmt.Sprintf("d%02d", i), 2)
	}
	counted := &countingFS{FS: os.DirFS(root)}
	objects, err := walkLocalObjects(context.Background(), counted, localListQuery{
		after: "d38/o000", limit: MaxListLimit, budget: localListWorkBudget,
	})
	if err != nil {
		t.Fatalf("walkLocalObjects: %v", err)
	}
	if len(objects) != 3 || objects[0].Key != "d38/o001" || objects[2].Key != "d39/o001" {
		t.Fatalf("objects = %+v; want d38/o001 through d39/o001", objects)
	}
	// Lstat of the root, the root itself, d38, and d39: earlier subtrees stay closed.
	if counted.opens > 4 {
		t.Fatalf("walk opened %d paths; want at most 4", counted.opens)
	}
}

func TestWalkLocalObjects_WorkLimitAfterProgressReturnsShortPage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeLocalTestFiles(t, root, "a", 3)
	writeLocalTestFiles(t, root, "b", 200)
	query := localListQuery{limit: 10, budget: 100}
	objects, err := walkLocalObjects(context.Background(), os.DirFS(root), query)
	if err != nil {
		t.Fatalf("walkLocalObjects: %v", err)
	}
	if len(objects) != 3 || objects[2].Key != "a/o002" {
		t.Fatalf("objects = %+v; want the three keys under a/", objects)
	}
	query.after = objects[2].Key
	if _, err = walkLocalObjects(context.Background(), os.DirFS(root), query); !errors.Is(err, ErrListWorkLimit) {
		t.Fatalf("walk without progress error = %v; want ErrListWorkLimit", err)
	}
}
