// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sse

import (
	"strings"
	"testing"
)

func TestEventFormatPrefixesEveryDataLine(t *testing.T) {
	t.Parallel()
	wire, err := (Event{ID: "42", Event: "update", Data: "first\r\nsecond\n"}).format()
	if err != nil {
		t.Fatalf("format() error = %v", err)
	}
	want := "id: 42\nevent: update\ndata: first\ndata: second\ndata: \n\n"
	if string(wire) != want {
		t.Fatalf("wire = %q, want %q", wire, want)
	}
}

func TestEventFormatRejectsFieldInjection(t *testing.T) {
	t.Parallel()
	for _, event := range []Event{
		{ID: "safe\nevent: admin", Data: "x"},
		{Event: "safe\r\nid: forged", Data: "x"},
		{ID: "nul\x00id", Data: "x"},
	} {
		if wire, err := event.format(); err == nil {
			t.Fatalf("format(%+v) = %q, want error", event, strings.TrimSpace(string(wire)))
		}
	}
}
