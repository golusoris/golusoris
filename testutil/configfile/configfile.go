// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package configfile writes test configuration bodies to private temporary files.
package configfile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Write stores body in a temporary config.yaml and returns its path.
func Write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := writeFile(path, body); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeFile(path, body string) error {
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("configfile: write: %w", err)
	}
	return nil
}
