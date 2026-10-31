// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpclient

import (
	"net/http"
	"runtime"
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
