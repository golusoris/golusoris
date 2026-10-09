// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestValOrDefault_zero(t *testing.T) {
	t.Parallel()
	if got := valOrDefault(0, 5*time.Second); got != 5*time.Second {
		t.Fatalf("want 5s, got %v", got)
	}
}

func TestValOrDefault_negativePolicyDurations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		want time.Duration
	}{
		{name: "retry wait", want: defaultRetryWait},
		{name: "retry max wait", want: defaultRetryMaxWait},
		{name: "breaker open for", want: defaultBreakerOpenFor},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := valOrDefault(-time.Nanosecond, test.want); got != test.want {
				t.Fatalf("duration = %v, want %v", got, test.want)
			}
		})
	}
}

func TestValOrDefault_nonzero(t *testing.T) {
	t.Parallel()
	if got := valOrDefault(3*time.Second, 5*time.Second); got != 3*time.Second {
		t.Fatalf("want 3s, got %v", got)
	}
}

func TestValOrDefaultU32_zero(t *testing.T) {
	t.Parallel()
	if got := valOrDefaultU32(0, 1); got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}

func TestValOrDefaultU32_nonzero(t *testing.T) {
	t.Parallel()
	if got := valOrDefaultU32(3, 1); got != 3 {
		t.Fatalf("want 3, got %d", got)
	}
}

func TestErrServerError(t *testing.T) {
	t.Parallel()
	err := errServerError(503)
	if err == nil {
		t.Fatal("want non-nil error")
	}
	if !errors.Is(err, errServerErrorSentinel) {
		t.Fatalf("want error to wrap errServerErrorSentinel, got %v", err)
	}
}

// TestInnerTransportOwnsDefault pins that New never sends on
// http.DefaultTransport unless the caller hands it in (#703).
func TestInnerTransportOwnsDefault(t *testing.T) {
	t.Parallel()
	first, ok := innerTransport(Options{}).(*http.Transport)
	if !ok || first == http.DefaultTransport {
		t.Fatalf("default transport = %T shared=%v, want a private *http.Transport", first, first == http.DefaultTransport)
	}
	if second := innerTransport(Options{}); second == first {
		t.Fatal("two clients share one transport")
	}
	if got := innerTransport(Options{Transport: http.DefaultTransport}); got != http.DefaultTransport {
		t.Fatalf("explicit transport = %T, want the caller's http.DefaultTransport", got)
	}
}

func TestNew_defaults(t *testing.T) {
	t.Parallel()
	c := New(Options{})
	if c == nil {
		t.Fatal("want non-nil *http.Client")
	}
}

func TestNewDefaultsNegativeRetryDurations(t *testing.T) {
	t.Parallel()
	c := New(Options{
		Retry: RetryOptions{
			Max:     1,
			Wait:    -time.Nanosecond,
			MaxWait: -time.Nanosecond,
		},
	})
	retry, ok := c.Transport.(*retryTransport)
	if !ok {
		t.Fatalf("transport = %T, want *retryTransport", c.Transport)
	}
	if retry.rc.RetryWaitMin != 500*time.Millisecond {
		t.Errorf("RetryWaitMin = %v, want 500ms", retry.rc.RetryWaitMin)
	}
	if retry.rc.RetryWaitMax != 10*time.Second {
		t.Errorf("RetryWaitMax = %v, want 10s", retry.rc.RetryWaitMax)
	}
}

func TestDrain_nil(t *testing.T) {
	t.Parallel()
	// must not panic
	Drain(context.Background(), nil)
}

func TestDrain_response(t *testing.T) {
	t.Parallel()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("hello"))}
	Drain(context.Background(), resp)
	// body should be fully drained; a subsequent read yields 0 bytes
	n, _ := resp.Body.Read(make([]byte, 16))
	if n != 0 {
		t.Fatalf("want 0 bytes after Drain, got %d", n)
	}
}

type countingBody struct {
	remaining int64
	read      int64
	closes    int
}

func (b *countingBody) Read(dst []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(dst)), b.remaining)
	for i := range int(n) {
		dst[i] = 'x'
	}
	b.remaining -= n
	b.read += n
	return int(n), nil
}

func (b *countingBody) Close() error {
	b.closes++
	return nil
}

func TestDrainHasFiniteByteBound(t *testing.T) {
	t.Parallel()
	body := &countingBody{remaining: maxDrainBytes + 1}
	Drain(context.Background(), &http.Response{Body: body})
	if body.read != maxDrainBytes {
		t.Fatalf("drained bytes = %d, want %d", body.read, maxDrainBytes)
	}
	if body.closes != 1 {
		t.Fatalf("body closes = %d, want 1", body.closes)
	}
}

type blockingBody struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
	closes      atomic.Int32
}

func newBlockingBody() *blockingBody {
	return &blockingBody{started: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingBody) Read([]byte) (int, error) {
	b.startedOnce.Do(func() { close(b.started) })
	<-b.release
	return 0, errors.New("body closed")
}

func (b *blockingBody) Close() error {
	b.closes.Add(1)
	b.releaseOnce.Do(func() { close(b.release) })
	return nil
}

func TestDrainCancellationClosesBlockedBody(t *testing.T) {
	t.Parallel()
	body := newBlockingBody()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Drain(ctx, &http.Response{Body: body})
		close(done)
	}()
	<-body.started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = body.Close()
		<-done
		t.Fatal("Drain did not stop after context cancellation")
	}
	select {
	case <-body.release:
	case <-time.After(time.Second):
		_ = body.Close()
		t.Fatal("Drain did not invoke Body.Close after context cancellation")
	}
	if body.closes.Load() != 1 {
		t.Fatalf("body closes = %d, want 1", body.closes.Load())
	}
}

type blockingCloseBody struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingCloseBody) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (b *blockingCloseBody) Close() error {
	close(b.started)
	<-b.release
	return nil
}

func TestDrainDeadlineBoundsBlockedClose(t *testing.T) {
	t.Parallel()
	body := &blockingCloseBody{started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		Drain(ctx, &http.Response{Body: body})
		close(done)
	}()

	<-body.started
	select {
	case <-done:
	case <-time.After(time.Second):
		close(body.release)
		<-done
		t.Fatal("Drain blocked on Body.Close past its context deadline")
	}
	close(body.release)
}
