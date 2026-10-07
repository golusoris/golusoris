// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc/credentials"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/tlsx"
)

var errTLSPair = errors.New("grpc: tls requires cert_file and key_file")

// serverCredentials builds TLS 1.3 credentials whose certificate and client CA
// pool reload from disk on handshake.
func serverCredentials(cfg Config, logger *slog.Logger, clk clock.Clock) (credentials.TransportCredentials, error) {
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, errTLSPair
	}
	auth, err := tlsx.ParseClientAuth(cfg.ClientAuth, cfg.CAFile != "")
	if err != nil {
		return nil, fmt.Errorf("grpc: tls client_auth: %w", err)
	}
	files := tlsx.Files{Cert: cfg.CertFile, Key: cfg.KeyFile, CA: cfg.CAFile}
	reloader, err := tlsx.NewReloader(files, tlsx.Options{Clock: clk, Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("grpc: load tls cert: %w", err)
	}
	return credentials.NewTLS(reloader.ServerConfig(auth)), nil
}

// reloadingCreds rebuilds TLS credentials for each new client connection so a
// rotated CA bundle reaches the next handshake; the client certificate already
// follows the files through GetClientCertificate.
type reloadingCreds struct {
	reloader *tlsx.Reloader
}

func (c reloadingCreds) current() credentials.TransportCredentials {
	return credentials.NewTLS(c.reloader.ClientConfig(""))
}

func (c reloadingCreds) ClientHandshake(ctx context.Context, authority string, raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return c.current().ClientHandshake(ctx, authority, raw) //nolint:wrapcheck // grpc-go type-switches on Temporary/Timeout of this error
}

func (c reloadingCreds) ServerHandshake(raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return c.current().ServerHandshake(raw) //nolint:wrapcheck // same contract as ClientHandshake
}

func (c reloadingCreds) Info() credentials.ProtocolInfo { return c.current().Info() }

func (c reloadingCreds) Clone() credentials.TransportCredentials { return c }

// OverrideServerName is unused by grpc-go; use ClientConfig.ServerName.
func (reloadingCreds) OverrideServerName(string) error { return nil }
