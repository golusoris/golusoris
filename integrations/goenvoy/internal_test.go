// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package goenvoy

import "testing"

func TestNewRegistry_NilLoggerDoesNotPanic(t *testing.T) {
	t.Parallel()
	r := newRegistry(registryParams{})
	if r == nil {
		t.Fatal("newRegistry returned nil")
	}
}
