// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package wol

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type deadlineConn struct {
	net.Conn
	mu            sync.Mutex
	writeDeadline time.Time
	write         func([]byte) (int, error)
	setDeadline   func(time.Time) error
}

func (c *deadlineConn) Write(p []byte) (int, error) {
	if c.write != nil {
		return c.write(p)
	}
	return len(p), nil
}

func (*deadlineConn) Close() error { return nil }

func (c *deadlineConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.writeDeadline = deadline
	c.mu.Unlock()
	if c.setDeadline != nil {
		return c.setDeadline(deadline)
	}
	return nil
}

func (c *deadlineConn) deadline() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeDeadline
}

func TestWakeToContextBoundsBackgroundAndWrite(t *testing.T) {
	t.Parallel()
	conn := &deadlineConn{}
	var dialDeadline time.Time
	var dialRemaining time.Duration
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		require.Equal(t, "udp", network)
		require.Equal(t, "192.0.2.255:9", address)
		var ok bool
		dialDeadline, ok = ctx.Deadline()
		require.True(t, ok)
		dialRemaining = time.Until(dialDeadline)
		return conn, nil
	}

	err := wakeToContextWithDialer(
		context.Background(),
		"aa:bb:cc:dd:ee:ff",
		"192.0.2.255:9",
		dial,
	)

	require.NoError(t, err)
	require.WithinDuration(t, dialDeadline, conn.deadline(), time.Millisecond)
	require.Greater(t, dialRemaining, time.Duration(0))
	require.LessOrEqual(t, dialRemaining, DefaultTimeout)
}

func TestWakeToContextPreservesEarlierCallerDeadline(t *testing.T) {
	t.Parallel()
	wantDeadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), wantDeadline)
	defer cancel()
	conn := &deadlineConn{}
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		gotDeadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.WithinDuration(t, wantDeadline, gotDeadline, time.Millisecond)
		return conn, nil
	}

	err := wakeToContextWithDialer(ctx, "aa:bb:cc:dd:ee:ff", "192.0.2.255:9", dial)

	require.NoError(t, err)
	require.WithinDuration(t, wantDeadline, conn.deadline(), time.Millisecond)
}

func TestWakeToContextRejectsCanceledContextBeforeDial(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	dial := func(context.Context, string, string) (net.Conn, error) {
		called = true
		return &deadlineConn{}, nil
	}

	err := wakeToContextWithDialer(ctx, "aa:bb:cc:dd:ee:ff", "192.0.2.255:9", dial)

	require.ErrorIs(t, err, context.Canceled)
	require.False(t, called)
}

func TestWakeToContextCancellationInterruptsWrite(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	writeStarted := make(chan struct{})
	writeReleased := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(writeReleased) }) })
	conn := &deadlineConn{}
	conn.write = func([]byte) (int, error) {
		close(writeStarted)
		<-writeReleased
		return 0, os.ErrDeadlineExceeded
	}
	conn.setDeadline = func(deadline time.Time) error {
		if !deadline.After(time.Now()) {
			releaseOnce.Do(func() { close(writeReleased) })
		}
		return nil
	}
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return conn, nil
	}
	result := make(chan error, 1)
	go func() {
		result <- wakeToContextWithDialer(
			ctx,
			"aa:bb:cc:dd:ee:ff",
			"192.0.2.255:9",
			dial,
		)
	}()

	<-writeStarted
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, errors.Is(err, os.ErrDeadlineExceeded))
	case <-time.After(time.Second):
		t.Fatal("write did not stop after context cancellation")
	}
}
