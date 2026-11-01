// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"bytes"
	"log/slog"
	"math/big"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
)

// safeBuffer captures log entries concurrently for assertion.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func serverSerial(t *testing.T, conn *grpc.ClientConn) *big.Int {
	t.Helper()
	var p peer.Peer
	// Read server serial from TLS connection state using a lightweight RPC.
	err := sleepRPC(conn, 0, grpc.Peer(&p))
	require.NoError(t, err)
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	require.True(t, ok, "peer AuthInfo must be credentials.TLSInfo")
	require.NotEmpty(t, tlsInfo.State.PeerCertificates, "peer must present TLS certificates")
	return tlsInfo.State.PeerCertificates[0].SerialNumber
}

func TestCertRotation_HeldRPCFinishesNewConnUsesNewCert(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	first := ca.Server(t, "127.0.0.1")
	files := ca.WriteFiles(t, t.TempDir(), first)
	cliReloader, err := tlsx.NewReloader(ca.WriteFiles(t, t.TempDir(), ca.Client(t, "client")), tlsx.Options{})
	require.NoError(t, err)
	cliCreds := credentials.NewTLS(cliReloader.ClientConfig(""))
	cfg := Config{
		TLS:        true,
		CertFile:   files.Cert,
		KeyFile:    files.Key,
		CAFile:     files.CA,
		ClientAuth: "require_and_verify",
	}
	addr := startServerWithOptions(t, cfg, slog.New(slog.DiscardHandler), clk, nil)
	conn1 := dial(t, addr, cliCreds)
	require.Zero(t, serverSerial(t, conn1).Cmp(first.Serial), "initial conn must see first serial")

	heldDone := make(chan error, 1)
	// Hold an RPC in flight while cert files rotate on disk.
	go func() {
		heldDone <- sleepRPC(conn1, 2*time.Second)
	}()
	// Ensure the held RPC is active on the wire before cert rotation.
	time.Sleep(50 * time.Millisecond)

	second := ca.Server(t, "127.0.0.1")
	tlsxtest.WritePair(t, files, second)
	clk.Advance(31 * time.Second)

	conn2 := dial(t, addr, cliCreds)
	require.Zero(t, serverSerial(t, conn2).Cmp(second.Serial), "new conn must see rotated serial")
	select {
	case err := <-heldDone:
		t.Fatalf("held RPC ended before rotation completed: %v", err)
	default:
	}
	require.Zero(t, serverSerial(t, conn1).Cmp(first.Serial), "existing conn must keep its handshake")
	require.NoError(t, <-heldDone, "held RPC must finish successfully")
}

func TestCertRotation_CorruptReplacementKeepsOldCertAndLogs(t *testing.T) {
	t.Parallel()
	var logBuf safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	first := ca.Server(t, "127.0.0.1")
	files := ca.WriteFiles(t, t.TempDir(), first)
	cliReloader, err := tlsx.NewReloader(ca.WriteFiles(t, t.TempDir(), ca.Client(t, "client")), tlsx.Options{})
	require.NoError(t, err)
	cliCreds := credentials.NewTLS(cliReloader.ClientConfig(""))
	cfg := Config{
		TLS:        true,
		CertFile:   files.Cert,
		KeyFile:    files.Key,
		CAFile:     files.CA,
		ClientAuth: "require_and_verify",
	}
	addr := startServerWithOptions(t, cfg, logger, clk, nil)
	conn1 := dial(t, addr, cliCreds)
	require.Zero(t, serverSerial(t, conn1).Cmp(first.Serial), "initial conn must see first serial")

	// Corrupt replacement pair with a mismatched key to trigger reload failure.
	corrupt := ca.Server(t, "127.0.0.1")
	require.NoError(t, os.WriteFile(files.Cert, corrupt.CertPEM, 0o600))
	clk.Advance(31 * time.Second)

	conn2 := dial(t, addr, cliCreds)
	require.Zero(t, serverSerial(t, conn2).Cmp(first.Serial), "new conn must keep old serial after corrupt replacement")

	logs := logBuf.String()
	require.Regexp(t, `level=WARN msg="tlsx: reload failed; keeping last good certificate"`, logs)
}
