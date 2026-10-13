// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package podinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenMounted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("jwt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !tokenMounted(token) {
		t.Error("token file present: want true")
	}
	if tokenMounted(filepath.Join(dir, "missing")) {
		t.Error("token file absent: want false")
	}
}
