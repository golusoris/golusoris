// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package geo

import (
	"context"
	"testing"
)

func TestRegisterTypes(t *testing.T) {
	t.Parallel()
	// RegisterTypes is a no-op stub; it must return nil.
	if err := RegisterTypes(context.Background(), nil); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPoint_String(t *testing.T) {
	t.Parallel()
	p := &Point{Lat: 1.5, Lon: 2.5}
	s := p.String()
	if s == "" {
		t.Fatal("expected non-empty string from Point.String()")
	}
}
