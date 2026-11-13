// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpclient

import (
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeTB struct {
	testing.TB
	failed bool
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Fatal(_ ...any) {
	f.failed = true
	runtime.Goexit()
}

func (f *fakeTB) Fatalf(_ string, _ ...any) {
	f.failed = true
	runtime.Goexit()
}

func runInFakeTB(fn func(tb testing.TB)) bool {
	fake := &fakeTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(fake)
	}()
	<-done
	return fake.failed
}

func TestAssertBoundedClone_Positive(t *testing.T) {
	t.Parallel()
	source := &http.Client{Transport: http.DefaultTransport}
	clone := &http.Client{Transport: http.DefaultTransport, Timeout: 10 * time.Second}
	AssertBoundedClone(t, source, clone, 10*time.Second)
}

type boundedCloneNegativeCase struct {
	name    string
	source  func() *http.Client
	clone   func(src *http.Client) *http.Client
	timeout time.Duration
}

var boundedCloneNegativeCases = []boundedCloneNegativeCase{
	{
		name: "same pointer",
		source: func() *http.Client {
			return &http.Client{Transport: http.DefaultTransport}
		},
		clone: func(src *http.Client) *http.Client {
			return src
		},
		timeout: 10 * time.Second,
	},
	{
		name: "mutated source timeout",
		source: func() *http.Client {
			return &http.Client{Transport: http.DefaultTransport, Timeout: 5 * time.Second}
		},
		clone: func(_ *http.Client) *http.Client {
			return &http.Client{Transport: http.DefaultTransport, Timeout: 10 * time.Second}
		},
		timeout: 10 * time.Second,
	},
	{
		name: "wrong timeout",
		source: func() *http.Client {
			return &http.Client{Transport: http.DefaultTransport}
		},
		clone: func(_ *http.Client) *http.Client {
			return &http.Client{Transport: http.DefaultTransport, Timeout: 5 * time.Second}
		},
		timeout: 10 * time.Second,
	},
	{
		name: "swapped transport",
		source: func() *http.Client {
			return &http.Client{Transport: http.DefaultTransport}
		},
		clone: func(_ *http.Client) *http.Client {
			return &http.Client{Transport: &http.Transport{}, Timeout: 10 * time.Second}
		},
		timeout: 10 * time.Second,
	},
}

// checkBoundedCloneNegativeCase asserts that invalid clones trigger failure in AssertBoundedClone.
func checkBoundedCloneNegativeCase(t *testing.T, tc boundedCloneNegativeCase) {
	t.Helper()
	src := tc.source()
	cln := tc.clone(src)
	failed := runInFakeTB(func(tb testing.TB) {
		tb.Helper()
		AssertBoundedClone(tb, src, cln, tc.timeout)
	})
	if !failed {
		t.Fatalf("case %q: expected failure", tc.name)
	}
}

func TestAssertBoundedClone_Negatives(t *testing.T) {
	t.Parallel()
	for _, tc := range boundedCloneNegativeCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkBoundedCloneNegativeCase(t, tc)
		})
	}
}

func TestAssertBoundedClone_BoundaryZeroTimeout(t *testing.T) {
	t.Parallel()
	t.Run("distinct instances pass", func(t *testing.T) {
		t.Parallel()
		source := &http.Client{Transport: http.DefaultTransport}
		clone := &http.Client{Transport: http.DefaultTransport, Timeout: 0}
		AssertBoundedClone(t, source, clone, 0)
	})
	t.Run("same pointer fails", func(t *testing.T) {
		t.Parallel()
		source := &http.Client{Transport: http.DefaultTransport}
		failed := runInFakeTB(func(tb testing.TB) {
			tb.Helper()
			AssertBoundedClone(tb, source, source, 0)
		})
		if !failed {
			t.Fatal("expected failure when clone == source with zero timeout")
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error { c.closed = true; return nil }

func newRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://provider.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestRoundTripError_ReturnsFailure(t *testing.T) {
	t.Parallel()
	cause := errors.New("upstream failed")
	err := RoundTripError(t, roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause }), newRequest(t))
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want %v", err, cause)
	}
}

func TestRoundTripError_ResponseFailsAndCloses(t *testing.T) {
	t.Parallel()
	for _, withBody := range []bool{true, false} {
		body := &closeTracker{Reader: strings.NewReader("ok")}
		req := newRequest(t)
		failed := runInFakeTB(func(tb testing.TB) {
			tb.Helper()
			resp := &http.Response{StatusCode: http.StatusOK}
			if withBody {
				resp.Body = body
			}
			_ = RoundTripError(tb, roundTripFunc(func(*http.Request) (*http.Response, error) { return resp, nil }), req)
		})
		if !failed || body.closed != withBody {
			t.Fatalf("body=%t: failed=%t closed=%t", withBody, failed, body.closed)
		}
	}
}

func TestRoundTripError_BoundaryNilResponseNilError(t *testing.T) {
	t.Parallel()
	// A contract-breaking transport yields nil; callers assert the error themselves.
	//nolint:nilnil // WHY: the boundary under test is a transport returning neither response nor error.
	silent := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, nil })
	err := RoundTripError(t, silent, newRequest(t))
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
}
