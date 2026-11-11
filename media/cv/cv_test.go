// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cv_test

import (
	"context"
	"errors"
	"testing"

	"github.com/golusoris/golusoris/media/cv"
)

func TestNewAnalyzer_stubReturnsError(t *testing.T) {
	t.Parallel()
	_, err := cv.NewAnalyzer(cv.Options{})
	if !errors.Is(err, cv.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}

func TestStub_DetectFaces(t *testing.T) {
	t.Parallel()
	a := newStubAnalyzer(t)
	_, err := a.DetectFaces(context.Background(), []byte{})
	if !errors.Is(err, cv.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}

func TestStub_DetectObjects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  []byte
	}{
		{name: "populated", src: []byte("image")},
		{name: "empty", src: []byte{}},
		{name: "nil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newStubAnalyzer(t)
			objects, err := a.DetectObjects(t.Context(), tc.src)
			if !errors.Is(err, cv.ErrCGORequired) {
				t.Fatalf("DetectObjects() error = %v, want ErrCGORequired", err)
			}
			if objects != nil {
				t.Fatalf("DetectObjects() = %v, want nil", objects)
			}
		})
	}
}

func TestStub_Thumbnail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		path   string
		offset float64
	}{
		{name: "populated", path: "video.mp4", offset: 1},
		{name: "empty"},
		{name: "negative_offset", path: "video.mp4", offset: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newStubAnalyzer(t)
			thumbnail, err := a.Thumbnail(t.Context(), tc.path, tc.offset)
			if !errors.Is(err, cv.ErrCGORequired) {
				t.Fatalf("Thumbnail() error = %v, want ErrCGORequired", err)
			}
			if thumbnail != nil {
				t.Fatalf("Thumbnail() = %v, want nil", thumbnail)
			}
		})
	}
}

func TestStub_Close(t *testing.T) {
	t.Parallel()
	a := newStubAnalyzer(t)
	a.Close()
	a.Close()
}

func newStubAnalyzer(t *testing.T) cv.Analyzer {
	t.Helper()
	a, err := cv.NewAnalyzer(cv.Options{})
	if !errors.Is(err, cv.ErrCGORequired) {
		t.Fatalf("NewAnalyzer() error = %v, want ErrCGORequired", err)
	}
	return a
}
