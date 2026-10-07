// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var (
	errFlushFixture  = errors.New("flush fixture")
	errHijackFixture = errors.New("hijack fixture")
)

type optionalResponseWriter struct {
	*httptest.ResponseRecorder
	flushed bool
	pushed  string
}

type failingFlushWriter struct{ *httptest.ResponseRecorder }

type plainResponseWriter struct {
	header http.Header
	body   bytes.Buffer
}

type headerEvent struct {
	status int
	header http.Header
}

type sequenceResponseWriter struct {
	header http.Header
	events []headerEvent
	body   bytes.Buffer
	final  bool
}

type recordingHijackWriter struct {
	header http.Header
	conn   net.Conn
	events []string
}

func (w *sequenceResponseWriter) Header() http.Header { return w.header }

func (w *sequenceResponseWriter) WriteHeader(status int) {
	w.events = append(w.events, headerEvent{status: status, header: w.header.Clone()})
	if status >= http.StatusOK {
		w.final = true
	}
}

func (w *sequenceResponseWriter) Write(body []byte) (int, error) {
	if !w.final {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(body)
}

func (w *recordingHijackWriter) Header() http.Header { return w.header }

func (w *recordingHijackWriter) WriteHeader(status int) {
	w.events = append(w.events, fmt.Sprintf("header:%d", status))
}

func (w *recordingHijackWriter) Write(body []byte) (int, error) {
	w.events = append(w.events, "write")
	return len(body), nil
}

func (w *recordingHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.events = append(w.events, "hijack")
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func (w *plainResponseWriter) Header() http.Header { return w.header }

func (*plainResponseWriter) WriteHeader(int) {}

func (w *plainResponseWriter) Write(body []byte) (int, error) { return w.body.Write(body) }

func (*failingFlushWriter) FlushError() error { return errFlushFixture }

func (w *optionalResponseWriter) Flush() { w.flushed = true }

func (*optionalResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errHijackFixture
}

func (w *optionalResponseWriter) Push(target string, _ *http.PushOptions) error {
	w.pushed = target
	return nil
}

func (w *optionalResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	return io.Copy(w.ResponseRecorder, reader)
}

func TestEtagRecorder_WriteHeader(t *testing.T) {
	t.Parallel()
	e, writer := newETagRecorder(httptest.NewRecorder(), 1024)
	writer.WriteHeader(http.StatusCreated)
	if e.status != http.StatusCreated {
		t.Errorf("status = %d, want %d", e.status, http.StatusCreated)
	}
}

func TestStatusOrDefault_zero(t *testing.T) {
	t.Parallel()
	if got := statusOrDefault(0); got != http.StatusOK {
		t.Errorf("statusOrDefault(0) = %d, want %d", got, http.StatusOK)
	}
}

func TestStatusOrDefault_nonzero(t *testing.T) {
	t.Parallel()
	if got := statusOrDefault(404); got != 404 {
		t.Errorf("statusOrDefault(404) = %d, want 404", got)
	}
}

func TestStatusRecorder_WriteHeader(t *testing.T) {
	t.Parallel()
	s, writer := newTrackingWriter(httptest.NewRecorder())
	writer.WriteHeader(http.StatusCreated)
	if s.status != http.StatusCreated {
		t.Errorf("status = %d, want %d", s.status, http.StatusCreated)
	}
}

func TestRecordersForwardInformationalThenFinalStatus(t *testing.T) {
	t.Parallel()
	underlying := &sequenceResponseWriter{header: make(http.Header)}
	state, writer := newTrackingWriter(underlying)
	writer.WriteHeader(http.StatusEarlyHints)
	writer.WriteHeader(http.StatusCreated)
	if _, err := writer.Write([]byte("created")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if state.status != http.StatusCreated {
		t.Fatalf("tracked status = %d, want %d", state.status, http.StatusCreated)
	}
	if len(underlying.events) != 2 || underlying.events[0].status != http.StatusEarlyHints ||
		underlying.events[1].status != http.StatusCreated {
		t.Fatalf("status sequence = %+v, want [103 201]", underlying.events)
	}
}

func TestRecordersTreatSwitchingProtocolsAsFinal(t *testing.T) {
	t.Parallel()
	underlying := &sequenceResponseWriter{header: make(http.Header)}
	state, writer := newTrackingWriter(underlying)
	writer.WriteHeader(http.StatusSwitchingProtocols)
	if state.status != http.StatusSwitchingProtocols || !state.committed() {
		t.Fatalf("tracked status = %d committed = %t", state.status, state.committed())
	}

	etagState, etagWriter := newETagRecorder(
		&sequenceResponseWriter{header: make(http.Header)}, 1024,
	)
	etagWriter.WriteHeader(http.StatusSwitchingProtocols)
	if etagState.status != http.StatusSwitchingProtocols || !etagState.passthrough {
		t.Fatalf("ETag status = %d passthrough = %t", etagState.status, etagState.passthrough)
	}
}

func TestETagPreservesFinalHeaderSnapshotAfterEarlyHints(t *testing.T) {
	t.Parallel()
	underlying := &sequenceResponseWriter{header: make(http.Header)}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Set("X-Snapshot", "final")
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("X-Snapshot", "late")
		_, err := w.Write([]byte("created"))
		if err != nil {
			t.Errorf("write: %v", err)
		}
	})
	serveETag(underlying, httptest.NewRequest(http.MethodGet, "/", nil), handler, 1024)
	if len(underlying.events) != 2 || underlying.events[0].status != http.StatusEarlyHints ||
		underlying.events[1].status != http.StatusCreated {
		t.Fatalf("status sequence = %+v, want [103 201]", underlying.events)
	}
	if got := underlying.events[1].header.Get("X-Snapshot"); got != "final" {
		t.Fatalf("final X-Snapshot = %q, want final", got)
	}
}

func TestETagFirstCallHijackDoesNotCommitHTTPResponse(t *testing.T) {
	t.Parallel()
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = peerConn.Close()
	})
	underlying := &recordingHijackWriter{header: make(http.Header), conn: serverConn}
	state, writer := newETagRecorder(underlying, 1024)
	hijacker := writer.(http.Hijacker) //nolint:forcetypeassert // contract under test
	conn, readWriter, err := hijacker.Hijack()
	if err != nil {
		t.Fatalf("Hijack: %v", err)
	}
	if conn != serverConn || readWriter == nil {
		t.Fatalf("hijack result = (%v, %v), want underlying connection", conn, readWriter)
	}
	if len(underlying.events) != 1 || underlying.events[0] != "hijack" {
		t.Fatalf("events = %v, want raw hijack without header write", underlying.events)
	}
	if !state.passthrough || state.status != 0 {
		t.Fatalf("state passthrough = %t status = %d, want true and uncommitted", state.passthrough, state.status)
	}
}

func TestStatusRecorderPreservesOptionalInterfaces(t *testing.T) {
	t.Parallel()
	underlying := &optionalResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	recorder, writer := newTrackingWriter(underlying)
	writer.(http.Flusher).Flush() //nolint:forcetypeassert // contract under test
	if !underlying.flushed {
		t.Fatal("Flush was not delegated")
	}
	_, _, err := writer.(http.Hijacker).Hijack() //nolint:forcetypeassert // contract under test
	if !errors.Is(err, errHijackFixture) {
		t.Fatalf("Hijack error = %v", err)
	}
	if err := writer.(http.Pusher).Push("/asset", nil); err != nil { //nolint:forcetypeassert // contract under test
		t.Fatalf("Push error = %v", err)
	}
	if underlying.pushed != "/asset" {
		t.Fatalf("pushed = %q", underlying.pushed)
	}
	readerFrom := writer.(io.ReaderFrom) //nolint:forcetypeassert // contract under test
	if _, err := readerFrom.ReadFrom(strings.NewReader("body")); err != nil {
		t.Fatalf("ReadFrom error = %v", err)
	}
	if recorder.bytes != 4 || underlying.Body.String() != "body" {
		t.Fatalf("bytes = %d body = %q", recorder.bytes, underlying.Body.String())
	}
	if writer.(interface{ Unwrap() http.ResponseWriter }).Unwrap() != underlying { //nolint:forcetypeassert // contract under test
		t.Fatal("Unwrap did not return underlying writer")
	}
}

func TestRecordersDoNotFabricateOptionalInterfaces(t *testing.T) {
	t.Parallel()
	plain := &plainResponseWriter{header: make(http.Header)}

	_, tracking := newTrackingWriter(plain)
	_, etag := newETagRecorder(plain, 1024)
	for name, writer := range map[string]http.ResponseWriter{"tracking": tracking, "etag": etag} {
		if _, ok := writer.(http.Flusher); ok {
			t.Errorf("%s writer fabricated http.Flusher", name)
		}
		if _, ok := writer.(http.Hijacker); ok {
			t.Errorf("%s writer fabricated http.Hijacker", name)
		}
		if _, ok := writer.(http.Pusher); ok {
			t.Errorf("%s writer fabricated http.Pusher", name)
		}
		if _, ok := writer.(io.ReaderFrom); ok {
			t.Errorf("%s writer fabricated io.ReaderFrom", name)
		}
	}
}

func TestRecordersRetainPlainFlushFailure(t *testing.T) {
	t.Parallel()

	tracking, trackingWriter := newTrackingWriter(&failingFlushWriter{httptest.NewRecorder()})
	trackingWriter.(http.Flusher).Flush() //nolint:forcetypeassert // contract under test
	if !errors.Is(tracking.flushErr, errFlushFixture) {
		t.Fatalf("tracking flush error = %v, want %v", tracking.flushErr, errFlushFixture)
	}

	etag, etagWriter := newETagRecorder(&failingFlushWriter{httptest.NewRecorder()}, 1024)
	etagWriter.(http.Flusher).Flush() //nolint:forcetypeassert // contract under test
	if !errors.Is(etag.flushErr, errFlushFixture) {
		t.Fatalf("ETag flush error = %v, want %v", etag.flushErr, errFlushFixture)
	}
}

// TestRewriteRemoteAddrFromXFF_untrustedPeer: no nets configured -> no-op.
func TestRewriteRemoteAddrFromXFF_noTrustedNets(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.1.2.3:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.5")
	rewriteRemoteAddrFromXFF(r, nil)
	if r.RemoteAddr != "10.1.2.3:1234" {
		t.Errorf("RemoteAddr = %q, want unchanged", r.RemoteAddr)
	}
}

func TestRewriteRemoteAddrFromXFF_ignoresSpoofedLeftEntries(t *testing.T) {
	t.Parallel()
	nets := parseCIDRs([]string{"10.0.0.0/8"})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.3:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.99, 203.0.113.5, 10.0.0.2")
	rewriteRemoteAddrFromXFF(r, nets)
	if r.RemoteAddr != "203.0.113.5" {
		t.Errorf("RemoteAddr = %q, want first untrusted hop 203.0.113.5", r.RemoteAddr)
	}
}

func TestRewriteRemoteAddrFromXFF_invalidChainFailsClosed(t *testing.T) {
	t.Parallel()
	nets := parseCIDRs([]string{"10.0.0.0/8"})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.3:1234"
	r.Header.Set("X-Forwarded-For", "spoofed, 10.0.0.2")
	rewriteRemoteAddrFromXFF(r, nets)
	if r.RemoteAddr != "10.0.0.3:1234" {
		t.Errorf("RemoteAddr = %q, want unchanged", r.RemoteAddr)
	}
}

// TestRewriteRemoteAddrFromXFF_noHeader: trusted peer but no
// X-Forwarded-For header present -> no-op.
func TestRewriteRemoteAddrFromXFF_noHeader(t *testing.T) {
	t.Parallel()
	nets := parseCIDRs([]string{"10.0.0.0/8"})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.1.2.3:1234"
	rewriteRemoteAddrFromXFF(r, nets)
	if r.RemoteAddr != "10.1.2.3:1234" {
		t.Errorf("RemoteAddr = %q, want unchanged", r.RemoteAddr)
	}
}

// Empty chain entries invalidate the whole forwarded chain.
func TestRewriteRemoteAddrFromXFF_emptyAfterTrim(t *testing.T) {
	t.Parallel()
	nets := parseCIDRs([]string{"10.0.0.0/8"})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.1.2.3:1234"
	r.Header.Set("X-Forwarded-For", "   ,10.0.0.1")
	rewriteRemoteAddrFromXFF(r, nets)
	if r.RemoteAddr != "10.1.2.3:1234" {
		t.Errorf("RemoteAddr = %q, want unchanged", r.RemoteAddr)
	}
}

func TestForwardedClientIPBoundsTrustedHopScan(t *testing.T) {
	t.Parallel()
	trusted := parseCIDRs([]string{"10.0.0.0/8"})
	hops := make([]string, maxForwardedHops+1)
	for i := range hops {
		hops[i] = fmt.Sprintf("10.0.0.%d", i+1)
	}
	if ip, ok := forwardedClientIP(strings.Join(hops, ","), trusted); ok {
		t.Fatalf("forwardedClientIP() = %q, true; want bounded rejection", ip)
	}
}

func TestForwardedClientIPDoesNotScanSpoofedPrefix(t *testing.T) {
	t.Parallel()
	trusted := parseCIDRs([]string{"10.0.0.0/8"})
	xff := strings.Repeat("spoofed,", maxForwardedHops*1024) +
		"203.0.113.5,10.0.0.2"
	ip, ok := forwardedClientIP(xff, trusted)
	if !ok || ip != "203.0.113.5" {
		t.Fatalf("forwardedClientIP() = %q, %t; want 203.0.113.5, true", ip, ok)
	}
}
