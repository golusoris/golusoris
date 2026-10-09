// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package localstackdev

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var pinned = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*:[^@\s]+@sha256:[a-f0-9]{64}$`)

func TestImagesArePinned(t *testing.T) {
	t.Parallel()
	for _, image := range []string{LocalStackImage, RyukImage} {
		if !pinned.MatchString(image) {
			t.Errorf("%q is not tag@sha256:<64 hex>", image)
		}
	}
	for _, image := range []string{"localstack/localstack:4", "localstack/localstack@sha256:" + strings.Repeat("a", 64), "localstack/localstack:4@sha256:" + strings.Repeat("A", 64)} {
		if pinned.MatchString(image) {
			t.Errorf("%q accepted as pinned", image)
		}
	}
}

// TestRyukMatchesRootPin keeps the reaper pin in lockstep with the root
// module's internal/testimages, which this module cannot import.
func TestRyukMatchesRootPin(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..", "..", "..", "..", "..", "internal", "testimages", "images.go")
	b, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read root pins: %v", err)
	}
	if !strings.Contains(string(b), `"`+RyukImage+`"`) {
		t.Fatalf("RyukImage %q differs from %s", RyukImage, root)
	}
}
