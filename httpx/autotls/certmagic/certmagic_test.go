// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package certmagic_test

import (
	"strings"
	"testing"

	"github.com/golusoris/golusoris/httpx/autotls/certmagic"
)

func TestNewRequiresDomains(t *testing.T) {
	t.Parallel()
	_, err := certmagic.New(certmagic.Options{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Domains required") {
		t.Errorf("err = %q", err)
	}
}
