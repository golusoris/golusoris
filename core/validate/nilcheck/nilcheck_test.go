// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nilcheck_test

import (
	"testing"

	"github.com/golusoris/golusoris/core/validate/nilcheck"
)

func TestIsNilPositive(t *testing.T) {
	t.Parallel()
	var nilInterface any
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "nil", value: nil},
		{name: "channel", value: (chan int)(nil)},
		{name: "function", value: (func())(nil)},
		{name: "interface", value: nilInterface},
		{name: "map", value: map[string]int(nil)},
		{name: "pointer", value: (*int)(nil)},
		{name: "slice", value: []int(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if !nilcheck.IsNil(test.value) {
				t.Fatalf("IsNil(%T) = false, want true", test.value)
			}
		})
	}
}

func TestIsNilNegative(t *testing.T) {
	t.Parallel()
	var nonNilInterface any = new(int)
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "channel", value: make(chan int)},
		{name: "function", value: func() {}},
		{name: "interface", value: nonNilInterface},
		{name: "map", value: map[string]int{"key": 1}},
		{name: "pointer", value: new(int)},
		{name: "slice", value: []int{1}},
		{name: "zero int", value: 0},
		{name: "empty string", value: ""},
		{name: "empty non-nil slice", value: []int{}},
		{name: "empty non-nil map", value: map[string]int{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if nilcheck.IsNil(test.value) {
				t.Fatalf("IsNil(%T) = true, want false", test.value)
			}
		})
	}
}

func TestIsNilBoundary(t *testing.T) {
	t.Parallel()
	var typedNil *int
	var wrapped any = typedNil
	for _, test := range []struct {
		name  string
		value any
		want  bool
	}{
		{name: "interface holding typed-nil pointer", value: wrapped, want: true},
		{name: "struct", value: struct{}{}, want: false},
		{name: "array", value: [1]int{}, want: false},
		{name: "empty array", value: [0]int{}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := nilcheck.IsNil(test.value); got != test.want {
				t.Fatalf("IsNil(%T) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}
