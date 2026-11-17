// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package migrate_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/log"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

const lateProxyConnections = 64

type lateDBProxy struct {
	mu       sync.Mutex
	conns    []net.Conn
	workers  sync.WaitGroup
	rejected atomic.Int32
}

func TestModuleWaitsForLateDatabase(t *testing.T) {
	t.Parallel()
	dsn := pgtest.DSN(t)
	purl, err := url.Parse(dsn)
	require.NoError(t, err)
	proxy, addr := newLateDBProxy(t, purl.Host)
	purl.Host = addr
	opts := dbpgx.Options{
		DSN: purl.String(), ConnectTimeout: 5 * time.Second,
		Retry: dbpgx.RetryOptions{Attempts: 5, Initial: 10 * time.Millisecond, Max: 100 * time.Millisecond},
	}
	app := fxtest.New(t, moduleOptionsWithPGX(t, opts,
		dbmigrate.Options{Auto: true}.WithFS(testMigrationsFS()), log.New(log.Options{}))...)
	app.RequireStart()
	app.RequireStop()
	require.Equal(t, 2, stepProbeTables(t, dsn))
	require.EqualValues(t, 2, proxy.rejected.Load())
}

func newLateDBProxy(t *testing.T, target string) (*lateDBProxy, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), moduleQueryTimeout)
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	proxy := &lateDBProxy{conns: make([]net.Conn, 0, lateProxyConnections*2)}
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		proxy.mu.Lock()
		for _, conn := range proxy.conns {
			_ = conn.Close()
		}
		proxy.mu.Unlock()
		proxy.workers.Wait()
	})
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.NoError(t, listener.SetDeadline(deadline))
	proxy.workers.Go(func() {
		proxy.accept(ctx, t, listener, target)
	})
	return proxy, listener.Addr().String()
}

func (p *lateDBProxy) accept(ctx context.Context, t *testing.T, listener net.Listener, target string) {
	t.Helper()
	for attempt := range lateProxyConnections {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				t.Errorf("proxy accept: %v", err)
			}
			return
		}
		if !p.track(ctx, t, conn) {
			return
		}
		if attempt < 2 {
			p.rejected.Add(1)
			_ = conn.Close()
			continue
		}
		p.workers.Go(func() {
			p.forward(ctx, t, conn, target)
		})
	}
	t.Error("proxy connection bound exhausted")
}

func (p *lateDBProxy) track(ctx context.Context, t *testing.T, conn net.Conn) bool {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		_ = conn.Close()
		return false
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Error("proxy connection has no deadline")
		_ = conn.Close()
		return false
	}
	if err := conn.SetDeadline(deadline); err != nil {
		t.Errorf("proxy deadline: %v", err)
		_ = conn.Close()
		return false
	}
	p.conns = append(p.conns, conn)
	return true
}

func (p *lateDBProxy) forward(ctx context.Context, t *testing.T, client net.Conn, target string) {
	t.Helper()
	defer func() { _ = client.Close() }()
	dialer := &net.Dialer{}
	server, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		if ctx.Err() == nil {
			t.Errorf("proxy dial: %v", err)
		}
		return
	}
	if !p.track(ctx, t, server) {
		return
	}
	defer func() { _ = server.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		copyLateDB(ctx, t, server, client)
	}()
	copyLateDB(ctx, t, client, server)
	<-done
}

func copyLateDB(ctx context.Context, t *testing.T, dst, src net.Conn) {
	t.Helper()
	defer func() { _ = dst.Close() }()
	defer func() { _ = src.Close() }()
	if _, err := io.Copy(dst, src); err != nil && ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("proxy copy: %v", err)
	}
}
