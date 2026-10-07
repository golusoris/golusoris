// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package geofence_test

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golusoris/golusoris/httpx/geofence"
)

// fakeReader fakes a maxminddb lookup by IP → country code.
type fakeReader struct {
	byIP map[string]string
	err  error
}

func (f *fakeReader) Lookup(ip net.IP, result any) error {
	if f.err != nil {
		return f.err
	}
	rec, ok := result.(*geofence.Record)
	if !ok {
		return errors.New("bad result type")
	}
	if code, ok := f.byIP[ip.String()]; ok {
		rec.Country.ISOCode = code
	}
	return nil
}

func (f *fakeReader) Close() error { return nil }

func TestNewFromReaderTypedNilFailsClosed(t *testing.T) {
	t.Parallel()
	var reader *fakeReader
	middleware := geofence.NewFromReader(geofence.Options{Allow: []string{"US"}}, reader)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.5:12345"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestNoPolicyNoReaderIsNoop(t *testing.T) {
	t.Parallel()
	mw, reader, err := geofence.New(geofence.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if reader != nil {
		t.Error("expected nil reader for no-op")
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusTeapot {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestNoPolicyIgnoresDatabasePathAndReaderFailures(t *testing.T) {
	t.Parallel()

	mw, reader, err := geofence.New(geofence.Options{MmdbPath: "/missing/country.mmdb"})
	if err != nil {
		t.Fatalf("New(no policy): %v", err)
	}
	if reader != nil {
		t.Fatalf("reader = %T, want nil", reader)
	}
	for _, middleware := range []func(http.Handler) http.Handler{
		mw,
		geofence.NewFromReader(geofence.Options{}, &fakeReader{err: errors.New("lookup failed")}),
	} {
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "203.0.113.5:12345"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusTeapot {
			t.Fatalf("status = %d, want no-policy pass-through", recorder.Code)
		}
	}
}

func TestBlankPolicyEntriesRemainNoop(t *testing.T) {
	t.Parallel()

	_, reader, err := geofence.New(geofence.Options{
		MmdbPath: "/missing/country.mmdb",
		Allow:    []string{" ", ""},
	})
	if err != nil {
		t.Fatalf("New(blank policy): %v", err)
	}
	if reader != nil {
		t.Fatalf("reader = %T, want nil", reader)
	}
}

func TestPolicyWithoutMmdbErrors(t *testing.T) {
	t.Parallel()
	_, _, err := geofence.New(geofence.Options{Allow: []string{"US"}})
	if err == nil {
		t.Fatal("expected error when policy is set without MmdbPath")
	}
}

func TestAllowList(t *testing.T) {
	t.Parallel()
	fake := &fakeReader{byIP: map[string]string{
		"203.0.113.5":  "US",
		"198.51.100.1": "RU",
	}}
	mw := geofence.NewFromReader(
		geofence.Options{Allow: []string{"US", "CA"}},
		fake,
	)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:12345"
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("US should be allowed; status = %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.1:12345"
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("RU should be blocked; status = %d", rr.Code)
	}
}

func TestDenyList(t *testing.T) {
	t.Parallel()
	fake := &fakeReader{byIP: map[string]string{
		"203.0.113.5":  "US",
		"198.51.100.1": "KP",
	}}
	mw := geofence.NewFromReader(
		geofence.Options{Deny: []string{"KP"}},
		fake,
	)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:12345"
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("US should pass; status = %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.1:12345"
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("KP should be blocked; status = %d", rr.Code)
	}
}
