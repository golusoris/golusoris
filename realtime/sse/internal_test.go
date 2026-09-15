// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sse

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

// flushRecorder wraps httptest.ResponseRecorder and counts Flush calls.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushRecorder) Flush() { f.flushes++ }

// failWriter always fails Write; it embeds a ResponseRecorder so it still
// satisfies http.ResponseWriter (Write is shadowed below).
type failWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (f *failWriter) Write([]byte) (int, error) { return 0, f.err }

func TestSetSSEHeaders(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	setSSEHeaders(rec)
	cases := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	}
	for header, want := range cases {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// TestHub_writeEvent_success proves a well-formed event is written to w and
// flushed, and the stream continues (returns true).
func TestHub_writeEvent_success(t *testing.T) {
	t.Parallel()
	h := NewHub(slog.New(slog.DiscardHandler))
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	ok := h.writeEvent(context.Background(), rec, rec, Event{Event: "ping", Data: "hi"})
	if !ok {
		t.Fatal("writeEvent returned false, want true")
	}
	if rec.flushes != 1 {
		t.Errorf("flushes = %d, want 1", rec.flushes)
	}
	if body := rec.Body.String(); !strings.Contains(body, "event: ping") || !strings.Contains(body, "data: hi") {
		t.Errorf("body = %q", body)
	}
}

// TestHub_writeEvent_formatError proves a format failure (unmarshalable
// Data) is logged and skipped — the stream continues (returns true) and
// nothing is written.
func TestHub_writeEvent_formatError(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	h := NewHub(slog.New(slog.NewTextHandler(&logs, nil)))
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	ok := h.writeEvent(context.Background(), rec, rec, Event{Data: make(chan int)})
	if !ok {
		t.Fatal("writeEvent returned false, want true (format error is not fatal)")
	}
	if rec.flushes != 0 {
		t.Errorf("flushes = %d, want 0", rec.flushes)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "sse: format event") {
		t.Errorf("format error not logged: %q", logs.String())
	}
}

// TestHub_writeEvent_writeError is the boundary where the event formats
// successfully but the write to w fails: the stream must stop (false).
func TestHub_writeEvent_writeError(t *testing.T) {
	t.Parallel()
	h := NewHub(slog.New(slog.DiscardHandler))
	fw := &failWriter{ResponseRecorder: httptest.NewRecorder(), err: errors.New("client gone")}
	ok := h.writeEvent(context.Background(), fw, &flushRecorder{ResponseRecorder: fw.ResponseRecorder}, Event{Data: "x"})
	if ok {
		t.Fatal("writeEvent returned true, want false on write failure")
	}
}
