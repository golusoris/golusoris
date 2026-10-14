// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"crypto/tls"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/credentials"

	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
)

func loopbackConnPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	type acceptRes struct {
		conn net.Conn
		err  error
	}
	ch := make(chan acceptRes, 1)
	go func() {
		c, acceptErr := ln.Accept()
		ch <- acceptRes{conn: c, err: acceptErr}
	}()

	clientConn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)

	res := <-ch
	require.NoError(t, res.err)
	return res.conn, clientConn
}

func TestReloadingCreds_ClientHandshakeSuccessAndFailure(t *testing.T) {
	t.Parallel()

	ca := tlsxtest.NewCA(t)
	serverPair := ca.Server(t, "localhost", "127.0.0.1")
	dir := t.TempDir()
	serverFiles := ca.WriteFiles(t, dir, serverPair)
	clientFiles := tlsx.Files{CA: serverFiles.CA}

	serverReloader, err := tlsx.NewReloader(serverFiles, tlsx.Options{})
	require.NoError(t, err)
	serverCreds := credentials.NewTLS(serverReloader.ServerConfig(tls.NoClientCert))

	clientReloader, err := tlsx.NewReloader(clientFiles, tlsx.Options{})
	require.NoError(t, err)
	clientCreds := reloadingCreds{reloader: clientReloader}

	// Positive: handshake against matching authority succeeds
	t.Run("success", func(t *testing.T) {
		t.Parallel()
		serverRaw, clientRaw := loopbackConnPair(t)
		defer serverRaw.Close()
		defer clientRaw.Close()

		serverErrCh := make(chan error, 1)
		go func() {
			sConn, _, sErr := serverCreds.ServerHandshake(serverRaw)
			if sConn != nil {
				_ = sConn.Close()
			}
			serverErrCh <- sErr
		}()

		cConn, authInfo, cErr := clientCreds.ClientHandshake(t.Context(), "localhost", clientRaw)
		if cConn != nil {
			_ = cConn.Close()
		}
		require.NoError(t, cErr)
		require.NoError(t, <-serverErrCh)
		require.NotNil(t, authInfo)
		require.Equal(t, "tls", authInfo.AuthType())
	})

	// Negative: handshake against wrong server name fails
	t.Run("wrong server name", func(t *testing.T) {
		t.Parallel()
		serverRaw, clientRaw := loopbackConnPair(t)
		defer serverRaw.Close()
		defer clientRaw.Close()

		serverErrCh := make(chan error, 1)
		go func() {
			sConn, _, sErr := serverCreds.ServerHandshake(serverRaw)
			if sConn != nil {
				_ = sConn.Close()
			}
			serverErrCh <- sErr
		}()

		cConn, _, cErr := clientCreds.ClientHandshake(t.Context(), "wrong.example.invalid", clientRaw)
		if cConn != nil {
			_ = cConn.Close()
		}
		require.Error(t, cErr)
		<-serverErrCh
	})
}

func TestReloadingCreds_ServerHandshake(t *testing.T) {
	t.Parallel()

	ca := tlsxtest.NewCA(t)
	pair := ca.Server(t, "localhost")
	files := ca.WriteFiles(t, t.TempDir(), pair)

	reloader, err := tlsx.NewReloader(files, tlsx.Options{})
	require.NoError(t, err)
	creds := reloadingCreds{reloader: reloader}

	serverConn, clientConn := loopbackConnPair(t)
	defer serverConn.Close()
	_ = clientConn.Close()

	_, _, err = creds.ServerHandshake(serverConn)
	require.Error(t, err)
}

func TestReloadingCreds_InfoAndCloneAndOverride(t *testing.T) {
	t.Parallel()

	ca := tlsxtest.NewCA(t)
	pair := ca.Server(t, "localhost")
	files := ca.WriteFiles(t, t.TempDir(), pair)

	reloader, err := tlsx.NewReloader(files, tlsx.Options{})
	require.NoError(t, err)
	creds := reloadingCreds{reloader: reloader}

	// Info
	info := creds.Info()
	require.Equal(t, "tls", info.SecurityProtocol)

	// Clone independence
	cloned := creds.Clone()
	require.NotNil(t, cloned)
	clonedReloading, ok := cloned.(reloadingCreds)
	require.True(t, ok)
	require.Equal(t, creds.reloader, clonedReloading.reloader)

	// OverrideServerName
	require.NoError(t, creds.OverrideServerName("anything"))
}
